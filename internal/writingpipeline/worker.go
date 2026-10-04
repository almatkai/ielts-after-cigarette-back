package writingpipeline

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

type Queue interface {
	EnqueueWritingAssessment(context.Context, uuid.UUID) error
	PopWritingAssessment(context.Context) (uuid.UUID, error)
}

type Worker struct {
	repository *attempts.PostgresRepository
	provider   attempts.MaterialProvider
	evaluator  attempts.WritingEvaluator
	queue      Queue
	logger     *slog.Logger
}

func NewWorker(
	repository *attempts.PostgresRepository,
	provider attempts.MaterialProvider,
	evaluator attempts.WritingEvaluator,
	queue Queue,
	logger *slog.Logger,
) *Worker {
	return &Worker{
		repository: repository,
		provider:   provider,
		evaluator:  evaluator,
		queue:      queue,
		logger:     logger,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	assessmentEvents := make(chan uuid.UUID, 8)
	go w.consume(ctx, w.queue.PopWritingAssessment, assessmentEvents)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case id := <-assessmentEvents:
			w.processAssessment(ctx, id)
		case <-ticker.C:
			w.recoverPending(ctx)
		}
	}
}

func (w *Worker) consume(ctx context.Context, pop func(context.Context) (uuid.UUID, error), output chan<- uuid.UUID) {
	for ctx.Err() == nil {
		id, err := pop(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Warn("writing queue pop failed", "error", err)
				time.Sleep(time.Second)
			}
			continue
		}
		select {
		case output <- id:
		case <-ctx.Done():
			return
		}
	}
}

func (w *Worker) recoverPending(ctx context.Context) {
	if err := w.repository.RecoverWritingJobs(ctx); err != nil {
		w.logger.Error("recover writing jobs", "error", err)
		return
	}
	ids, err := w.repository.PendingWritingAssessmentIDs(ctx, 1)
	if err != nil {
		w.logger.Error("list pending writing assessments", "error", err)
		return
	}
	// PostgreSQL is the durable queue; Redis only reduces wake-up latency.
	// Process recovered jobs directly, so Redis outages cannot stall grading.
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		w.processAssessment(ctx, id)
	}
}

func (w *Worker) processAssessment(ctx context.Context, attemptID uuid.UUID) {
	claimed, err := w.repository.ClaimWritingAssessment(ctx, attemptID)
	if err != nil || !claimed {
		if err != nil {
			w.logger.Error("claim writing assessment", "attempt_id", attemptID, "error", err)
		}
		return
	}
	stopHeartbeat := keepLease(ctx, func(ctx context.Context) error {
		return w.repository.RenewWritingAssessment(ctx, attemptID)
	})
	defer stopHeartbeat()

	attempt, err := w.repository.Get(ctx, attemptID)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}
	if attempt.Status == attempts.StatusSubmitted {
		_ = w.repository.CompleteWritingAssessment(ctx, attemptID)
		return
	}

	material, err := w.provider.GradingStructure(ctx, attempt.MaterialID, attempt.MaterialVersionID)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}

	saved, err := w.repository.ListAnswers(ctx, attemptID)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}

	byTask := map[uuid.UUID]string{}
	for _, item := range saved {
		if text, ok := item.Answer["value"].(string); ok {
			byTask[item.QuestionID] = text
		}
	}

	request := attempts.WritingEvaluationRequest{
		ExamType: material.ExamType,
		Tasks:    make([]attempts.WritingTaskAnswer, 0, len(material.WritingTasks)),
	}
	gradedAnswers := make([]attempts.Answer, 0, len(material.WritingTasks))
	for _, task := range material.WritingTasks {
		text := strings.TrimSpace(byTask[task.ID])
		if text == "" {
			w.failAssessment(ctx, attemptID, attempts.ErrWritingIncomplete)
			return
		}
		request.Tasks = append(request.Tasks, attempts.WritingTaskAnswer{Task: task, Text: text})
		gradedAnswers = append(gradedAnswers, attempts.Answer{
			QuestionID: task.ID,
			Answer:     map[string]any{"value": text},
		})
	}

	if w.evaluator == nil {
		w.failAssessment(ctx, attemptID, attempts.ErrAIUnavailable)
		return
	}

	evaluation, err := w.evaluator.Evaluate(ctx, request)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}
	evaluation.AttemptID = attemptID

	band := evaluation.OverallBand
	score := int(math.Round(band * 10))
	accuracy := math.Round(band/9*10000) / 100

	if err := w.repository.Submit(ctx, attempts.SubmitResult{
		AttemptID:         attemptID,
		UserID:            attempt.UserID,
		Skill:             attempts.MaterialWriting,
		Answers:           gradedAnswers,
		Score:             score,
		MaxScore:          90,
		Band:              band,
		Accuracy:          accuracy,
		WritingEvaluation: &evaluation,
	}); err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}

	_ = w.repository.CompleteWritingAssessment(ctx, attemptID)
}

func (w *Worker) failAssessment(ctx context.Context, attemptID uuid.UUID, err error) {
	w.logger.Error("writing assessment failed", "attempt_id", attemptID, "error", err)
	httpx.ReportBackground(ctx, "writing_assessment", "attempt_id", attemptID, err)
	if updateErr := w.repository.FailWritingAssessment(ctx, attemptID, truncate(err.Error(), 2000)); updateErr != nil && ctx.Err() == nil {
		w.logger.Error("persist writing assessment failure", "attempt_id", attemptID, "error", updateErr)
	}
}

func keepLease(ctx context.Context, renew func(context.Context) error) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = renew(ctx)
			}
		}
	}()
	return cancel
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
