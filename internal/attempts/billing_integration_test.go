package attempts

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/billing"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestBillingPracticeStartResumeConcurrencyAndFailures(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := testdb.User(t, pool)
	bills := billing.NewService(billing.NewPostgresRepository(pool), true, nil, "", nil, nil)
	repo := NewPostgresRepository(pool).WithBilling(bills)
	providers := map[string]MaterialProvider{}
	for _, skill := range billing.Skills {
		providers[skill] = stubProvider{publishedVersionID: uuid.New(), public: map[string]any{"title": "Practice"}}
	}
	svc := NewService(repo, providers)
	for _, skill := range billing.Skills {
		t.Run(skill, func(t *testing.T) {
			material := uuid.New()
			var wg sync.WaitGroup
			var created atomic.Int32
			ids := make(chan uuid.UUID, 10)
			for range 10 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					a, _, isNew, err := svc.Start(ctx, user, skill, material)
					if err != nil {
						t.Errorf("start: %v", err)
						return
					}
					ids <- a.ID
					if isNew {
						created.Add(1)
					}
				}()
			}
			wg.Wait()
			close(ids)
			var id uuid.UUID
			for got := range ids {
				if id == uuid.Nil {
					id = got
				}
				if got != id {
					t.Fatal("racing resume created a second attempt")
				}
			}
			if created.Load() != 1 {
				t.Fatalf("created=%d", created.Load())
			}
			// Two more distinct tests consume the rest of the lifetime allowance.
			for range 2 {
				if _, _, _, err := svc.Start(ctx, user, skill, uuid.New()); err != nil {
					t.Fatal(err)
				}
			}
			var required *billing.PaymentRequired
			if _, _, _, err := svc.Start(ctx, user, skill, uuid.New()); !errors.As(err, &required) || required.Skill != skill {
				t.Fatalf("quota: %v", err)
			}
			if a, _, isNew, err := svc.Start(ctx, user, skill, material); err != nil || isNew || a.ID != id {
				t.Fatalf("exhausted resume: %+v %v %v", a, isNew, err)
			}
		})
	}
	a, err := bills.Access(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range billing.Skills {
		if a.Free[skill].Used != 3 {
			t.Fatalf("skill usage: %+v", a)
		}
	}
	// Force the attempt INSERT to fail after billing writes usage.
	other := testdb.User(t, pool)
	// A duplicate ID forces a constraint failure after quota admission.
	var existing uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM attempts WHERE user_id=$1 AND material_type='reading' LIMIT 1`, user).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.CreatePractice(ctx, Attempt{ID: existing, UserID: other, MaterialType: "writing", MaterialID: uuid.New(), MaterialVersionID: uuid.New()})
	if err == nil {
		t.Fatal("duplicate attempt ID succeeded")
	}
	a, err = bills.Access(ctx, other)
	if err != nil || a.Free["writing"].Used != 0 {
		t.Fatalf("failed creation charged quota: %+v %v", a, err)
	}
}
