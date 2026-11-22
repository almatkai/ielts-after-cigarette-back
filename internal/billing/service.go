package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/ailimits"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type dailyLimiter interface {
	Allow(context.Context, string, string, int64) (cache.DailyLimitResult, error)
	Check(context.Context, string, string, int64) (cache.DailyLimitResult, error)
}
type paidLimits interface {
	Get(context.Context) (ailimits.Limits, error)
}
type Service struct {
	repo      Repository
	enabled   bool
	provider  Provider
	returnURL string
	daily     dailyLimiter
	paid      paidLimits
}

func NewService(repo Repository, enabled bool, provider Provider, returnURL string, daily dailyLimiter, paid paidLimits) *Service {
	return &Service{repo: repo, enabled: enabled, provider: provider, returnURL: returnURL, daily: daily, paid: paid}
}
func (s *Service) Enabled() bool { return s != nil && s.enabled }
func (s *Service) Access(ctx context.Context, user uuid.UUID) (Access, error) {
	a, err := s.repo.Snapshot(ctx, user)
	if err != nil {
		return a, err
	}
	a.PaywallEnabled = s.enabled
	if a.Staff {
		a.Assistant = AssistantQuota{}
		return a, nil
	}
	if !s.enabled || a.Active || a.Guest {
		if s.paid != nil {
			limits, err := s.paid.Get(ctx)
			if err != nil {
				return a, err
			}
			a.Assistant.Limit = limits.AssistantLimit
			if a.Guest {
				a.Assistant.Limit = limits.GuestAssistantLimit
			}
		}
	}
	a.Assistant.Remaining = a.Assistant.Limit
	if s.daily != nil && a.Assistant.Limit > 0 {
		scope := "billing:assistant:free"
		if !s.enabled || a.Active {
			scope = "assistant:user"
		}
		if a.Guest {
			scope = "assistant:guest"
		}
		result, err := s.daily.Check(ctx, scope, user.String(), a.Assistant.Limit)
		if err != nil {
			return a, err
		}
		a.Assistant.Remaining = result.Remaining
	}
	return a, nil
}
func quotaError(a Access, feature string, skills []string) error {
	if a.Staff || a.Guest || a.Active {
		return nil
	}
	remaining := map[string]int{}
	for _, skill := range Skills {
		remaining[skill] = a.Free[skill].Remaining
	}
	for _, skill := range skills {
		if remaining[skill] < 1 {
			return &PaymentRequired{Feature: feature, Skill: skill, Remaining: remaining}
		}
	}
	return nil
}

// ConsumeTx locks the account before checking entitlement/quota and recording
// usage. The caller MUST use this transaction for creation and commit only
// after the attempt/session is fully persisted. Rollback also rolls back usage.
func (s *Service) ConsumeTx(ctx context.Context, tx pgx.Tx, user uuid.UUID, kind string, id uuid.UUID, skills []string) error {
	if !s.Enabled() {
		return nil
	}
	if err := lockUser(ctx, tx, user); err != nil {
		return err
	}
	a, err := snapshot(ctx, tx, user)
	if err != nil {
		return err
	}
	if a.Staff || a.Guest || a.Active {
		return nil
	}
	feature := skills[0]
	if kind == "FULL_MOCK_SESSION" {
		feature = "full_mock"
	}
	pending := []string{}
	for _, skill := range skills {
		if _, ok := a.Free[skill]; !ok {
			return ErrValidation
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM free_usage WHERE source_kind=$1 AND source_id=$2 AND skill=$3)`, kind, id, skill).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			pending = append(pending, skill)
		}
	}
	if err := quotaError(a, feature, pending); err != nil {
		return err
	}
	return recordUsage(ctx, tx, user, kind, id, pending)
}
func recordUsage(ctx context.Context, tx pgx.Tx, user uuid.UUID, kind string, id uuid.UUID, skills []string) error {
	for _, skill := range skills {
		if _, err := tx.Exec(ctx, `INSERT INTO free_usage(user_id,skill,source_kind,source_id) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, user, skill, kind, id); err != nil {
			return err
		}
	}
	return nil
}

