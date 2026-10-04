package writingpipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

type unavailableQueue struct{}

func (unavailableQueue) EnqueueWritingAssessment(context.Context, uuid.UUID) error {
	return errors.New("Redis down")
}
func (unavailableQueue) PopWritingAssessment(ctx context.Context) (uuid.UUID, error) {
	<-ctx.Done()
	return uuid.Nil, ctx.Err()
}

type recoveredProvider struct{ attempts.MaterialProvider }

func (recoveredProvider) GradingStructure(context.Context, uuid.UUID, uuid.UUID) (attempts.GradingMaterial, error) {
	return attempts.GradingMaterial{ExamType: "academic", WritingTasks: []attempts.WritingTask{{ID: uuid.MustParse("00000000-0000-4000-8000-000000000001")}}}, nil
}

func TestRecoveryProcessesDurableJobsWithoutRedis(t *testing.T) {
	pool := testdb.Open(t)
	repo := attempts.NewPostgresRepository(pool)
	ctx := context.Background()
	user := testdb.User(t, pool)
	att, err := repo.Create(ctx, attempts.Attempt{ID: uuid.New(), UserID: user, MaterialType: attempts.MaterialWriting, MaterialID: uuid.New(), MaterialVersionID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.QueueWritingAssessment(ctx, att.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE writing_assessment_jobs SET updated_at=CURRENT_TIMESTAMP-INTERVAL '31 seconds' WHERE attempt_id=$1`, att.ID); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(repo, recoveredProvider{}, nil, unavailableQueue{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.recoverPending(ctx)
	job, err := repo.GetWritingAssessmentJob(ctx, att.ID)
	// Missing answers fail after claim and enter retry backoff. Old recovery
	// would only call Redis and leave attempts=0 forever during an outage.
	if err != nil || job.Attempts != 1 || job.Status != "QUEUED" {
		t.Fatalf("job not recovered directly: %+v err=%v", job, err)
	}
	worker.recoverPending(ctx)
	job, _ = repo.GetWritingAssessmentJob(ctx, att.ID)
	if job.Attempts != 1 {
		t.Fatal("ignored retry backoff")
	}
}
