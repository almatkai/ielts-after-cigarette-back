package attempts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestAtomicSavePreservesLastAnswerAndSubmitLock(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	attempt, err := repo.Create(ctx, Attempt{ID: uuid.New(), UserID: testdb.User(t, pool), MaterialType: MaterialReading, MaterialID: uuid.New(), MaterialVersionID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	question := uuid.New()
	answers := []AnswerInput{{QuestionID: question, Answer: map[string]any{"value": "old"}}, {QuestionID: question, Answer: map[string]any{"value": "final"}}}
	if err := repo.SaveAnswers(ctx, attempt.ID, answers); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAnswers(ctx, attempt.ID, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.ListAnswers(ctx, attempt.ID)
	if err != nil || len(saved) != 1 || saved[0].Answer["value"] != "final" {
		t.Fatalf("saved: %+v / %v", saved, err)
	}
	// Simulate Submit owning the row while a delayed autosave arrives.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM attempts WHERE id=$1 FOR UPDATE`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- repo.SaveAnswers(ctx, attempt.ID, []AnswerInput{{QuestionID: question, Answer: map[string]any{"value": "late"}}})
	}()
	if _, err := tx.Exec(ctx, `UPDATE attempts SET status='SUBMITTED',score=1,max_score=1,submitted_at=CURRENT_TIMESTAMP WHERE id=$1`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrAlreadySubmitted) {
			t.Fatalf("late autosave error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late autosave deadlocked")
	}
	saved, err = repo.ListAnswers(ctx, attempt.ID)
	if err != nil || saved[0].Answer["value"] != "final" {
		t.Fatalf("submitted answer overwritten: %+v / %v", saved, err)
	}
	if err := repo.SaveAnswers(ctx, uuid.New(), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing attempt: %v", err)
	}
}

func TestHistoryPagesPreserveAllAttemptsAndOwnership(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user, other := testdb.User(t, pool), testdb.User(t, pool)
	for i := 0; i < 53; i++ {
		owner := user
		if i == 52 {
			owner = other
		}
		skill := MaterialReading
		if i%2 == 1 {
			skill = MaterialListening
		}
		attempt, err := repo.Create(ctx, Attempt{ID: uuid.New(), UserID: owner, MaterialType: skill, MaterialID: uuid.New(), MaterialVersionID: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		// Identical timestamps exercise the UUID tie-breaker.
		if _, err := pool.Exec(ctx, `UPDATE attempts SET started_at='2026-10-01T10:00:00Z' WHERE id=$1`, attempt.ID); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uuid.UUID]bool{}
	cursor := HistoryCursor{}
	for page := 0; page < 4; page++ {
		result, err := repo.ListHistory(ctx, user, "", 20, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Items) > 20 {
			t.Fatal("unbounded page")
		}
		for _, item := range result.Items {
			if item.UserID != user || seen[item.ID] {
				t.Fatalf("foreign or duplicate attempt: %s", item.ID)
			}
			seen[item.ID] = true
		}
		if result.NextCursor == "" {
			break
		}
		cursor, err = ParseHistoryCursor(result.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 52 {
		t.Fatalf("history lost attempts: %d", len(seen))
	}
	legacy, err := repo.ListByUser(ctx, user, "")
	if err != nil || len(legacy) != 52 {
		t.Fatalf("legacy history changed: %d / %v", len(legacy), err)
	}
	reading, err := repo.ListHistory(ctx, user, MaterialReading, 100, HistoryCursor{})
	if err != nil || len(reading.Items) != 26 || reading.NextCursor != "" {
		t.Fatalf("skill filter: %d / %v", len(reading.Items), err)
	}
	for _, raw := range []string{"!invalid", "e30", "bnVsbA"} {
		if _, err := ParseHistoryCursor(raw); err == nil {
			t.Fatalf("accepted invalid cursor %q", raw)
		}
	}
}
