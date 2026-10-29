package fullmock

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/billing"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestBillingGeneratedMockRestartResumeAndFailedRestart(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := mockUser(t, pool, "academic")
	seedMockBank(t, pool, user)
	bills := billing.NewService(billing.NewPostgresRepository(pool), true, nil, "", nil, nil)
	repo := NewPostgresRepository(pool).WithBilling(bills)
	svc := NewService(repo, attempts.NewService(attempts.NewPostgresRepository(pool).WithBilling(bills), nil))
	var last Session
	for i := 1; i <= 3; i++ {
		session, created, err := svc.StartGenerated(ctx, user, i > 1)
		if err != nil || !created {
			t.Fatalf("start %d: %+v %v %v", i, session, created, err)
		}
		last = session
		a, err := bills.Access(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		for _, skill := range billing.Skills {
			if a.Free[skill].Used != i {
				t.Fatalf("start %d usage: %+v", i, a)
			}
		}
		same, created, err := svc.StartGenerated(ctx, user, false)
		if err != nil || created || same.ID != session.ID {
			t.Fatalf("resume: %+v %v %v", same, created, err)
		}
	}
	var required *billing.PaymentRequired
	if _, _, err := svc.StartGenerated(ctx, user, true); !errors.As(err, &required) || required.Skill != "listening" {
		t.Fatalf("restart quota: %v", err)
	}
	preserved, err := repo.GetSession(ctx, last.ID)
	if err != nil || preserved.Status != SessionInProgress {
		t.Fatalf("failed restart destroyed active mock: %+v %v", preserved, err)
	}
	for _, section := range preserved.Sections {
		if section.Attempt.Status != attempts.StatusInProgress {
			t.Fatalf("failed restart abandoned attempt: %+v", section)
		}
	}
	if err = repo.StartSection(ctx, last.ID, 1, 30); err != nil {
		t.Fatal(err)
	}
	if err = repo.StartSection(ctx, last.ID, 1, 30); err != nil {
		t.Fatal(err)
	}
	a, err := bills.Access(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range billing.Skills {
		if a.Free[skill].Used != 3 {
			t.Fatal("section charged twice")
		}
	}
}
func TestBillingMockRacesPracticeForLastWritingUnit(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := mockUser(t, pool, "academic")
	seedMockBank(t, pool, user)
	bills := billing.NewService(billing.NewPostgresRepository(pool), true, nil, "", nil, nil)
	repo := NewPostgresRepository(pool).WithBilling(bills)
	practice := attempts.NewPostgresRepository(pool).WithBilling(bills)
	for range 2 {
		execSeed(t, pool, `INSERT INTO free_usage(user_id,skill,source_kind,source_id) VALUES ($1,'writing','ATTEMPT',$2)`, user, uuid.New())
	}
	var wg sync.WaitGroup
	var allowed, blocked atomic.Int32
	check := func(err error) {
		var required *billing.PaymentRequired
		if err == nil {
			allowed.Add(1)
		} else if errors.As(err, &required) {
			blocked.Add(1)
		} else {
			t.Errorf("start: %v", err)
		}
	}
	wg.Add(2)
	go func() { defer wg.Done(); _, _, err := repo.GenerateSession(ctx, user, false); check(err) }()
	go func() {
		defer wg.Done()
		_, _, err := practice.CreatePractice(ctx, attempts.Attempt{ID: uuid.New(), UserID: user, MaterialType: "writing", MaterialID: uuid.New(), MaterialVersionID: uuid.New()})
		check(err)
	}()
	wg.Wait()
	if allowed.Load() != 1 || blocked.Load() != 1 {
		t.Fatalf("starts allowed=%d blocked=%d", allowed.Load(), blocked.Load())
	}
	a, err := bills.Access(ctx, user)
	if err != nil || a.Free["writing"].Used != 3 {
		t.Fatalf("mixed usage: %+v %v", a, err)
	}
}

func TestBillingLegacyMockStartAndRestart(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	user := testdb.User(t, pool)
	bills := billing.NewService(billing.NewPostgresRepository(pool), true, nil, "", nil, nil)
	repo := NewPostgresRepository(pool).WithBilling(bills)
	providers := map[string]attempts.MaterialProvider{}
	for _, skill := range billing.Skills {
		providers[skill] = expiryProvider{questionID: uuid.New()}
	}
	svc := NewService(repo, attempts.NewService(attempts.NewPostgresRepository(pool).WithBilling(bills), providers))
	item, err := repo.Create(ctx, Test{ID: uuid.New(), Slug: "legacy-billing", ExamType: "academic", Title: "Legacy", DurationMinutes: 180,
		ListeningMaterialID: uuid.New(), ReadingMaterialID: uuid.New(), WritingMaterialID: uuid.New(), SpeakingMaterialID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Publish(ctx, item.ID, item.Revision); err != nil {
		t.Fatal(err)
	}
	var last Session
	for i := 1; i <= 3; i++ {
		session, created, err := svc.Start(ctx, user, item.ID, i > 1)
		if err != nil || !created {
			t.Fatalf("legacy start: %+v %v %v", session, created, err)
		}
		last = session
		same, created, err := svc.Start(ctx, user, item.ID, false)
		if err != nil || created || same.ID != last.ID {
			t.Fatalf("legacy resume: %+v %v %v", same, created, err)
		}
	}
	var required *billing.PaymentRequired
	if _, _, err = svc.Start(ctx, user, item.ID, true); !errors.As(err, &required) {
		t.Fatalf("legacy exhausted restart: %v", err)
	}
	same, err := repo.GetSession(ctx, last.ID)
	if err != nil || same.Status != SessionInProgress {
		t.Fatalf("legacy failed restart lost session: %+v %v", same, err)
	}
	a, err := bills.Access(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range billing.Skills {
		if a.Free[skill].Used != 3 {
			t.Fatalf("legacy usage: %+v", a)
		}
	}
}
