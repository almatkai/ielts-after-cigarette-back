package guest

import (
	"context"
	"sync"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/billing"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestBillingClaimRecordsOneMockEvenAfterQuotaExhaustion(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	bills := billing.NewService(billing.NewPostgresRepository(pool), true, nil, "", nil, nil)
	repo := NewPostgresRepository(pool).WithBilling(bills)
	user := testdb.User(t, pool)
	_, session, _ := seedClaimTrial(t, pool, "billing-claim", "SUBMITTED", false)
	// Historical claim is always allowed; it cannot lose the guest's result.
	for range 3 {
		if _, err := pool.Exec(ctx, `INSERT INTO free_usage(user_id,skill,source_kind,source_id) VALUES ($1,'writing','ATTEMPT',$2)`, user, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := repo.Claim(ctx, []byte("billing-claim"), user)
			if err != nil || id == nil || *id != session {
				t.Errorf("claim: %v %v", id, err)
			}
		}()
	}
	wg.Wait()
	a, err := bills.Access(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range billing.Skills {
		want := 1
		if skill == "writing" {
			want = 4
		}
		if a.Free[skill].Used != want || a.Free[skill].Remaining != max(0, 3-want) {
			t.Fatalf("claim usage: %+v", a)
		}
	}
}
func TestBillingDisabledClaimDoesNotRecord(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	bills := billing.NewService(billing.NewPostgresRepository(pool), false, nil, "", nil, nil)
	repo := NewPostgresRepository(pool).WithBilling(bills)
	user := testdb.User(t, pool)
	seedClaimTrial(t, pool, "disabled-claim", "SUBMITTED", false)
	if _, err := repo.Claim(ctx, []byte("disabled-claim"), user); err != nil {
		t.Fatal(err)
	}
	a, err := bills.Access(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range billing.Skills {
		if a.Free[skill].Used != 0 {
			t.Fatalf("disabled claim usage: %+v", a)
		}
	}
}
