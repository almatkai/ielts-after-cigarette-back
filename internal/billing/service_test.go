package billing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/google/uuid"
)

func freeAccess() Access {
	a := Access{Free: map[string]Quota{}, Assistant: AssistantQuota{Limit: 5, Remaining: 5}}
	for _, skill := range Skills {
		a.Free[skill] = Quota{Limit: 3, Remaining: 3}
	}
	return a
}
func TestQuotaRequiresEveryMockSkill(t *testing.T) {
	a := freeAccess()
	if err := quotaError(a, "full_mock", Skills); err != nil {
		t.Fatal(err)
	}
	for _, skill := range Skills {
		t.Run(skill, func(t *testing.T) {
			a := freeAccess()
			a.Free[skill] = Quota{Limit: 3, Used: 3}
			if err := quotaError(a, skill, []string{skill}); err == nil {
				t.Fatal("exhausted skill allowed")
			}
			var required *PaymentRequired
			if err := quotaError(a, "full_mock", Skills); !errors.As(err, &required) || required.Feature != "full_mock" || required.Skill != skill || required.Remaining[skill] != 0 {
				t.Fatalf("mock requirement: %v", err)
			}
		})
	}
	a.Free["reading"] = Quota{Limit: 3, Used: 3}
	a.Free["speaking"] = Quota{Limit: 3, Used: 3}
	var required *PaymentRequired
	if err := quotaError(a, "full_mock", Skills); !errors.As(err, &required) || required.Skill != "reading" {
		t.Fatalf("first exhausted skill: %v", err)
	}
}
func TestQuotaBypassesStaffGuestAndPaid(t *testing.T) {
	for _, a := range []Access{{Staff: true}, {Guest: true}, {Active: true}} {
		if err := quotaError(a, "full_mock", Skills); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(nil, false, nil, "", nil, nil)
	if err := s.ConsumeTx(context.Background(), nil, uuid.New(), "FULL_MOCK_SESSION", uuid.New(), Skills); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimTx(context.Background(), nil, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	handled, err := s.AssistantAdmission(context.Background(), uuid.New())
	if handled || err != nil {
		t.Fatalf("disabled admission: %v %v", handled, err)
	}
}
func TestEntitlementQueueWindow(t *testing.T) {
	now := time.Date(2026, 10, 29, 21, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		last *time.Time
		want time.Time
	}{
		{"new", nil, now}, {"expired", ptr(now.Add(-time.Hour)), now}, {"queued", ptr(now.AddDate(0, 0, 60)), now.AddDate(0, 0, 60)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end := entitlementWindow(now, tc.last, 30)
			if !start.Equal(tc.want) || !end.Equal(tc.want.AddDate(0, 0, 30)) {
				t.Fatalf("window: %v %v", start, end)
			}
		})
	}
}
func ptr[T any](v T) *T { return &v }

type serviceRepo struct {
	Repository
	access        Access
	plan          Plan
	created       []Order
	notifications []Notification
	notifyErr     error
}

func (r *serviceRepo) Snapshot(context.Context, uuid.UUID) (Access, error) { return r.access, nil }
func (r *serviceRepo) Plan(context.Context, string) (Plan, error)          { return r.plan, nil }
func (r *serviceRepo) CreateOrder(_ context.Context, o Order) (Order, error) {
	r.created = append(r.created, o)
	return o, nil
}
func (r *serviceRepo) SetProviderOrder(context.Context, uuid.UUID, string) error { return nil }
func (r *serviceRepo) Notify(_ context.Context, _ string, n Notification) (Order, bool, error) {
	r.notifications = append(r.notifications, n)
	return Order{}, true, r.notifyErr
}

type countingLimiter struct {
	count int64
	scope string
	err   error
}

func (l *countingLimiter) Allow(_ context.Context, scope, _ string, limit int64) (cache.DailyLimitResult, error) {
	l.scope = scope
	if l.err != nil {
		return cache.DailyLimitResult{}, l.err
	}
	allowed := l.count < limit
	if allowed {
		l.count++
	}
	return cache.DailyLimitResult{Allowed: allowed, Remaining: max(0, limit-l.count)}, nil
}
func (l *countingLimiter) Check(_ context.Context, scope, _ string, limit int64) (cache.DailyLimitResult, error) {
	l.scope = scope
	return cache.DailyLimitResult{Allowed: l.count < limit, Remaining: max(0, limit-l.count)}, l.err
}
func TestAssistantUsesSeparateDailyQuota(t *testing.T) {
	repo := &serviceRepo{access: freeAccess()}
	limiter := &countingLimiter{}
	s := NewService(repo, true, nil, "", limiter, nil)
	user := uuid.New()
	ctx := context.Background()
	for range 5 {
		handled, err := s.AssistantAdmission(ctx, user)
		if !handled || err != nil {
			t.Fatalf("free admission: %v %v", handled, err)
		}
	}
	handled, err := s.AssistantAdmission(ctx, user)
	var required *PaymentRequired
	if !handled || !errors.As(err, &required) || required.Feature != "assistant" || limiter.scope != "billing:assistant:free" {
		t.Fatalf("free limit: %v %v", handled, err)
	}
	a, err := s.Access(ctx, user)
	if err != nil || a.Assistant.Remaining != 0 {
		t.Fatalf("remaining: %+v %v", a, err)
	}
	for _, access := range []Access{{Active: true}, {Guest: true}, {Staff: true}} {
		repo.access = access
		handled, err = s.AssistantAdmission(ctx, user)
		if err != nil || handled != access.Staff {
			t.Fatalf("bypass %+v: %v %v", access, handled, err)
		}
	}
	repo.access = freeAccess()
	repo.access.Assistant.Limit = 0
	if _, err = s.AssistantAdmission(ctx, user); !errors.As(err, &required) {
		t.Fatal("zero must deny all")
	}
	repo.access = freeAccess()
	limiter.err = errors.New("redis unavailable")
	if _, err = s.AssistantAdmission(ctx, user); !errors.Is(err, limiter.err) {
		t.Fatalf("dependency failed open: %v", err)
	}
}
func TestCheckoutValidationAndFakeSettlementUseSameNotificationPath(t *testing.T) {
	repo := &serviceRepo{access: freeAccess(), plan: Plan{Code: "self", ListPrice: 9990, SalePrice: ptr(int64(4990)), Price: 4990, AccessDays: 30, Active: true}}
	s := NewService(repo, true, nil, "https://app.example/billing/return?from=checkout", nil, nil)
	user := uuid.New()
	ctx := context.Background()
	if _, _, err := s.Checkout(ctx, user, "self", false); !errors.Is(err, ErrValidation) {
		t.Fatal(err)
	}
	if _, _, err := s.Checkout(ctx, user, "self", true); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatal(err)
	}
	if len(repo.created) != 0 {
		t.Fatal("unavailable provider created order")
	}
	if err := s.WithFakeProvider("production"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatal("fake allowed in production")
	}
	if err := s.WithFakeProvider("development"); err != nil {
		t.Fatal(err)
	}
	o, redirect, err := s.Checkout(ctx, user, "self", true)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("orderId") != o.ID.String() || u.Query().Get("from") != "checkout" || len(repo.notifications) != 1 || repo.notifications[0].OrderID != o.ID || repo.notifications[0].Amount != 4990 || o.ListPrice != 9990 {
		t.Fatalf("checkout: %+v %s %+v", o, redirect, repo.notifications)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"status":"PAID"}`))
	if err := s.Callback(req, "fake"); !errors.Is(err, ErrInvalidCallback) || len(repo.notifications) != 1 {
		t.Fatal("unsigned fake callback accepted")
	}
	repo.access.Guest = true
	if _, _, err = s.Checkout(ctx, user, "self", true); !errors.Is(err, ErrValidation) {
		t.Fatalf("guest checkout: %v", err)
	}
	repo.access.Guest = false
	repo.plan.Active = false
	if _, _, err = s.Checkout(ctx, user, "self", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive plan: %v", err)
	}
}
func TestAccessDoesNotRetainStaleEntitlement(t *testing.T) {
	repo := &serviceRepo{access: freeAccess()}
	s := NewService(repo, true, nil, "", nil, nil)
	ctx := context.Background()
	user := uuid.New()
	a, err := s.Access(ctx, user)
	if err != nil || a.Active {
		t.Fatal(a, err)
	}
	repo.access.Active = true
	a, err = s.Access(ctx, user)
	if err != nil || !a.Active {
		t.Fatal(a, err)
	}
	repo.access.Active = false
	a, err = s.Access(ctx, user)
	if err != nil || a.Active {
		t.Fatal(a, err)
	}
}
