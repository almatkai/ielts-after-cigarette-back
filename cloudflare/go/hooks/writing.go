package writingpipeline

import (
	"context"
	"github.com/google/uuid"
)

// CloudflareProcess is invoked by a durable alarm, never a permanently running goroutine.
func (w *Worker) CloudflareProcess(ctx context.Context, id uuid.UUID) { w.processAssessment(ctx, id) }
func (w *Worker) CloudflareRecover(ctx context.Context)               { w.recoverPending(ctx) }
