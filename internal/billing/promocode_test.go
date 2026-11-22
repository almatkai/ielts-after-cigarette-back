package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type promoTestRepo struct {
	Repository
	plan       Plan
	access     Access
	promos     map[string]Promocode
	redemptions map[uuid.UUID]int
	freeOrders []Order
	orders     []Order
}

func (r *promoTestRepo) Plan(_ context.Context, code string) (Plan, error) {
	if r.plan.Code == code {
		return r.plan, nil
	}
	return Plan{}, ErrNotFound
}

func (r *promoTestRepo) Snapshot(_ context.Context, _ uuid.UUID) (Access, error) {
	return r.access, nil
}

func (r *promoTestRepo) PromocodeByCode(_ context.Context, code string) (Promocode, error) {
	if p, ok := r.promos[code]; ok {
		return p, nil
	}
	return Promocode{}, ErrPromocodeNotFound
}

func (r *promoTestRepo) UserPromocodeRedemptionsCount(_ context.Context, promoID, _ uuid.UUID) (int, error) {
	return r.redemptions[promoID], nil
}

func (r *promoTestRepo) CreateOrder(_ context.Context, o Order) (Order, error) {
	r.orders = append(r.orders, o)
	return o, nil
}

func (r *promoTestRepo) CreateFreeOrder(_ context.Context, o Order) (Order, error) {
	o.Status = "PAID"
	r.freeOrders = append(r.freeOrders, o)
	return o, nil
}