// A completed guest mock is historical usage. Claim must succeed even if the
// account already exhausted its quota or has paid access; it is charged once.
func (s *Service) ClaimTx(ctx context.Context, tx pgx.Tx, user, session uuid.UUID) error {
	if !s.Enabled() {
		return nil
	}
	if err := lockUser(ctx, tx, user); err != nil {
		return err
	}
	var staff bool
	if err := tx.QueryRow(ctx, `SELECT role IN ('ADMIN','EDITOR') FROM users WHERE id=$1`, user).Scan(&staff); err != nil {
		return err
	}
	if staff {
		return nil
	}
	return recordUsage(ctx, tx, user, "FULL_MOCK_SESSION", session, Skills)
}
func entitlementWindow(now time.Time, last *time.Time, days int) (time.Time, time.Time) {
	start := now.UTC()
	if last != nil && last.After(start) {
		start = last.UTC()
	}
	// Kazakhstan calendar days, including queued grants. No auto-renewal.
	loc, _ := time.LoadLocation(cache.TimeZone)
	return start, start.In(loc).AddDate(0, 0, days).UTC()
}
func (s *Service) Checkout(ctx context.Context, user uuid.UUID, code string, accepted bool, promocodes ...string) (Order, string, error) {
	if !accepted {
		return Order{}, "", ErrValidation
	}
	var promocode string
	if len(promocodes) > 0 {
		promocode = strings.TrimSpace(promocodes[0])
	}
	if s.provider == nil && promocode == "" {
		return Order{}, "", ErrProviderUnavailable
	}
	if s.repo == nil {
		return Order{}, "", ErrNotFound
	}
	p, err := s.repo.Plan(ctx, code)
	if err != nil {
		return Order{}, "", err
	}
	if !p.Active {
		return Order{}, "", ErrNotFound
	}
	a, err := s.repo.Snapshot(ctx, user)
	if err != nil {
		return Order{}, "", err
	}
	if a.Guest {
		return Order{}, "", ErrValidation
	}

	finalPrice := p.Price
	var discountAmount int64
	var promoID *uuid.UUID

	if promocode != "" {
		validation, promo, err := s.ValidatePromocode(ctx, promocode, p.Code, user)
		if err != nil {
			return Order{}, "", err
		}
		discountAmount = validation.DiscountAmount
		finalPrice = validation.FinalPrice
		promoID = &promo.ID
	}

	if finalPrice == 0 {
		o := Order{
			ID:             uuid.New(),
			UserID:         user,
			PlanCode:       p.Code,
			ListPrice:      p.ListPrice,
			Amount:         0,
			DiscountAmount: discountAmount,
			PromocodeID:    promoID,
			Provider:       "promocode",
			AccessDays:     p.AccessDays,
			MeetingsCount:  p.MeetingsCount,
		}
		o, err := s.repo.CreateFreeOrder(ctx, o)
		if err != nil {
			return o, "", err
		}
		target, err := returnTarget(s.returnURL, o.ID)
		if err != nil {
			target = s.returnURL
		}
		return o, target, nil
	}

	if s.provider == nil {
		return Order{}, "", ErrProviderUnavailable
	}

	o, err := s.repo.CreateOrder(ctx, Order{
		ID:             uuid.New(),
		UserID:         user,
		PlanCode:       p.Code,
		ListPrice:      p.ListPrice,
		Amount:         finalPrice,
		DiscountAmount: discountAmount,
		PromocodeID:    promoID,
		Provider:       s.provider.Name(),
		AccessDays:     p.AccessDays,
		MeetingsCount:  p.MeetingsCount,
	})
	if err != nil {
		return o, "", err
	}
	redirect, providerID, err := s.provider.CreatePayment(ctx, o, s.returnURL)
	if err != nil {
		return o, "", fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	if err = s.repo.SetProviderOrder(ctx, o.ID, providerID); err != nil {
		return o, "", err
	}
	return o, redirect, nil
}

func (s *Service) ValidatePromocode(ctx context.Context, code, planCode string, user uuid.UUID) (PromocodeValidation, *Promocode, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return PromocodeValidation{}, nil, ErrPromocodeNotFound
	}
	promo, err := s.repo.PromocodeByCode(ctx, code)
	if err != nil {
		return PromocodeValidation{}, nil, err
	}
	now := time.Now().UTC()
	if !promo.IsActive || now.Before(promo.StartsAt) {
		return PromocodeValidation{}, nil, ErrPromocodeNotFound
	}
	if promo.ExpiresAt != nil && now.After(*promo.ExpiresAt) {
		return PromocodeValidation{}, nil, ErrPromocodeExpired
	}
	if promo.MaxUses != nil && promo.UsedCount >= *promo.MaxUses {
		return PromocodeValidation{}, nil, ErrPromocodeLimitReached
	}

	p, err := s.repo.Plan(ctx, planCode)
	if err != nil {
		return PromocodeValidation{}, nil, err
	}
	if !p.Active {
		return PromocodeValidation{}, nil, ErrNotFound
	}
	if promo.PlanCode != nil && *promo.PlanCode != p.Code {
		return PromocodeValidation{}, nil, ErrPromocodePlanMismatch
	}
	if promo.MinPriceKZT != nil && p.Price < *promo.MinPriceKZT {
		return PromocodeValidation{}, nil, ErrPromocodeMinPrice
	}
	if !promo.AllowOnSale && p.SalePrice != nil {
		return PromocodeValidation{}, nil, ErrPromocodeNotOnSale
	}

	maxPerUser := promo.MaxUsesPerUser
	if maxPerUser <= 0 {
		maxPerUser = 1
	}
	if user != uuid.Nil {
		redemptions, err := s.repo.UserPromocodeRedemptionsCount(ctx, promo.ID, user)
		if err != nil {
			return PromocodeValidation{}, nil, err
		}
		if redemptions >= maxPerUser {
			return PromocodeValidation{}, nil, ErrPromocodeUserLimit
		}
	}

	basePrice := p.Price
	var discountAmount int64
	var finalPrice int64
	var isFree bool

	switch promo.DiscountType {
	case "FREE":
		discountAmount = basePrice
		finalPrice = 0
		isFree = true
	case "PERCENT":
		discountAmount = (basePrice * int64(promo.DiscountValue)) / 100
		finalPrice = basePrice - discountAmount
		isFree = finalPrice <= 0
	case "FIXED":
		discountAmount = int64(promo.DiscountValue)
		if discountAmount > basePrice {
			discountAmount = basePrice
		}
		finalPrice = basePrice - discountAmount
		isFree = finalPrice <= 0
	default:
		return PromocodeValidation{}, nil, ErrValidation
	}

	if finalPrice < 0 {
		finalPrice = 0
	}

	return PromocodeValidation{
		Valid:          true,
		Code:           promo.Code,
		DiscountType:   promo.DiscountType,
		DiscountValue:  promo.DiscountValue,
		DiscountAmount: discountAmount,
		OriginalPrice:  basePrice,
		FinalPrice:     finalPrice,
		IsFree:         isFree,
	}, &promo, nil
}

