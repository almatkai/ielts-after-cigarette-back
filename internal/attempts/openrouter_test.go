package attempts

import (
	"context"
	"errors"
	"testing"
)

func TestDecodeEvaluationRoundsAverageAndKeepsTaskFeedback(t *testing.T) {
	evaluation, err := decodeEvaluation(`{
		"criteria": {
			"taskResponse": {"band": 6.2, "feedback": "Address the overview more clearly."},
			"coherence": {"band": 6.5, "feedback": "Use clearer paragraphing."},
			"lexicalResource": {"band": 7.0, "feedback": "Good range."},
			"grammar": {"band": 6.5, "feedback": "Check articles."}
		},
		"summary": "A promising response.",
		"taskFeedback": [{
			"taskId": "00000000-0000-0000-0000-000000000001",
			"feedback": "Compare the key features.",
			"strengths": ["Clear overview"],
			"improvements": ["Use more data"]
		}]
	}`)
	if err != nil {
		t.Fatalf("decodeEvaluation returned error: %v", err)
	}
	if evaluation.Criteria.TaskResponse.Band != 6 {
		t.Fatalf("task response band = %v, want 6", evaluation.Criteria.TaskResponse.Band)
	}
	if evaluation.OverallBand != 6.5 {
		t.Fatalf("overall band = %v, want 6.5", evaluation.OverallBand)
	}
	if len(evaluation.Tasks) != 1 || evaluation.Tasks[0].Feedback == "" {
		t.Fatalf("task feedback was not preserved: %#v", evaluation.Tasks)
	}
}

func TestOpenRouterEvaluatorRequiresConfiguration(t *testing.T) {
	evaluator := NewOpenRouterEvaluator("", "openrouter/free", nil)
	_, err := evaluator.Evaluate(context.Background(), WritingEvaluationRequest{})
	if !errors.Is(err, ErrAIUnavailable) {
		t.Fatalf("Evaluate error = %v, want ErrAIUnavailable", err)
	}
}
