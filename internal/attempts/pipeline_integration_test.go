package attempts

import (
	"context"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestRecoverSpeakingJobsFromLostWorker(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	att, err := repo.Create(ctx, Attempt{ID: uuid.New(), UserID: user, MaterialType: MaterialSpeaking, MaterialID: uuid.New(), MaterialVersionID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	recording, _, err := repo.UpsertSpeakingRecording(ctx, SpeakingRecording{ID: uuid.New(), AttemptID: att.ID, PartID: uuid.New(), OriginalName: "test.webm", MimeType: "audio/webm", StorageKey: "test.webm", ByteSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := repo.ClaimTranscription(ctx, recording.ID); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := repo.QueueSpeakingAssessment(ctx, att.ID, nil); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.ClaimAssessment(ctx, att.ID); err != nil || !claimed {
		t.Fatalf("claim assessment: %v %v", claimed, err)
	}
	if err := repo.RecoverSpeakingJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if claimed, _ := repo.ClaimAssessment(ctx, att.ID); claimed {
		t.Fatal("stole active lease")
	}
	// Simulate a worker disappearing without recording failure.
	for _, table := range []string{"speaking_transcriptions", "speaking_assessment_jobs"} {
		if _, err := pool.Exec(ctx, `UPDATE `+table+` SET updated_at=CURRENT_TIMESTAMP-INTERVAL '3 minutes'`); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.RecoverSpeakingJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := repo.ClaimTranscription(ctx, recording.ID); err != nil || !claimed {
		t.Fatalf("transcription not recovered: %v %v", claimed, err)
	}
	if claimed, err := repo.ClaimAssessment(ctx, att.ID); err != nil || !claimed {
		t.Fatalf("assessment not recovered: %v %v", claimed, err)
	}
}