func (s *Service) ListPromocodes(ctx context.Context, partnerUserID *uuid.UUID, activeOnly *bool) ([]Promocode, error) {
	return s.repo.ListPromocodes(ctx, partnerUserID, activeOnly)
}
func (s *Service) CreatePromocode(ctx context.Context, input CreatePromocodeInput, actor uuid.UUID) (Promocode, error) {
	return s.repo.CreatePromocode(ctx, input, actor)
}
func (s *Service) TogglePromocodeStatus(ctx context.Context, id uuid.UUID, isActive bool) (Promocode, error) {
	return s.repo.UpdatePromocodeStatus(ctx, id, isActive)
}
func (s *Service) DeletePromocode(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeletePromocode(ctx, id)
}
func (s *Service) PromocodeRedemptions(ctx context.Context, id uuid.UUID) ([]PromocodeRedemption, error) {
	return s.repo.PromocodeRedemptions(ctx, id)
}
func (s *Service) Notify(ctx context.Context, provider string, n Notification) error {
	_, _, err := s.repo.Notify(ctx, provider, n)
	return err
}
func (s *Service) Callback(r *http.Request, provider string) error {
	if s.provider == nil || provider != s.provider.Name() {
		return ErrProviderUnavailable
	}
	n, err := s.provider.ParseCallback(r)
	if err != nil {
		return err
	}
	return s.Notify(r.Context(), provider, n)
}
func (s *Service) Grant(ctx context.Context, user, actor uuid.UUID, input GrantInput) (Entitlement, error) {
	if input.PlanCode == "" || (input.Source != "ADMIN" && input.Source != "BONUS") || (input.Days != nil && (*input.Days <= 0 || *input.Days > 36500)) || len(input.Note) > 4000 {
		return Entitlement{}, ErrValidation
	}
	return s.repo.Grant(ctx, user, actor, input)
}
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	v, err := s.repo.Settings(ctx)
	v.PaywallEnabled = s.enabled
	return v, err
}
func (s *Service) UpdateSettings(ctx context.Context, v Settings) (Settings, error) {
	if v.FreeListening < 0 || v.FreeReading < 0 || v.FreeWriting < 0 || v.FreeSpeaking < 0 || v.FreeAssistantDailyLimit < 0 {
		return v, ErrValidation
	}
	result, err := s.repo.UpdateSettings(ctx, v)
	result.PaywallEnabled = s.enabled
	return result, err
}
func (s *Service) UpdatePlan(ctx context.Context, code string, input PlanInput) (Plan, error) {
	if input.ListPrice == nil || input.Active == nil || *input.ListPrice < 0 || input.SalePrice != nil && (*input.SalePrice < 0 || *input.SalePrice > *input.ListPrice) {
		return Plan{}, ErrValidation
	}
	return s.repo.UpdatePlan(ctx, code, input)
}

