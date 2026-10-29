package billing

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func execSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func consume(t *testing.T, s *Service, pool *pgxpool.Pool, user, id uuid.UUID, kind string, skills []string, commit bool) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = s.ConsumeTx(ctx, tx, user, kind, id, skills); err != nil {
		return err
	}
	if commit {
		return tx.Commit(ctx)
	}
	return nil
}
func TestConcurrentQuotaAtomicityAndRollback(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	s := NewService(repo, true, nil, "", nil, nil)
	user := testdb.User(t, pool)
	// A failed creation must not leave a reservation behind.
	if err := consume(t, s, pool, user, uuid.New(), "ATTEMPT", []string{"reading"}, false); err != nil {
		t.Fatal(err)
	}
	a, err := s.Access(ctx, user)
	if err != nil || a.Free["reading"].Used != 0 {
		t.Fatalf("rollback: %+v %v", a, err)
	}
	var allowed, blocked atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := consume(t, s, pool, user, uuid.New(), "ATTEMPT", []string{"reading"}, true)
			var required *PaymentRequired
			if err == nil {
				allowed.Add(1)
			} else if errors.As(err, &required) {
				blocked.Add(1)
			} else {
				t.Errorf("consume: %v", err)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 3 || blocked.Load() != 9 {
		t.Fatalf("allowed=%d blocked=%d", allowed.Load(), blocked.Load())
	}
	a, err = s.Access(ctx, user)
	if err != nil || a.Free["reading"].Used != 3 || a.Free["reading"].Remaining != 0 || a.Free["listening"].Remaining != 3 {
		t.Fatalf("usage: %+v %v", a, err)
	}
	// No other skill can be partially charged when a mock fails admission.
	err = consume(t, s, pool, user, uuid.New(), "FULL_MOCK_SESSION", Skills, true)
	var required *PaymentRequired
	if !errors.As(err, &required) || required.Feature != "full_mock" || required.Skill != "reading" {
		t.Fatalf("mock admission: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM free_usage WHERE user_id=$1`, user).Scan(&count); err != nil || count != 3 {
		t.Fatalf("partial charge %d: %v", count, err)
	}
	other := testdb.User(t, pool)
	id := uuid.New()
	for range 5 {
		if err = consume(t, s, pool, other, id, "FULL_MOCK_SESSION", Skills, true); err != nil {
			t.Fatal(err)
		}
	}
	a, err = s.Access(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range Skills {
		if a.Free[skill].Used != 1 || a.Free[skill].Remaining != 2 {
			t.Fatalf("idempotent full mock: %+v", a)
		}
	}
}
func TestPaymentIdempotencyQueueRefundAndAdministration(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	s := NewService(repo, true, nil, "https://example.test/billing/return", nil, nil)
	user, admin := testdb.User(t, pool), testdb.User(t, pool)
	o, err := repo.CreateOrder(ctx, Order{ID: uuid.New(), UserID: user, PlanCode: "group", ListPrice: 24990, Amount: 14990, Provider: "test", AccessDays: 30, MeetingsCount: 4})
	if err != nil {
		t.Fatal(err)
	}
	// Benefits must stay those quoted at checkout even if the plan changes.
	execSQL(t, pool, `UPDATE billing_plans SET access_days=7,meetings_count=1 WHERE code='group'`)
	n := Notification{OrderID: o.ID, ProviderOrderID: "bank-1", Amount: o.Amount, Currency: "KZT", Status: "PAID"}
	invalid := n
	invalid.Amount++
	if err = s.Notify(ctx, "test", invalid); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("amount mismatch: %v", err)
	}
	invalid = n
	invalid.Currency = "USD"
	if err = s.Notify(ctx, "test", invalid); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("currency mismatch: %v", err)
	}
	if err = s.Notify(ctx, "wrong", n); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("provider mismatch: %v", err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Notify(ctx, "test", n); err != nil {
				t.Errorf("callback: %v", err)
			}
		}()
	}
	wg.Wait()
	e, err := repo.Entitlements(ctx, user)
	if err != nil || len(e) != 1 || e[0].OrderID == nil || *e[0].OrderID != o.ID || e[0].Source != "PAYMENT" || e[0].MeetingsTotal != 4 {
		t.Fatalf("payment grant: %+v %v", e, err)
	}
	order, err := repo.Order(ctx, o.ID)
	if err != nil || order.PaidAt == nil || order.Status != "PAID" || order.Amount != 14990 || order.ListPrice != 24990 {
		t.Fatalf("order: %+v %v", order, err)
	}
	a, err := s.Access(ctx, user)
	if err != nil || !a.Active || a.Plan.Code != "group" || a.MeetingsTotal != 4 {
		t.Fatalf("access: %+v %v", a, err)
	}
	first := e[0]
	if !first.EndsAt.Equal(first.StartsAt.AddDate(0, 0, 30)) {
		t.Fatal("plan edit rewrote the purchased access duration")
	}
	// Concurrent admin grants are serialized with payment grants on the account.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Grant(ctx, user, admin, GrantInput{PlanCode: "self", Source: "BONUS", Days: ptr(2)}); err != nil {
				t.Errorf("grant: %v", err)
			}
		}()
	}
	wg.Wait()
	e, err = repo.Entitlements(ctx, user)
	if err != nil || len(e) != 5 {
		t.Fatalf("queue: %+v %v", e, err)
	}
	for i := 1; i < len(e); i++ {
		if !e[i].StartsAt.Equal(e[i-1].EndsAt) || !e[i].EndsAt.Equal(e[i].StartsAt.AddDate(0, 0, 2)) {
			t.Fatalf("queue overlap or gap: %+v", e)
		}
	}
	a, err = s.Access(ctx, user)
	if err != nil || a.QueuedUntil == nil || !a.QueuedUntil.Equal(e[4].EndsAt) || !a.EndsAt.Equal(first.EndsAt) {
		t.Fatalf("queuedUntil: %+v %v", a, err)
	}
	if err = repo.Meetings(ctx, first.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err = repo.Meetings(ctx, first.ID, 5); !errors.Is(err, ErrValidation) {
		t.Fatalf("meetings overflow: %v", err)
	}
	a, err = s.Access(ctx, user)
	if err != nil || a.MeetingsUsed != 2 {
		t.Fatalf("meetings: %+v %v", a, err)
	}
	n.Status = "REFUNDED"
	if err = s.Notify(ctx, "test", n); err != nil {
		t.Fatal(err)
	}
	if err = s.Notify(ctx, "test", n); err != nil {
		t.Fatal(err)
	}
	a, err = s.Access(ctx, user)
	if err != nil || a.Active {
		t.Fatalf("refund access: %+v %v", a, err)
	}
	n.Status = "PAID"
	if err = s.Notify(ctx, "test", n); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("refunded replay: %v", err)
	}
	if err = repo.Revoke(ctx, e[1].ID, "Cancelled bonus"); err != nil {
		t.Fatal(err)
	}
	// Current grant is gone, but future non-revoked grants stay queued.
	a, err = s.Access(ctx, user)
	if err != nil || a.Active || a.QueuedUntil == nil {
		t.Fatalf("revoked queue: %+v %v", a, err)
	}
	execSQL(t, pool, `DELETE FROM users WHERE id=$1`, user)
	for _, table := range []string{"billing_orders", "user_entitlements", "free_usage"} {
		var count int
		if err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+table+` WHERE user_id=$1`, user).Scan(&count); err != nil || count != 0 {
			t.Fatalf("erasure %s: %d %v", table, count, err)
		}
	}
}
func TestQuotaBypassesAndDisabledDoesNotRecord(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	s := NewService(repo, true, nil, "", nil, nil)
	admin := testdb.User(t, pool)
	for _, role := range []string{"ADMIN", "EDITOR", "GUEST", "STUDENT"} {
		t.Run(role, func(t *testing.T) {
			user := testdb.User(t, pool)
			execSQL(t, pool, `UPDATE users SET role=$2::text,status=CASE WHEN $2::text='GUEST' THEN 'GUEST' ELSE status END WHERE id=$1`, user, role)
			if role == "STUDENT" {
				if _, err := s.Grant(ctx, user, admin, GrantInput{PlanCode: "self", Source: "ADMIN"}); err != nil {
					t.Fatal(err)
				}
			}
			for range 5 {
				if err := consume(t, s, pool, user, uuid.New(), "FULL_MOCK_SESSION", Skills, true); err != nil {
					t.Fatal(err)
				}
			}
			a, err := s.Access(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			for _, skill := range Skills {
				if a.Free[skill].Used != 0 {
					t.Fatalf("bypass charged: %+v", a)
				}
			}
		})
	}
	user := testdb.User(t, pool)
	disabled := NewService(repo, false, nil, "", nil, nil)
	for range 5 {
		if err := consume(t, disabled, pool, user, uuid.New(), "FULL_MOCK_SESSION", Skills, true); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.Access(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range Skills {
		if a.Free[skill].Remaining != 3 {
			t.Fatal("disabled flag consumed trial")
		}
	}
	// Future, expired and revoked entitlements do not bypass quotas.
	for _, window := range []struct {
		start, end time.Duration
		revoked    bool
	}{{time.Hour, 48 * time.Hour, false}, {-48 * time.Hour, -time.Hour, false}, {-time.Hour, time.Hour, true}} {
		execSQL(t, pool, `DELETE FROM user_entitlements WHERE user_id=$1`, user)
		execSQL(t, pool, `INSERT INTO user_entitlements(id,user_id,plan_code,source,starts_at,ends_at,revoked_at) VALUES ($1,$2,'self','ADMIN',$3,$4,$5)`, uuid.New(), user, time.Now().Add(window.start), time.Now().Add(window.end), func() *time.Time {
			if window.revoked {
				return ptr(time.Now())
			}
			return nil
		}())
		a, err = s.Access(ctx, user)
		if err != nil || a.Active {
			t.Fatalf("invalid active window: %+v %v", a, err)
		}
	}
}
func TestPlansSettingsAndFakeCheckout(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	s := NewService(repo, true, nil, "https://example.test/billing/return", nil, nil)
	user := testdb.User(t, pool)
	plans, err := repo.Plans(ctx)
	if err != nil || len(plans) != 3 || plans[0].Code != "self" || plans[2].Price != 34990 {
		t.Fatalf("seed: %+v %v", plans, err)
	}
	if _, err = s.UpdatePlan(ctx, "self", PlanInput{ListPrice: ptr(int64(10000)), SalePrice: nil, Active: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	p, err := repo.Plan(ctx, "self")
	if err != nil || p.SalePrice != nil || p.Price != 10000 {
		t.Fatalf("remove sale: %+v %v", p, err)
	}
	settings, err := s.UpdateSettings(ctx, Settings{FreeListening: 2, FreeReading: 1, FreeWriting: 4, FreeSpeaking: 6, FreeAssistantDailyLimit: 7})
	if err != nil || !settings.PaywallEnabled {
		t.Fatal(settings, err)
	}
	a, err := s.Access(ctx, user)
	if err != nil || a.Free["speaking"].Limit != 6 || a.Assistant.Limit != 7 {
		t.Fatal(a, err)
	}
	if err = s.WithFakeProvider("test"); err != nil {
		t.Fatal(err)
	}
	o, _, err := s.Checkout(ctx, user, "self", true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.Order(ctx, o.ID)
	if err != nil || got.Status != "PAID" || got.Amount != 10000 {
		t.Fatalf("fake: %+v %v", got, err)
	}
	a, err = s.Access(ctx, user)
	if err != nil || !a.Active {
		t.Fatalf("fake access: %+v %v", a, err)
	}

	if _, _, err = s.Checkout(ctx, user, "self", true); err != nil {
		t.Fatal(err)
	}
	queued, err := repo.Entitlements(ctx, user)
	if err != nil || len(queued) != 2 || !queued[1].StartsAt.Equal(queued[0].EndsAt) {
		t.Fatalf("paid renewal not queued: %+v %v", queued, err)
	}
	// Expiring the last grant causes the next grant to start now, without a gap.
	execSQL(t, pool, `UPDATE user_entitlements SET starts_at=clock_timestamp()-interval '40 days',ends_at=clock_timestamp()-interval '10 days' WHERE user_id=$1`, user)
	before := time.Now()
	e, err := s.Grant(ctx, user, user, GrantInput{PlanCode: "group", Source: "ADMIN"})
	if err != nil || e.StartsAt.Before(before) || e.StartsAt.After(time.Now()) {
		t.Fatalf("expired renewal: %+v %v", e, err)
	}
}

func TestBillingMigrationRollbackAndUserForeignKeys(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := testdb.User(t, pool)
	var cascades int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_constraint WHERE contype='f' AND confrelid='users'::regclass AND confdeltype='c'
 AND conrelid IN ('billing_orders'::regclass,'user_entitlements'::regclass,'free_usage'::regclass)`).Scan(&cascades); err != nil || cascades != 4 {
		t.Fatalf("billing user FK cascades=%d error=%v", cascades, err)
	}
	for _, direction := range []string{"down", "up"} {
		sql, err := os.ReadFile("../../migrations/000047_billing." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		execSQL(t, pool, string(sql))
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, user).Scan(&exists); err != nil || !exists {
		t.Fatal("billing rollback changed an existing account", err)
	}
	plans, err := NewPostgresRepository(pool).Plans(ctx)
	if err != nil || len(plans) != 3 {
		t.Fatalf("billing rollback/reapply failed: %v %v", plans, err)
	}
}
