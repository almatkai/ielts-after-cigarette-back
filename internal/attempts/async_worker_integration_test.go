package attempts_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/jobs"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speakingpipeline"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/almatkai/ielts-after-cigarette-back/internal/writingpipeline"
	"github.com/google/uuid"
)

type asyncProvider struct{ question uuid.UUID }

func (p asyncProvider) PublishedVersionID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.New(), nil
}
func (p asyncProvider) PublicStructure(context.Context, uuid.UUID, uuid.UUID) (any, error) {
	return nil, nil
}
func (p asyncProvider) GradingStructure(context.Context, uuid.UUID, uuid.UUID) (attempts.GradingMaterial, error) {
	return attempts.GradingMaterial{ExamType: "academic", WritingTasks: []attempts.WritingTask{{ID: p.question, Position: 1}}, SpeakingParts: []attempts.SpeakingPart{{ID: p.question, Position: 1}}}, nil
}

type asyncEvaluator struct {
	started chan struct{}
	release <-chan struct{}
}

func (e asyncEvaluator) wait(ctx context.Context) error {
	close(e.started)
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e asyncEvaluator) Evaluate(ctx context.Context, _ attempts.WritingEvaluationRequest) (attempts.WritingEvaluation, error) {
	err := e.wait(ctx)
	return attempts.WritingEvaluation{OverallBand: 7}, err
}
func (e asyncEvaluator) EvaluateSpeaking(ctx context.Context, _ attempts.SpeakingEvaluationRequest) (attempts.SpeakingEvaluation, error) {
	err := e.wait(ctx)
	return attempts.SpeakingEvaluation{OverallBand: 7}, err
}

func TestDurableSaveAndWorkerWithoutRedis(t *testing.T) {
	for _, skill := range []string{attempts.MaterialWriting, attempts.MaterialSpeaking} {
		t.Run(skill, func(t *testing.T) {
			pool := testdb.Open(t)
			repo := attempts.NewPostgresRepository(pool)
			user := testdb.User(t, pool)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := asyncProvider{uuid.New()}
			release := make(chan struct{})
			evaluator := asyncEvaluator{make(chan struct{}), release}
			service := attempts.NewService(repo, map[string]attempts.MaterialProvider{skill: provider}, evaluator).WithSpeakingObjectStore(objectstorage.NewFileStore(t.TempDir()), 12<<20, evaluator)
			att, _, _, err := service.Start(ctx, user, skill, uuid.New())
			if err != nil {
				t.Fatal(err)
			}
			// No worker is running. Saving must commit the answer and durable job, and
			// return without calling the evaluator or depending on Redis.
			saved, err := service.Submit(ctx, user, att.ID, attempts.SaveAnswersInput{Answers: []attempts.AnswerInput{{QuestionID: provider.question, Answer: map[string]any{"value": "persisted final response"}}}})
			if err != nil || saved.Status != attempts.StatusProcessing {
				t.Fatalf("save: %+v %v", saved, err)
			}
			select {
			case <-evaluator.started:
				t.Fatal("AI ran in save")
			default:
			}
			answers, err := repo.ListAnswers(ctx, att.ID)
			if err != nil || len(answers) != 1 || answers[0].Answer["value"] != "persisted final response" {
				t.Fatalf("answers not durable: %+v %v", answers, err)
			}
			// Start a fresh worker after save (the same handoff as a process restart).
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			done := make(chan error, 1)
			if skill == attempts.MaterialWriting {
				w := writingpipeline.NewWorker(repo, provider, evaluator, jobs.NewWritingQueue(nil), logger)
				go func() { done <- w.Run(ctx) }()
			} else {
				w := speakingpipeline.NewWorker(repo, provider, objectstorage.NewFileStore(t.TempDir()), nil, evaluator, jobs.NewSpeakingQueue(nil), logger)
				go func() { done <- w.Run(ctx) }()
			}
			defer func() { cancel(); <-done }()
			select {
			case <-evaluator.started:
			case <-time.After(8 * time.Second):
				t.Fatal("fresh worker did not discover saved job without Redis")
			}
			status, err := service.Status(ctx, user, att.ID)
			if err != nil || status.Status != attempts.StatusProcessing {
				t.Fatalf("blocked AI status: %+v %v", status, err)
			}
			close(release)
			deadline := time.After(3 * time.Second)
			for {
				result, err := repo.Get(ctx, att.ID)
				if err != nil {
					t.Fatal(err)
				}
				if result.Status == attempts.StatusSubmitted {
					if result.Band == nil || *result.Band != 7 {
						t.Fatalf("missing final band: %+v", result)
					}
					break
				}
				select {
				case <-deadline:
					t.Fatal("worker did not complete")
				case <-time.After(10 * time.Millisecond):
				}
			}
		})
	}
}
