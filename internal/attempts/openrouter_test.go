package attempts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
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

func TestDecodeSpeakingEvaluationRoundsCriteriaAndKeepsTranscript(t *testing.T) {
	partID := uuid.New()
	evaluation, err := decodeSpeakingEvaluation(`{
		"criteria": {
			"fluency": {"band": 6.2, "feedback": "Link ideas more naturally."},
			"lexicalResource": {"band": 6.5, "feedback": "Good topic vocabulary."},
			"grammar": {"band": 7.0, "feedback": "Accurate complex sentences."},
			"pronunciation": {"band": 6.5, "feedback": "Stress word endings."}
		},
		"summary": "A confident response.",
		"partFeedback": [{
			"partId": "` + partID.String() + `",
			"transcript": "I would like to talk about my hometown.",
			"feedback": "Add a specific example.",
			"strengths": ["Natural pace"],
			"improvements": ["Pause less"]
		}]
	}`)
	if err != nil {
		t.Fatalf("decodeSpeakingEvaluation returned error: %v", err)
	}
	if evaluation.Criteria.Fluency.Band != 6 {
		t.Fatalf("fluency band = %v, want 6", evaluation.Criteria.Fluency.Band)
	}
	if evaluation.OverallBand != 6.5 {
		t.Fatalf("overall band = %v, want 6.5", evaluation.OverallBand)
	}
	if len(evaluation.Parts) != 1 || evaluation.Parts[0].Transcript == "" {
		t.Fatalf("part transcript was not preserved: %#v", evaluation.Parts)
	}
}

func TestOpenRouterEvaluatorRequiresConfiguration(t *testing.T) {
	evaluator := NewOpenRouterEvaluator("", "openrouter/free", nil)
	_, err := evaluator.Evaluate(context.Background(), WritingEvaluationRequest{})
	if !errors.Is(err, ErrAIUnavailable) {
		t.Fatalf("Evaluate error = %v, want ErrAIUnavailable", err)
	}
}

func TestChatCompletionsEvaluatorUsesConfiguredProvider(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v/chat/completions" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Model != "qwen3-8" {
			t.Errorf("model = %q", payload.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen3-8","choices":[{"message":{"content":"{\"criteria\":{\"taskResponse\":{\"band\":6.5,\"feedback\":\"Good\"},\"coherence\":{\"band\":6.5,\"feedback\":\"Good\"},\"lexicalResource\":{\"band\":6.5,\"feedback\":\"Good\"},\"grammar\":{\"band\":6.5,\"feedback\":\"Good\"}},\"summary\":\"Good\",\"taskFeedback\":[]}"}}]}`))
	}))
	defer server.Close()

	evaluator := NewChatCompletionsEvaluator(server.URL+"/v/chat/completions", "test-key", "qwen3-8", server.Client())
	evaluation, err := evaluator.Evaluate(context.Background(), WritingEvaluationRequest{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if evaluation.Model != "qwen3-8" || evaluation.OverallBand != 6.5 {
		t.Fatalf("evaluation = %#v", evaluation)
	}
}
