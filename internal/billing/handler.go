package billing

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	service *Service
	logger  *slog.Logger
	maxBody int64
}

func NewHandler(service *Service, logger *slog.Logger, maxBody int64) *Handler {
	return &Handler{service, logger, maxBody}
}
func WritePaymentRequired(w http.ResponseWriter, r *http.Request, err error) bool {
	var required *PaymentRequired
	if !errors.As(err, &required) {
		return false
	}
	httpx.WriteError(w, r, http.StatusPaymentRequired, "PAYMENT_REQUIRED", required.Error(), map[string]any{"feature": required.Feature, "skill": required.Skill, "remaining": required.Remaining})
	return true
}
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if WritePaymentRequired(w, r, err) {
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, 404, "NOT_FOUND", "Billing resource was not found", nil)
	case errors.Is(err, ErrValidation):
		httpx.WriteError(w, r, 400, "VALIDATION_ERROR", "Request validation failed", nil)
	case errors.Is(err, ErrProviderUnavailable):
		httpx.WriteError(w, r, 503, "PAYMENT_PROVIDER_UNAVAILABLE", "Payment provider is not configured or unavailable", nil)
	case errors.Is(err, ErrInvalidCallback):
		httpx.WriteError(w, r, 400, "INVALID_PAYMENT_CALLBACK", "Payment callback is invalid", nil)
	case errors.Is(err, ErrPromocodeNotFound):
		httpx.WriteError(w, r, 404, "PROMOCODE_NOT_FOUND", "Промокод не найден или неактивен", nil)
	case errors.Is(err, ErrPromocodeExpired):
		httpx.WriteError(w, r, 400, "PROMOCODE_EXPIRED", "Срок действия промокода истёк", nil)
	case errors.Is(err, ErrPromocodeLimitReached):
		httpx.WriteError(w, r, 400, "PROMOCODE_LIMIT_REACHED", "Лимит использований промокода исчерпан", nil)
	case errors.Is(err, ErrPromocodeMinPrice):
		httpx.WriteError(w, r, 400, "PROMOCODE_MIN_PRICE", "Сумма заказа меньше минимальной суммы для этого промокода", nil)
	case errors.Is(err, ErrPromocodeNotOnSale):
		httpx.WriteError(w, r, 400, "PROMOCODE_NOT_ON_SALE", "Промокод нельзя применить к акционному тарифу", nil)
	case errors.Is(err, ErrPromocodePlanMismatch):
		httpx.WriteError(w, r, 400, "PROMOCODE_PLAN_MISMATCH", "Промокод не действует на этот тариф", nil)
	case errors.Is(err, ErrPromocodeUserLimit):
		httpx.WriteError(w, r, 400, "PROMOCODE_ALREADY_USED", "Вы уже использовали этот промокод", nil)
	default:
		httpx.InternalError(w, r, h.logger, err)
	}
}
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := httpx.DecodeJSON(w, r, h.maxBody, v); err != nil {
		h.writeError(w, r, ErrValidation)
		return false
	}
	return true
}
func (h *Handler) id(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, key))
	if err != nil {
		h.writeError(w, r, ErrValidation)
		return uuid.Nil, false
	}
	return id, true
}
func (h *Handler) Plans(w http.ResponseWriter, r *http.Request) {
	p, err := h.service.repo.Plans(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"currency": "KZT", "plans": p})
}
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if auth.Role(r.Context()) == "GUEST" {
		httpx.WriteJSON(w, 200, map[string]any{"paywallEnabled": h.service.enabled, "guest": true})
		return
	}
	user, _ := auth.UserID(r.Context())
	a, err := h.service.Access(r.Context(), user)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if a.Guest {
		httpx.WriteJSON(w, 200, map[string]any{"paywallEnabled": h.service.enabled, "guest": true})
		return
	}
	httpx.WriteJSON(w, 200, a)
}
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	var input struct {
		PlanCode      string `json:"planCode"`
		AcceptedTerms bool   `json:"acceptedTerms"`
		Promocode     string `json:"promocode"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	user, _ := auth.UserID(r.Context())
	o, redirect, err := h.service.Checkout(r.Context(), user, input.PlanCode, input.AcceptedTerms, input.Promocode)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"orderId": o.ID, "redirectUrl": redirect, "free": o.Amount == 0})
}
func (h *Handler) ValidatePromocode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	var input struct {
		Code     string `json:"code"`
		PlanCode string `json:"planCode"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	user, _ := auth.UserID(r.Context())
	res, _, err := h.service.ValidatePromocode(r.Context(), input.Code, input.PlanCode, user)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, res)
}
func (h *Handler) Order(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	id, ok := h.id(w, r, "orderID")
	if !ok {
		return
	}
	user, _ := auth.UserID(r.Context())
	o, err := h.service.repo.Order(r.Context(), id)
	if err == nil && o.UserID != user {
		err = ErrNotFound
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"id": o.ID, "status": o.Status, "planCode": o.PlanCode, "amount": o.Amount, "listPrice": o.ListPrice, "provider": o.Provider, "providerOrderId": o.ProviderOrderID, "createdAt": o.CreatedAt, "paidAt": o.PaidAt})
}
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBody)
	if err := h.service.Callback(r, chi.URLParam(r, "provider")); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) AdminOrders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	limit, offset := 50, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			h.writeError(w, r, ErrValidation)
			return
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 {
			h.writeError(w, r, ErrValidation)
			return
		}
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "", "PENDING", "PAID", "FAILED", "CANCELLED", "REFUNDED":
	default:
		h.writeError(w, r, ErrValidation)
		return
	}
	orders, err := h.service.repo.Orders(r.Context(), nil, status, limit, offset)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"orders": orders, "limit": limit, "offset": offset})
}
func (h *Handler) UserBilling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	user, ok := h.id(w, r, "userID")
	if !ok {
		return
	}
	a, err := h.service.Access(r.Context(), user)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	entitlements, err := h.service.repo.Entitlements(r.Context(), user)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	orders, err := h.service.repo.Orders(r.Context(), &user, "", 0, 0)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"access": a, "entitlements": entitlements, "orders": orders})
}
func (h *Handler) Grant(w http.ResponseWriter, r *http.Request) {
	user, ok := h.id(w, r, "userID")
	if !ok {
		return
	}
	var input GrantInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	e, err := h.service.Grant(r.Context(), user, actor, input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 201, e)
}
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r, "entitlementID")
	if !ok {
		return
	}
	var input struct {
		Note string `json:"note"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if len(input.Note) > 4000 {
		h.writeError(w, r, ErrValidation)
		return
	}
	if err := h.service.repo.Revoke(r.Context(), id, input.Note); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) Meetings(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r, "entitlementID")
	if !ok {
		return
	}
	var input struct {
		Used *int `json:"meetingsUsed"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if input.Used == nil || *input.Used < 0 {
		h.writeError(w, r, ErrValidation)
		return
	}
	if err := h.service.repo.Meetings(r.Context(), id, *input.Used); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	v, err := h.service.Settings(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, v)
}
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Listening *int   `json:"freeListening"`
		Reading   *int   `json:"freeReading"`
		Writing   *int   `json:"freeWriting"`
		Speaking  *int   `json:"freeSpeaking"`
		Assistant *int64 `json:"freeAssistantDailyLimit"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if input.Listening == nil || input.Reading == nil || input.Writing == nil || input.Speaking == nil || input.Assistant == nil {
		h.writeError(w, r, ErrValidation)
		return
	}
	v, err := h.service.UpdateSettings(r.Context(), Settings{FreeListening: *input.Listening, FreeReading: *input.Reading, FreeWriting: *input.Writing, FreeSpeaking: *input.Speaking, FreeAssistantDailyLimit: *input.Assistant})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, v)
}
func (h *Handler) UpdatePlan(w http.ResponseWriter, r *http.Request) {
	var input PlanInput
	if !h.decode(w, r, &input) {
		return
	}
	p, err := h.service.UpdatePlan(r.Context(), chi.URLParam(r, "code"), input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, p)
}

// AssistantLimit composes with the existing middleware for paid accounts and
// disabled paywall. A free account consumes only the separate daily quota.
func (h *Handler) AssistantLimit(legacy func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		paid := legacy(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := auth.UserID(r.Context())
			handled, err := h.service.AssistantAdmission(r.Context(), user)
			if err != nil {
				h.writeError(w, r, err)
				return
			}
			if handled {
				next.ServeHTTP(w, r)
				return
			}
			paid.ServeHTTP(w, r)
		})
	}
}

func (h *Handler) ListPromocodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	var partnerID *uuid.UUID
	if raw := r.URL.Query().Get("partnerUserId"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			partnerID = &id
		}
	}
	var activeOnly *bool
	if raw := r.URL.Query().Get("active"); raw != "" {
		b := raw == "true" || raw == "1"
		activeOnly = &b
	}
	list, err := h.service.ListPromocodes(r.Context(), partnerID, activeOnly)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"promocodes": list})
}

func (h *Handler) CreatePromocode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	var input CreatePromocodeInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	promo, err := h.service.CreatePromocode(r.Context(), input, actor)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 201, promo)
}

func (h *Handler) TogglePromocodeStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	id, ok := h.id(w, r, "id")
	if !ok {
		return
	}
	var input struct {
		IsActive bool `json:"isActive"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	promo, err := h.service.TogglePromocodeStatus(r.Context(), id, input.IsActive)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, promo)
}

func (h *Handler) DeletePromocode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	id, ok := h.id(w, r, "id")
	if !ok {
		return
	}
	if err := h.service.DeletePromocode(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) PromocodeRedemptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	id, ok := h.id(w, r, "id")
	if !ok {
		return
	}
	list, err := h.service.PromocodeRedemptions(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"redemptions": list})
}