func (r *promoTestRepo) SetProviderOrder(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func TestPromocodeValidation(t *testing.T) {
	ctx := context.Background()
	user := uuid.New()
	salePrice := int64(7990)
	plan := Plan{Code: "self", Title: "1 месяц", ListPrice: 14990, SalePrice: &salePrice, Price: 7990, AccessDays: 30, Active: true}

	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)
	maxUses := 10
	usedCountFull := 10
	minPriceHigh := int64(20000)
	specificPlan := "year_1"

	repo := &promoTestRepo{
		plan:   plan,
		access: Access{PaywallEnabled: true},
		redemptions: map[uuid.UUID]int{},
		promos: map[string]Promocode{
			"PERCENT20": {
				ID: uuid.New(), Code: "PERCENT20", DiscountType: "PERCENT", DiscountValue: 20,
				IsActive: true, StartsAt: past, AllowOnSale: true,
			},
			"FIXED2000": {
				ID: uuid.New(), Code: "FIXED2000", DiscountType: "FIXED", DiscountValue: 2000,
				IsActive: true, StartsAt: past, AllowOnSale: true,
			},
			"FREE100": {
				ID: uuid.New(), Code: "FREE100", DiscountType: "FREE", DiscountValue: 0,
				IsActive: true, StartsAt: past, AllowOnSale: true,
			},
			"EXPIRED": {
				ID: uuid.New(), Code: "EXPIRED", DiscountType: "PERCENT", DiscountValue: 50,
				IsActive: true, StartsAt: past.Add(-48 * time.Hour), ExpiresAt: &past, AllowOnSale: true,
			},
			"NOTSTARTED": {
				ID: uuid.New(), Code: "NOTSTARTED", DiscountType: "PERCENT", DiscountValue: 50,
				IsActive: true, StartsAt: future, AllowOnSale: true,
			},
			"LIMITREACHED": {
				ID: uuid.New(), Code: "LIMITREACHED", DiscountType: "PERCENT", DiscountValue: 50,
				IsActive: true, StartsAt: past, MaxUses: &maxUses, UsedCount: usedCountFull, AllowOnSale: true,
			},
			"MINPRICE": {
				ID: uuid.New(), Code: "MINPRICE", DiscountType: "PERCENT", DiscountValue: 50,
				IsActive: true, StartsAt: past, MinPriceKZT: &minPriceHigh, AllowOnSale: true,
			},
			"NOSALE": {
				ID: uuid.New(), Code: "NOSALE", DiscountType: "PERCENT", DiscountValue: 50,
				IsActive: true, StartsAt: past, AllowOnSale: false,
			},
			"WRONGPLAN": {
				ID: uuid.New(), Code: "WRONGPLAN", DiscountType: "PERCENT", DiscountValue: 50,
				IsActive: true, StartsAt: past, PlanCode: &specificPlan, AllowOnSale: true,
			},
			"ONCE": {
				ID: uuid.New(), Code: "ONCE", DiscountType: "PERCENT", DiscountValue: 50,
				IsActive: true, StartsAt: past, MaxUsesPerUser: 1, AllowOnSale: true,
			},
		},
	}

	s := NewService(repo, true, &fakeProvider{notify: func(context.Context, string, Notification) error { return nil }}, "https://example.com/return", nil, nil)

	t.Run("Valid 20% discount", func(t *testing.T) {
		res, promo, err := s.ValidatePromocode(ctx, "PERCENT20", "self", user)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if promo == nil || promo.Code != "PERCENT20" {
			t.Fatalf("wrong promo returned")
		}
		if res.DiscountAmount != 1598 || res.FinalPrice != 6392 || res.IsFree {
			t.Fatalf("wrong calculation: discount=%d, final=%d, isFree=%v", res.DiscountAmount, res.FinalPrice, res.IsFree)
		}
	})

	t.Run("Valid fixed discount 2000 KZT", func(t *testing.T) {
		res, _, err := s.ValidatePromocode(ctx, "FIXED2000", "self", user)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.DiscountAmount != 2000 || res.FinalPrice != 5990 || res.IsFree {
			t.Fatalf("wrong calculation: discount=%d, final=%d", res.DiscountAmount, res.FinalPrice)
		}
	})

	t.Run("Valid 100% free access", func(t *testing.T) {
		res, _, err := s.ValidatePromocode(ctx, "FREE100", "self", user)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.DiscountAmount != 7990 || res.FinalPrice != 0 || !res.IsFree {
			t.Fatalf("wrong calculation: discount=%d, final=%d, isFree=%v", res.DiscountAmount, res.FinalPrice, res.IsFree)
		}
	})

	t.Run("Expired promocode fails", func(t *testing.T) {
		_, _, err := s.ValidatePromocode(ctx, "EXPIRED", "self", user)
		if !errors.Is(err, ErrPromocodeExpired) {
			t.Fatalf("expected ErrPromocodeExpired, got: %v", err)
		}
	})

	t.Run("Not started promocode fails", func(t *testing.T) {
		_, _, err := s.ValidatePromocode(ctx, "NOTSTARTED", "self", user)
		if !errors.Is(err, ErrPromocodeNotFound) {
			t.Fatalf("expected ErrPromocodeNotFound, got: %v", err)
		}
	})

	t.Run("Limit reached fails", func(t *testing.T) {
		_, _, err := s.ValidatePromocode(ctx, "LIMITREACHED", "self", user)
		if !errors.Is(err, ErrPromocodeLimitReached) {
			t.Fatalf("expected ErrPromocodeLimitReached, got: %v", err)
		}
	})

	t.Run("Min price fails when plan price is below min", func(t *testing.T) {
		_, _, err := s.ValidatePromocode(ctx, "MINPRICE", "self", user)
		if !errors.Is(err, ErrPromocodeMinPrice) {
			t.Fatalf("expected ErrPromocodeMinPrice, got: %v", err)
		}
	})

	t.Run("Disallowed on sale fails when plan is on promotional sale", func(t *testing.T) {
		_, _, err := s.ValidatePromocode(ctx, "NOSALE", "self", user)
		if !errors.Is(err, ErrPromocodeNotOnSale) {
			t.Fatalf("expected ErrPromocodeNotOnSale, got: %v", err)
		}
	})

	t.Run("Plan code mismatch fails", func(t *testing.T) {
		_, _, err := s.ValidatePromocode(ctx, "WRONGPLAN", "self", user)
		if !errors.Is(err, ErrPromocodePlanMismatch) {
			t.Fatalf("expected ErrPromocodePlanMismatch, got: %v", err)
		}
	})

	t.Run("User limit reached fails", func(t *testing.T) {
		onceID := repo.promos["ONCE"].ID
		repo.redemptions[onceID] = 1
		_, _, err := s.ValidatePromocode(ctx, "ONCE", "self", user)
		if !errors.Is(err, ErrPromocodeUserLimit) {
			t.Fatalf("expected ErrPromocodeUserLimit, got: %v", err)
		}
	})
}

