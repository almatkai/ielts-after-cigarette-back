package attempts

import (
	"context"
	"errors"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestAssessmentRetryReusesSavedSubmissionAndResetsFailedTranscription(t *testing.T) {
	for _, skill := range []string{MaterialWriting, MaterialSpeaking} {
		t.Run(skill, func(t *testing.T) {
			pool := testdb.Open(t)
			repo := NewPostgresRepository(pool)
			ctx := context.Background()
			user := testdb.User(t, pool)
			att, err := repo.Create(ctx, Attempt{ID: uuid.New(), UserID: user, MaterialType: skill, MaterialID: uuid.New(), MaterialVersionID: uuid.New()})
			if err != nil {
				t.Fatal(err)
			}
			input := []AnswerInput{{QuestionID: uuid.New(), Answer: map[string]any{"value": "immutable submission"}}}
			table := "writing_assessment_jobs"
			if skill == MaterialWriting {
				err = repo.QueueWritingAssessment(ctx, att.ID, input)
			} else {
				table = "speaking_assessment_jobs"
				err = repo.QueueSpeakingAssessment(ctx, att.ID, input)
			}
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(repo, nil)
			if _, err := service.RetryAssessment(ctx, user, att.ID); !errors.Is(err, ErrAssessmentRetryNotAllowed) {
				t.Fatalf("queued job retried: %v", err)
			}
			if _, err := pool.Exec(ctx, `UPDATE `+table+` SET status='FAILED',attempts=3 WHERE attempt_id=$1`, att.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE attempts SET status='IN_PROGRESS' WHERE id=$1`, att.ID); err != nil {
				t.Fatal(err)
			}
			var recording SpeakingRecording
			if skill == MaterialSpeaking {
				recording, _, err = repo.UpsertSpeakingRecording(ctx, SpeakingRecording{ID: uuid.New(), AttemptID: att.ID, PartID: uuid.New(), OriginalName: "test.webm", MimeType: "audio/webm", StorageKey: "test.webm", ByteSize: 10})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `UPDATE speaking_transcriptions SET status='FAILED',attempts=3 WHERE recording_id=$1`, recording.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.RetryAssessment(ctx, uuid.New(), att.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign retry: %v", err)
			}
			retried, err := service.RetryAssessment(ctx, user, att.ID)
			if err != nil || retried.Status != StatusProcessing {
				t.Fatalf("retry: %+v %v", retried, err)
			}
			if _, err := service.RetryAssessment(ctx, user, att.ID); !errors.Is(err, ErrAssessmentRetryNotAllowed) {
				t.Fatalf("duplicate retry: %v", err)
			}
			answers, err := repo.ListAnswers(ctx, att.ID)
			if err != nil || len(answers) != 1 || answers[0].Answer["value"] != "immutable submission" {
				t.Fatalf("retry changed submission: %+v %v", answers, err)
			}
			if skill == MaterialSpeaking {
				transcript, err := repo.GetSpeakingTranscription(ctx, recording.ID)
				if err != nil || transcript.Status != "QUEUED" || transcript.Attempts != 0 {
					t.Fatalf("failed STT not recovered: %+v %v", transcript, err)
				}
			}
		})
	}
}