// AssistantAdmission returns handled=true for a free account or staff, so the
// caller skips the legacy paid daily limiter. Paid/guest/disabled use that path.
func (s *Service) AssistantAdmission(ctx context.Context, user uuid.UUID) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	a, err := s.repo.Snapshot(ctx, user)
	if err != nil {
		return false, err
	}
	if a.Staff {
		return true, nil
	}
	if a.Guest || a.Active {
		return false, nil
	}
	required := &PaymentRequired{Feature: "assistant", Skill: "assistant", Remaining: map[string]int{}}
	for _, skill := range Skills {
		required.Remaining[skill] = a.Free[skill].Remaining
	}
	if a.Assistant.Limit <= 0 {
		return true, required
	}
	if s.daily == nil {
		return true, errors.New("assistant limiter unavailable")
	}
	if availability, ok := s.daily.(interface{ Available() bool }); ok && !availability.Available() {
		return true, errors.New("assistant limiter unavailable")
	}
	result, err := s.daily.Allow(ctx, "billing:assistant:free", user.String(), a.Assistant.Limit)
	if err != nil {
		return true, err
	}
	if !result.Allowed {
		return true, required
	}
	return true, nil
}

// WithFakeProvider is only available outside production; config rejects the
// same combination at startup before the router is built.
func (s *Service) WithFakeProvider(environment string) error {
	p, err := NewFakeProvider(environment, s.Notify)
	if err != nil {
		return err
	}
	s.provider = p
	return nil
}
