package billing

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestBCCSuffixLeaseSurvivesWraparoundAndOrderDeletion(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	newOrder := func() Order {
		t.Helper()
		o, err := repo.CreateOrder(ctx, Order{ID: uuid.New(), UserID: user, PlanCode: "self", Amount: 4990, ListPrice: 9990, Provider: "bcc", AccessDays: 30})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	first := newOrder()
	bankID, err := repo.ReserveBCCOrder(ctx, first.ID)
	if err != nil || bankID != "1000000000000001" {
		t.Fatalf("first allocation: %s %v", bankID, err)
	}
	if again, err := repo.ReserveBCCOrder(ctx, first.ID); err != nil || again != bankID {
		t.Fatalf("allocation is not stable: %s %v", again, err)
	}
	var until time.Time
	if err := pool.QueryRow(ctx, `SELECT reserved_until FROM billing_bcc_order_suffixes WHERE suffix=1`).Scan(&until); err != nil || time.Until(until) < 48*time.Hour {
		t.Fatalf("insufficient suffix lease: %s %v", until, err)
	}
	// Even deleting the local order must not release a bank deduplication lease.
	if _, err := pool.Exec(ctx, `DELETE FROM billing_orders WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT setval('billing_bcc_order_sequence',1000000)`); err != nil {
		t.Fatal(err)
	}
	second := newOrder()
	secondID, err := repo.ReserveBCCOrder(ctx, second.ID)
	if err != nil || secondID != "1000000001000002" {
		t.Fatalf("active suffix was reused after wraparound: %s %v", secondID, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing_bcc_order_suffixes SET reserved_until=clock_timestamp()-INTERVAL '1 second' WHERE suffix=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT setval('billing_bcc_order_sequence',2000000)`); err != nil {
		t.Fatal(err)
	}
	third := newOrder()
	thirdID, err := repo.ReserveBCCOrder(ctx, third.ID)
	if err != nil || thirdID != "1000000002000001" {
		t.Fatalf("expired suffix was not reusable: %s %v", thirdID, err)
	}
	// All slots encountered within the bounded attempt budget are unavailable:
	// fail without publishing an unreserved provider mapping.
	if _, err := pool.Exec(ctx, `INSERT INTO billing_bcc_order_suffixes(suffix,reserved_until)
	 SELECT generate_series(3,66),clock_timestamp()+INTERVAL '1 day'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT setval('billing_bcc_order_sequence',3000002)`); err != nil {
		t.Fatal(err)
	}
	fourth := newOrder()
	if id, err := repo.ReserveBCCOrder(ctx, fourth.ID); err != ErrProviderUnavailable || id != "" {
		t.Fatalf("unreserved mapping exposed: %s %v", id, err)
	}
	stored, err := repo.Order(ctx, fourth.ID)
	if err != nil || stored.ProviderOrderID != nil {
		t.Fatal("failed reservation persisted a bank ORDER")
	}
}

func TestBCCConcurrentCheckoutsReserveDifferentSuffixes(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	p, err := NewBCCProvider(testBCCConfig(), repo, "https://app.example/billing/return", "test")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, true, p, p.returnURL, nil, nil)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, redirect, err := s.Checkout(ctx, user, "self", true); err != nil || redirect == "" {
				t.Errorf("checkout: redirect=%q err=%v", redirect, err)
			}
		}()
	}
	wg.Wait()
	var total, distinct int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*),COUNT(DISTINCT RIGHT(provider_order_id,6)) FROM billing_orders WHERE provider='bcc'`).Scan(&total, &distinct); err != nil || total != 12 || distinct != 12 {
		t.Fatalf("total=%d distinct suffixes=%d err=%v", total, distinct, err)
	}
}

func TestBCCDatabaseMappingAndConcurrentHTTPNotifications(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	p, err := NewBCCProvider(testBCCConfig(), repo, "https://app.example/billing/return", "test")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, true, p, p.returnURL, nil, nil)
	router := bccTestRouter(s, user)
	// Checkout through the unchanged JSON contract and real database.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/billing/checkout", strings.NewReader(`{"planCode":"self","acceptedTerms":true}`)))
	var response struct {
		OrderID  string `json:"orderId"`
		Redirect string `json:"redirectUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 201 {
		t.Fatalf("checkout: %d %s %v", w.Code, w.Body, err)
	}
	orders, err := repo.Orders(ctx, &user, "", 10, 0)
	if err != nil || len(orders) != 1 {
		t.Fatalf("orders: %v %v", orders, err)
	}
	o := orders[0]
	if o.ID.String() != response.OrderID || o.ProviderOrderID == nil || !bccBankOrder.MatchString(*o.ProviderOrderID) {
		t.Fatal("no persisted numeric mapping")
	}
	mapped, err := repo.OrderByProvider(ctx, "bcc", *o.ProviderOrderID)
	if err != nil || mapped.ID != o.ID {
		t.Fatal("numeric ORDER mapping failed")
	}
	other, err := repo.CreateOrder(ctx, Order{ID: uuid.New(), UserID: user, PlanCode: "self", Amount: 4990, ListPrice: 9990, Provider: "bcc", AccessDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetProviderOrder(ctx, other.ID, *o.ProviderOrderID); err == nil {
		t.Fatal("duplicate bank ORDER accepted")
	}
	if err := repo.SetProviderOrder(ctx, other.ID, other.ID.String()); err == nil {
		t.Fatal("UUID accepted as bank ORDER")
	}
	if _, err := repo.OrderByProvider(ctx, "fake", *o.ProviderOrderID); err == nil {
		t.Fatal("cross-provider mapping exposed")
	}
	// A forged browser return cannot grant, even with success parameters.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/billing/bcc/return/"+o.ID.String(), strings.NewReader("ACTION=0&RC=00")))
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_entitlements WHERE order_id=$1`, o.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("browser granted entitlement")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			router.ServeHTTP(w, callbackRequest(callbackValues(o)))
			if w.Code != 200 {
				t.Errorf("callback: %d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_entitlements WHERE order_id=$1`, o.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("entitlements=%d err=%v", count, err)
	}
	updated, err := repo.Order(ctx, o.ID)
	if err != nil || updated.Status != "PAID" || updated.ProviderOrderID == nil || *updated.ProviderOrderID != *o.ProviderOrderID {
		t.Fatalf("settled: %+v %v", updated, err)
	}
	a, err := repo.Snapshot(ctx, user)
	if err != nil || !a.Active {
		t.Fatalf("premium not active: %+v %v", a, err)
	}
	v := callbackValues(o)
	v.Set("TRTYPE", "14")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, callbackRequest(v))
	if w.Code != 400 {
		t.Fatal("refund operation accepted")
	}
	updated, _ = repo.Order(ctx, o.ID)
	if updated.Status != "PAID" {
		t.Fatal("unsupported refund mutated payment")
	}
}
