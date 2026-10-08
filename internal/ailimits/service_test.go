package ailimits

import (
	"context"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
)

type memoryRepo struct {
	limits Limits
	found  bool
}

func (m *memoryRepo) Get(ctx context.Context) (Limits, bool, error) {
	return m.limits, m.found, nil
}

func (m *memoryRepo) Upsert(ctx context.Context, limits Limits) (Limits, error) {
	limits.UpdatedAt = time.Now().UTC()
	m.limits = limits
	m.found = true
	return limits, nil
}

func TestLimitsServiceDefaultAndDynamicUpdate(t *testing.T) {
	defaults := Limits{
		AssistantLimit:      100,
		GuestAssistantLimit: 15,
		WritingLimit:        25,
		SpeakingLimit:       25,
	}

	repo := &memoryRepo{}
	limiter := cache.NewDailyLimiter(nil) // nil client fails open
	svc := NewService(repo, limiter, nil, defaults)

	ctx := context.Background()

	// 1. Initial Get returns defaults
	initial, err := svc.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.AssistantLimit != 100 || initial.WritingLimit != 25 {
		t.Fatalf("unexpected initial limits: %+v", initial)
	}

	// 2. Dynamic update by admin to lower limits during a peak
	newWriting := int64(5)
	newAssistant := int64(20)
	updated, err := svc.Update(ctx, UpdateLimitsInput{
		WritingLimit:   &newWriting,
		AssistantLimit: &newAssistant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritingLimit != 5 || updated.AssistantLimit != 20 {
		t.Fatalf("unexpected updated limits: %+v", updated)
	}

	// 3. Subsequent Get immediately reflects new values
	current, err := svc.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.WritingLimit != 5 || current.AssistantLimit != 20 {
		t.Fatalf("unexpected current limits: %+v", current)
	}

	// 4. Negative value should fail validation
	neg := int64(-1)
	_, err = svc.Update(ctx, UpdateLimitsInput{
		WritingLimit: &neg,
	})
	if err == nil {
		t.Fatal("expected error on negative limit")
	}
}
