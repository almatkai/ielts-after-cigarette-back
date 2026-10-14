package fullmock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestPausePreservesTimeAndDraftUntilExplicitReopen(t *testing.T) {
	for _, position := range []int{1, 2, 3, 4} {
		t.Run(mockSkills[position-1], func(t *testing.T) {
			ctx := context.Background()
			pool := testdb.Open(t)
			user := mockUser(t, pool, "academic")
			seedMockBank(t, pool, user)
			repo := NewPostgresRepository(pool)
			question := uuid.New()
			providers := map[string]attempts.MaterialProvider{}
			for _, skill := range mockSkills {
				if skill == attempts.MaterialWriting || skill == attempts.MaterialSpeaking {
					providers[skill] = incompleteAIProvider{expiryProvider{uuid.New()}}
				} else {
					providers[skill] = expiryProvider{question}
				}
			}
			attemptService := attempts.NewService(attempts.NewPostgresRepository(pool), providers)
			svc := NewService(repo, attemptService)
			attemptService.SetExamGuard(svc)
			session, _, err := svc.StartGenerated(ctx, user, false)
			if err != nil {
				t.Fatal(err)
			}
			for next := 2; next <= position; next++ {
				if err := repo.Advance(ctx, session.ID, next, false); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := svc.GetSection(ctx, user, session.ID, position); err != nil {
				t.Fatal(err)
			}
			att := session.Sections[position-1].Attempt.ID
			execSeed(t, pool, `INSERT INTO attempt_answers(attempt_id,question_id,answer) VALUES ($1,$2,'{"value":"draft"}')`, att, question)
			execSeed(t, pool, `UPDATE full_mock_session_sections SET deadline_at=CURRENT_TIMESTAMP+INTERVAL '8 minutes' WHERE session_id=$1 AND position=$2`, session.ID, position)
			if _, err := svc.Pause(ctx, uuid.New(), session.ID); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("foreign pause: %v", err)
			}
			paused, err := svc.Pause(ctx, user, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			sec := paused.Sections[position-1]
			if sec.DeadlineAt != nil || sec.RemainingMilliseconds == nil || *sec.RemainingMilliseconds > 480000 || *sec.RemainingMilliseconds < 479000 {
				t.Fatalf("pause: %+v", sec)
			}
			remaining := *sec.RemainingMilliseconds
			// Delay wall-clock time, then poll overview/repeat pause. Neither resumes.
			time.Sleep(100 * time.Millisecond)
			again, err := svc.Pause(ctx, user, session.ID)
			if err != nil || *again.Sections[position-1].RemainingMilliseconds != remaining {
				t.Fatalf("duplicate pause: %v", err)
			}
			saved, err := svc.GetSession(ctx, user, session.ID)
			if err != nil || saved.Sections[position-1].DeadlineAt != nil || saved.Sections[position-1].Attempt.Status != attempts.StatusInProgress {
				t.Fatalf("overview: %v", err)
			}
			if err := svc.ValidateAttemptAccess(ctx, user, att); !errors.Is(err, attempts.ErrSectionLocked) {
				t.Fatalf("paused attempt editable: %v", err)
			}
			if _, err := svc.Advance(ctx, user, session.ID); !errors.Is(err, ErrSectionIncomplete) {
				t.Fatalf("paused advance: %v", err)
			}
			if err := repo.PauseSection(ctx, session.ID, position%4+1); !errors.Is(err, ErrSectionLocked) {
				t.Fatalf("future/previous pause: %v", err)
			}
			before := time.Now()
			opened, _, err := svc.GetSection(ctx, user, session.ID, position)
			if err != nil {
				t.Fatal(err)
			}
			if opened.RemainingMilliseconds != nil || opened.DeadlineAt == nil || !opened.StartedAt.Equal(*sec.StartedAt) {
				t.Fatal("resume changed original start or failed to run")
			}
			duration := opened.DeadlineAt.Sub(before)
			if duration < time.Duration(remaining)*time.Millisecond-100*time.Millisecond || duration > time.Duration(remaining)*time.Millisecond+100*time.Millisecond {
				t.Fatalf("resume lost or reset time: %v", duration)
			}
			duplicate, _, err := svc.GetSection(ctx, user, session.ID, position)
			if err != nil || !duplicate.DeadlineAt.Equal(*opened.DeadlineAt) {
				t.Fatalf("reopening reset: %v", err)
			}
			var answers int
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM attempt_answers WHERE attempt_id=$1`, att).Scan(&answers); err != nil || answers != 1 {
				t.Fatalf("draft lost: %v", err)
			}
			execSeed(t, pool, `UPDATE full_mock_session_sections SET started_at=CURRENT_TIMESTAMP-INTERVAL '10 minutes', deadline_at=CURRENT_TIMESTAMP-INTERVAL '1 second' WHERE session_id=$1 AND position=$2`, session.ID, position)
			if _, err := svc.Pause(ctx, user, session.ID); !errors.Is(err, ErrSectionLocked) {
				t.Fatalf("expired pause: %v", err)
			}
			expired, err := svc.GetSession(ctx, user, session.ID)
			if err != nil || expired.Sections[position-1].Attempt.Status == attempts.StatusInProgress {
				t.Fatalf("expiry skipped: %v", err)
			}
		})
	}
}