func TestCheckoutWithPromocodes(t *testing.T) {
	ctx := context.Background()
	user := uuid.New()
	salePrice := int64(7990)
	plan := Plan{Code: "self", Title: "1 месяц", ListPrice: 14990, SalePrice: &salePrice, Price: 7990, AccessDays: 30, Active: true}

	past := time.Now().Add(-time.Hour)
	freePromo := Promocode{
		ID: uuid.New(), Code: "FREE100", DiscountType: "FREE", DiscountValue: 0,
		IsActive: true, StartsAt: past, AllowOnSale: true,
	}
	discountPromo := Promocode{
		ID: uuid.New(), Code: "DISCOUNT20", DiscountType: "PERCENT", DiscountValue: 20,
		IsActive: true, StartsAt: past, AllowOnSale: true,
	}

	repo := &promoTestRepo{
		plan:   plan,
		access: Access{PaywallEnabled: true},
		redemptions: map[uuid.UUID]int{},
		promos: map[string]Promocode{
			"FREE100":    freePromo,
			"DISCOUNT20": discountPromo,
			"PERCENT20_5": {
				ID: uuid.New(), Code: "PERCENT20_5", DiscountType: "PERCENT", DiscountValue: 20.5,
				IsActive: true, StartsAt: past, AllowOnSale: true,
			},
		},
	}

	s := NewService(repo, true, &fakeProvider{notify: func(context.Context, string, Notification) error { return nil }}, "https://example.com/return", nil, nil)

	t.Run("Decimal percentage promocode calculates correctly", func(t *testing.T) {
		val, promo, err := s.ValidatePromocode(ctx, "PERCENT20_5", "self", user)
		if err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		// 7990 * 0.205 = 1637.95 -> 1638 discount -> final price 6352
		if val.DiscountAmount != 1638 || val.FinalPrice != 6352 {
			t.Fatalf("expected DiscountAmount=1638, FinalPrice=6352, got %d, %d", val.DiscountAmount, val.FinalPrice)
		}
		if promo.DiscountValue != 20.5 {
			t.Fatalf("expected DiscountValue=20.5, got %f", promo.DiscountValue)
		}
	})

	t.Run("Free promocode checkout grants instant access", func(t *testing.T) {
		order, redirect, err := s.Checkout(ctx, user, "self", true, "FREE100")
		if err != nil {
			t.Fatalf("checkout failed: %v", err)
		}
		if order.Amount != 0 || order.Status != "PAID" {
			t.Fatalf("expected Amount=0, Status=PAID, got amount=%d, status=%s", order.Amount, order.Status)
		}
		if len(repo.freeOrders) != 1 {
			t.Fatalf("expected 1 free order, got %d", len(repo.freeOrders))
		}
		if redirect == "" {
			t.Fatalf("expected redirect URL, got empty")
		}
	})

	t.Run("Discounted promocode creates order with reduced amount", func(t *testing.T) {
		order, redirect, err := s.Checkout(ctx, user, "self", true, "DISCOUNT20")
		if err != nil {
			t.Fatalf("checkout failed: %v", err)
		}
		if order.Amount != 6392 {
			t.Fatalf("expected Amount=6392, got %d", order.Amount)
		}
		if len(repo.orders) != 1 {
			t.Fatalf("expected 1 order, got %d", len(repo.orders))
		}
		if redirect == "" {
			t.Fatalf("expected redirect URL")
		}
	})
}
