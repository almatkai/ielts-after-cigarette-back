package writingpipeline

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

type stubQueue struct {
	items []uuid.UUID
}

func (q *stubQueue) EnqueueWritingAssessment(_ context.Context, id uuid.UUID) error {
	q.items = append(q.items, id)
	return nil
}

func (q *stubQueue) PopWritingAssessment(ctx context.Context) (uuid.UUID, error) {
	if len(q.items) == 0 {
		<-ctx.Done()
		return uuid.Nil, ctx.Err()
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, nil
}

func TestWorkerContextCancellation(t *testing.T) {
	q := &stubQueue{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := NewWorker(nil, nil, nil, q, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := worker.Run(ctx)
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}
