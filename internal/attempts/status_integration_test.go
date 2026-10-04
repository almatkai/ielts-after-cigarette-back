package attempts

import (
	"context"
	"errors"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestStatusSnapshotLifecycle(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	for _, skill := range []string{MaterialWriting, MaterialSpeaking, MaterialReading, MaterialListening} {
		t.Run(skill, func(t *testing.T) {
			attempt, err := repo.Create(ctx, Attempt{ID: uuid.New(), UserID: user, MaterialType: skill, MaterialID: uuid.New(), MaterialVersionID: uuid.New()})
			if err != nil {
				t.Fatal(err)
			}
			status, err := repo.GetStatus(ctx, user, attempt.ID)
			if err != nil || status.ID != attempt.ID || status.Status != StatusInProgress || status.WritingAssessment != nil || status.SpeakingAssessment != nil {
				t.Fatalf("new attempt status: %+v / %v", status, err)
			}
			if _, err := repo.GetStatus(ctx, uuid.New(), attempt.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign attempt error: %v", err)
			}
			if skill == MaterialWriting {
				err = repo.QueueWritingAssessment(ctx, attempt.ID, nil)
			} else if skill == MaterialSpeaking {
				err = repo.QueueSpeakingAssessment(ctx, attempt.ID, nil)
			} else {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range []string{"QUEUED", "PROCESSING", "FAILED", "READY"} {
				attemptState := StatusProcessing
				if state == "FAILED" {
					attemptState = StatusInProgress
				} else if state == "READY" {
					attemptState = StatusSubmitted
				}
				if _, err := pool.Exec(ctx, "UPDATE "+skill+"_assessment_jobs SET status=$1, attempts=2, error_message='failure' WHERE attempt_id=$2", state, attempt.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, "UPDATE attempts SET status=$1, score=0, max_score=1, submitted_at=CURRENT_TIMESTAMP WHERE id=$2", attemptState, attempt.ID); err != nil {
					t.Fatal(err)
				}
				status, err = repo.GetStatus(ctx, user, attempt.ID)
				if err != nil || status.Status != attemptState {
					t.Fatalf("snapshot: %+v / %v", status, err)
				}
				if state == "READY" {
					if status.WritingAssessment != nil || status.SpeakingAssessment != nil {
						t.Fatal("terminal attempt should omit jobs like the detail endpoint")
					}
					continue
				}
				if skill == MaterialWriting {
					if status.WritingAssessment == nil || status.WritingAssessment.Status != state || status.WritingAssessment.Attempts != 2 || status.WritingAssessment.ErrorMessage != "failure" || status.SpeakingAssessment != nil {
						t.Fatalf("writing job: %+v", status)
					}
				} else if status.SpeakingAssessment == nil || status.SpeakingAssessment.Status != state || status.SpeakingAssessment.Attempts != 2 || status.SpeakingAssessment.ErrorMessage != "failure" || status.WritingAssessment != nil {
					t.Fatalf("speaking job: %+v", status)
				}
			}
		})
	}
	if _, err := repo.GetStatus(ctx, user, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing attempt error: %v", err)
	}
}
