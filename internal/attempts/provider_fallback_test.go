package attempts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/aiproviders"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

type gradingRepo struct{ providers []aiproviders.Provider }

func (r gradingRepo) List(context.Context) ([]aiproviders.Provider, error) { return r.providers, nil }
func (r gradingRepo) Get(context.Context, uuid.UUID) (aiproviders.Provider, error) {
	return aiproviders.Provider{}, errors.New("unused")
}
func (r gradingRepo) Save(context.Context, aiproviders.Provider, uuid.UUID, bool) (aiproviders.Provider, error) {
	return aiproviders.Provider{}, errors.New("unused")
}
func (r gradingRepo) Delete(context.Context, uuid.UUID, int64) error { return errors.New("unused") }
func completion(w http.ResponseWriter, content, model string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"model": model, "choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}})
}
func gradingRouter(first, last string) *aiproviders.Service {
	// Only fixtures mark localhost providers as trusted; database rows always
	// route through the SSRF-protected transport in production.
	firstProvider := aiproviders.Provider{ID: uuid.New(), Endpoint: first, APIKey: "test-key", Model: "primary-writing", SpeakingModel: "primary-speaking", FromEnv: true, Enabled: true, TimeoutSeconds: 5, Scopes: []string{"writing", "speaking"}}
	return aiproviders.NewService(gradingRepo{[]aiproviders.Provider{firstProvider}}, nil, aiproviders.Provider{Endpoint: last, APIKey: "reserve-key", Model: "reserve-writing", SpeakingModel: "reserve-speaking", TimeoutSeconds: 5}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func criterionJSON() map[string]any { return map[string]any{"band": 6.5, "feedback": "Good"} }

func TestWritingAndSpeakingRetryInvalidModelOutput(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { completion(w, "not a valid evaluation", "primary") }))
	defer first.Close()
	task1, task2 := uuid.New(), uuid.New()
	part1, part2, part3 := uuid.New(), uuid.New(), uuid.New()
	last := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		var content any
		if payload.Model == "reserve-writing" {
			tasks := []any{}
			for number, id := range []uuid.UUID{task1, task2} {
				tasks = append(tasks, map[string]any{"taskId": id.String(), "taskNumber": number + 1, "criteria": map[string]any{"taskAchievementResponse": criterionJSON(), "coherence": criterionJSON(), "lexicalResource": criterionJSON(), "grammar": criterionJSON()}, "feedback": "Good", "strengths": []string{}, "improvements": []string{}})
			}
			content = map[string]any{"summary": "Good", "taskEvaluations": tasks}
		} else if payload.Model == "reserve-speaking" {
			parts := []any{}
			for _, id := range []uuid.UUID{part1, part2, part3} {
				parts = append(parts, map[string]any{"partId": id.String(), "transcript": "A sufficient spoken answer.", "feedback": "Good", "strengths": []string{}, "improvements": []string{}})
			}
			content = map[string]any{"criteria": map[string]any{"fluency": criterionJSON(), "lexicalResource": criterionJSON(), "grammar": criterionJSON()}, "summary": "Good", "partFeedback": parts}
		} else {
			t.Errorf("wrong fallback model: %s", payload.Model)
		}
		encoded, _ := json.Marshal(content)
		completion(w, string(encoded), payload.Model)
	}))
	defer last.Close()
	var events []httpx.ErrorEvent
	httpx.SetErrorReporter(func(_ context.Context, event httpx.ErrorEvent) { events = append(events, event) })
	defer httpx.SetErrorReporter(nil)
	evaluator := NewChatCompletionsEvaluator("", "", "", nil).WithProviders(gradingRouter(first.URL, last.URL))
	writing, err := evaluator.Evaluate(context.Background(), WritingEvaluationRequest{Tasks: []WritingTaskAnswer{{Task: WritingTask{ID: task1, Position: 1}}, {Task: WritingTask{ID: task2, Position: 2}}}})
	if err != nil || writing.Model != "reserve-writing" || len(writing.Tasks) != 2 || writing.OverallBand != 6.5 {
		t.Fatalf("writing fallback failed: %+v %v", writing, err)
	}
	speaking, err := evaluator.EvaluateSpeaking(context.Background(), SpeakingEvaluationRequest{Parts: []SpeakingPartAnswer{{Part: SpeakingPart{ID: part1, Position: 1}}, {Part: SpeakingPart{ID: part2, Position: 2}}, {Part: SpeakingPart{ID: part3, Position: 3}}}})
	if err != nil || speaking.Model != "reserve-speaking" || len(speaking.Parts) != 3 || speaking.OverallBand != 6.5 || speaking.PronunciationAvailable {
		t.Fatalf("speaking fallback failed: %+v %v", speaking, err)
	}
	if len(events) != 2 || events[0].AIPurpose != "writing" || events[1].AIPurpose != "speaking" {
		t.Fatal("grading errors not reported")
	}
}

