package attempts

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/ailimits"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestAttemptsDailyLimitWriting(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(opts)
	defer redisClient.Close()

	limiter := cache.NewDailyLimiter(redisClient)
	limitsService := ailimits.NewService(nil, limiter, redisClient, ailimits.Limits{
		WritingLimit:  1,
		SpeakingLimit: 1,
	})

	repo := newStubRepository()
	evaluator := mockWritingEvaluator{
		evaluation: WritingEvaluation{
			OverallBand: 7.0,
			Summary:     "Strong essay",
			Criteria: WritingCriteria{
				TaskResponse:    WritingCriterion{Band: 7.0, Feedback: "Clear response"},
				Coherence:       WritingCriterion{Band: 7.0, Feedback: "Well structured"},
				LexicalResource: WritingCriterion{Band: 7.0, Feedback: "Good range"},
				Grammar:         WritingCriterion{Band: 7.0, Feedback: "Accurate"},
			},
		},
	}
	svc := NewService(repo, map[string]MaterialProvider{
		MaterialWriting: writingStubProvider(),
	}, evaluator).WithDailyLimiter(limitsService)

	userID := uuid.New()
	studentCtx := auth.WithUser(context.Background(), userID, "STUDENT")

	// 1. Start first writing attempt -> should succeed
	mat1 := testMaterialID
	att1, _, created, err := svc.Start(studentCtx, userID, MaterialWriting, mat1)
	if err != nil || !created {
		t.Fatalf("first start failed: created=%v, err=%v", created, err)
	}

	// 2. Submit first writing attempt -> should succeed and consume quota
	answers := SaveAnswersInput{
		Answers: []AnswerInput{
			{QuestionID: questionChoiceID, Answer: map[string]any{"value": "Sample essay for task 1 that is reasonably long and valid."}},
			{QuestionID: questionMatchingID, Answer: map[string]any{"value": "Sample essay for task 2 that is reasonably long and valid."}},
		},
	}
	submitted, err := svc.Submit(studentCtx, userID, att1.ID, answers)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if submitted.Status != StatusSubmitted {
		t.Fatalf("expected submitted status, got: %s", submitted.Status)
	}

	// 3. Attempting to start a second writing attempt for new material -> should fail with ErrDailyLimitExceeded
	mat2 := uuid.New()
	_, _, _, err = svc.Start(studentCtx, userID, MaterialWriting, mat2)
	if !errors.Is(err, ErrDailyLimitExceeded) {
		t.Fatalf("expected ErrDailyLimitExceeded on start after limit reached, got: %v", err)
	}

	// 4. Admin user should bypass the limit
	adminID := uuid.New()
	adminCtx := auth.WithUser(context.Background(), adminID, "ADMIN")
	// Consume admin's quota directly to test bypass
	_, _ = limiter.Allow(context.Background(), "writing", adminID.String(), 1)
	_, _, adminCreated, err := svc.Start(adminCtx, adminID, MaterialWriting, mat2)
	if err != nil || !adminCreated {
		t.Fatalf("admin start should bypass limit, got: created=%v, err=%v", adminCreated, err)
	}
}