func TestRouterPreservesNoAIConfiguredErrors(t *testing.T) {
	providers := aiproviders.NewService(gradingRepo{}, nil, aiproviders.Provider{}, nil)
	evaluator := NewChatCompletionsEvaluator("", "", "", nil).WithProviders(providers)
	if _, err := evaluator.Evaluate(context.Background(), WritingEvaluationRequest{}); !errors.Is(err, ErrAIUnavailable) {
		t.Fatalf("wrong writing configuration error: %v", err)
	}
	if _, err := evaluator.EvaluateSpeaking(context.Background(), SpeakingEvaluationRequest{}); !errors.Is(err, ErrAIUnavailable) {
		t.Fatalf("wrong speaking configuration error: %v", err)
	}
}

func gradingRouterWithTimeout(first, last string, timeoutSeconds int) *aiproviders.Service {
	firstProvider := aiproviders.Provider{ID: uuid.New(), Endpoint: first, APIKey: "test-key", Model: "primary-writing", SpeakingModel: "primary-speaking", FromEnv: true, Enabled: true, TimeoutSeconds: timeoutSeconds, Scopes: []string{"writing", "speaking"}}
	return aiproviders.NewService(gradingRepo{[]aiproviders.Provider{firstProvider}}, nil, aiproviders.Provider{Endpoint: last, APIKey: "reserve-key", Model: "reserve-writing", SpeakingModel: "reserve-speaking", TimeoutSeconds: timeoutSeconds}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSpeakingTimeoutReportsTimeoutCode(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer first.Close()
	part1 := uuid.New()
	last := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := map[string]any{
			"criteria": map[string]any{"fluency": criterionJSON(), "lexicalResource": criterionJSON(), "grammar": criterionJSON()},
			"summary": "Good",
			"partFeedback": []any{map[string]any{"partId": part1.String(), "transcript": "Answer", "feedback": "Good", "strengths": []string{}, "improvements": []string{}}},
		}
		encoded, _ := json.Marshal(content)
		completion(w, string(encoded), "reserve-speaking")
	}))
	defer last.Close()
	var events []httpx.ErrorEvent
	httpx.SetErrorReporter(func(_ context.Context, event httpx.ErrorEvent) { events = append(events, event) })
	defer httpx.SetErrorReporter(nil)
	evaluator := NewChatCompletionsEvaluator("", "", "", nil).WithProviders(gradingRouterWithTimeout(first.URL, last.URL, 1))
	speaking, err := evaluator.EvaluateSpeaking(context.Background(), SpeakingEvaluationRequest{Parts: []SpeakingPartAnswer{{Part: SpeakingPart{ID: part1, Position: 1}}}})
	if err != nil || speaking.Model != "reserve-speaking" {
		t.Fatalf("speaking fallback on timeout failed: %+v %v", speaking, err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 error event, got %d", len(events))
	}
	failure, ok := events[0].Error.(*aiproviders.Failure)
	if !ok || failure.Code != "timeout" {
		t.Fatalf("expected failure code 'timeout', got: %+v", events[0].Error)
	}
}
