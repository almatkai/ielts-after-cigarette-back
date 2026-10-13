package attempts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeEvaluationRoundsAverageAndKeepsTaskFeedback(t *testing.T) {
	evaluation, err := decodeEvaluation(`{
		"summary": "A promising response.",
		"taskEvaluations": [{
			"taskId": "00000000-0000-0000-0000-000000000001",
			"taskNumber": 1,
			"criteria": {
				"taskAchievementResponse": {"band": 6.2, "feedback": "Address the overview more clearly."},
				"coherence": {"band": 6.5, "feedback": "Use clearer paragraphing."},
				"lexicalResource": {"band": 7.0, "feedback": "Good range."},
				"grammar": {"band": 6.5, "feedback": "Check articles."}
			},
			"feedback": "Compare the key features.",
			"strengths": ["Clear overview"],
			"improvements": ["Use more data"]
		}, {
			"taskId": "00000000-0000-0000-0000-000000000002",
			"taskNumber": 2,
			"criteria": {
				"taskAchievementResponse": {"band": 7.0, "feedback": "Clear position."},
				"coherence": {"band": 7.0, "feedback": "Logical progression."},
				"lexicalResource": {"band": 7.0, "feedback": "Good range."},
				"grammar": {"band": 7.0, "feedback": "Good control."}
			},
			"feedback": "Well developed.",
			"strengths": ["Clear position"],
			"improvements": ["Add one example"]
		}]
	}`)
	if err != nil {
		t.Fatalf("decodeEvaluation returned error: %v", err)
	}
	if evaluation.Criteria.TaskResponse.Band != 6.5 {
		t.Fatalf("task response band = %v, want 6.5", evaluation.Criteria.TaskResponse.Band)
	}
	if evaluation.OverallBand != 7 {
		t.Fatalf("overall band = %v, want 7", evaluation.OverallBand)
	}
	if len(evaluation.Tasks) != 2 || evaluation.Tasks[0].Feedback == "" || evaluation.Tasks[1].Band != 7 {
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

func TestDecodeSpeakingEvaluationIgnoresReasoningBraces(t *testing.T) {
	partID := uuid.New()
	content := `<think>I first considered {"band": 7}, but that is not the response.</think>
{
  "criteria": {
    "fluency": {"band": 6.5, "feedback": "Clear."},
    "lexicalResource": {"band": 7, "feedback": "Varied."},
    "grammar": {"band": 6.5, "feedback": "Mostly accurate."}
  },
  "summary": "A clear response.",
  "partFeedback": [{
    "partId": "` + partID.String() + `",
    "transcript": "A short answer.",
    "feedback": "Develop the example.",
    "strengths": ["Clear position"],
    "improvements": ["Add detail"]
  }]
}`
	evaluation, err := decodeSpeakingEvaluation(content)
	if err != nil {
		t.Fatalf("decodeSpeakingEvaluation returned error: %v", err)
	}
	if evaluation.OverallBand != 6.5 || len(evaluation.Parts) != 1 || evaluation.Parts[0].PartID != partID {
		t.Fatalf("unexpected evaluation: %#v", evaluation)
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
	taskOneID := uuid.MustParse("00000000-0000-0000-0000-000000000011")
	taskTwoID := uuid.MustParse("00000000-0000-0000-0000-000000000012")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v/chat/completions" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Title"); got != "Daiyndyq IELTS" {
			t.Errorf("provider app title = %q", got)
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
		_, _ = w.Write([]byte(`{"model":"qwen3-8","choices":[{"message":{"content":"{\"summary\":\"Good\",\"taskEvaluations\":[{\"taskId\":\"00000000-0000-0000-0000-000000000011\",\"taskNumber\":1,\"criteria\":{\"taskAchievementResponse\":{\"band\":6.5,\"feedback\":\"Good\"},\"coherence\":{\"band\":6.5,\"feedback\":\"Good\"},\"lexicalResource\":{\"band\":6.5,\"feedback\":\"Good\"},\"grammar\":{\"band\":6.5,\"feedback\":\"Good\"}},\"feedback\":\"Good\",\"strengths\":[],\"improvements\":[]},{\"taskId\":\"00000000-0000-0000-0000-000000000012\",\"taskNumber\":2,\"criteria\":{\"taskAchievementResponse\":{\"band\":6.5,\"feedback\":\"Good\"},\"coherence\":{\"band\":6.5,\"feedback\":\"Good\"},\"lexicalResource\":{\"band\":6.5,\"feedback\":\"Good\"},\"grammar\":{\"band\":6.5,\"feedback\":\"Good\"}},\"feedback\":\"Good\",\"strengths\":[],\"improvements\":[]}] }"}}]}`))
	}))
	defer server.Close()

	evaluator := NewChatCompletionsEvaluator(server.URL+"/v/chat/completions", "test-key", "qwen3-8", server.Client())
	evaluation, err := evaluator.Evaluate(context.Background(), WritingEvaluationRequest{Tasks: []WritingTaskAnswer{
		{Task: WritingTask{ID: taskOneID, Position: 1}},
		{Task: WritingTask{ID: taskTwoID, Position: 2}},
	}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if evaluation.Model != "qwen3-8" || evaluation.OverallBand != 6.5 {
		t.Fatalf("evaluation = %#v", evaluation)
	}
}

func TestChatCompletionsEvaluatorDecodesAnnotationsAndModelParagraph(t *testing.T) {
	t.Helper()
	rawJSON := `{"summary":"Хорошая работа","taskEvaluations":[{"taskId":"00000000-0000-0000-0000-000000000011","taskNumber":1,"criteria":{"taskAchievementResponse":{"band":7.0,"feedback":"Accurate overview."},"coherence":{"band":6.5,"feedback":"Logical paragraphs."},"lexicalResource":{"band":6.5,"feedback":"Appropriate vocabulary."},"grammar":{"band":7.0,"feedback":"Mostly accurate."}},"feedback":"Task 1 feedback","strengths":["Clear trend"],"improvements":["Add data comparisons"],"annotations":[{"id":1,"quote":"increased sharply","replacement":"rose dramatically","type":"vocabulary","severity":"suggestion","explanationRu":"Более академичный глагол для описания резкого роста.","alternatives":["surged","climbed steeply"]}],"modelParagraph":"Overall, the number of visitors rose dramatically over the decade."},{"taskId":"00000000-0000-0000-0000-000000000012","taskNumber":2,"criteria":{"taskAchievementResponse":{"band":6.5,"feedback":"Clear position."},"coherence":{"band":6.5,"feedback":"Good progression."},"lexicalResource":{"band":6.0,"feedback":"Basic word choice."},"grammar":{"band":7.0,"feedback":"Mostly accurate."}},"feedback":"Task 2 feedback","strengths":["Well structured"],"improvements":["Support with examples"],"annotations":[{"id":1,"quote":"a lot of people thinks","replacement":"many people think","type":"grammar","severity":"error","explanationRu":"Подлежащее во множественном числе — глагол без -s.","alternatives":[]},{"id":2,"quote":"very important","replacement":"vital","type":"vocabulary","severity":"suggestion","explanationRu":"Выбирайте точное слово по смыслу.","alternatives":["crucial","essential"]}],"modelParagraph":"Nowadays, many people believe that studying abroad plays a pivotal role in cultivating a successful career."}]}`

	evaluation, err := decodeEvaluation(rawJSON)
	if err != nil {
		t.Fatalf("decodeEvaluation error: %v", err)
	}

	if len(evaluation.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(evaluation.Tasks))
	}

	task1 := evaluation.Tasks[0]
	if len(task1.Annotations) != 1 || task1.ModelParagraph != "Overall, the number of visitors rose dramatically over the decade." {
		t.Fatalf("task 1 annotations/modelParagraph mismatch: %#v", task1)
	}
	if task1.Annotations[0].Quote != "increased sharply" || task1.Annotations[0].Type != "vocabulary" {
		t.Fatalf("task 1 annotation mismatch: %#v", task1.Annotations[0])
	}

	task2 := evaluation.Tasks[1]
	if len(task2.Annotations) != 2 {
		t.Fatalf("expected 2 annotations in task 2, got %d", len(task2.Annotations))
	}
	if task2.Annotations[0].Quote != "a lot of people thinks" || task2.Annotations[0].Severity != "error" {
		t.Fatalf("task 2 annotation 0 mismatch: %#v", task2.Annotations[0])
	}
	if task2.Annotations[1].Quote != "very important" || task2.Annotations[1].Severity != "suggestion" {
		t.Fatalf("task 2 annotation 1 mismatch: %#v", task2.Annotations[1])
	}
	if task2.ModelParagraph == "" {
		t.Fatalf("expected task 2 modelParagraph to be non-empty")
	}
}

func TestChatCompletionsEvaluatorGroundsAnnotationsInMatchingAnswer(t *testing.T) {
	taskOneID := uuid.New()
	taskTwoID := uuid.New()
	annotation := func(id int, quote, kind, severity string) WritingAnnotation {
		return WritingAnnotation{ID: id, Quote: quote, Replacement: "Better wording", Type: kind,
			Severity: severity, ExplanationRu: "Объяснение замечания."}
	}
	missingExplanation := annotation(9, "rose", "grammar", "error")
	missingExplanation.ExplanationRu = " "
	taskFeedback := func(id uuid.UUID, position int, annotations []WritingAnnotation) map[string]any {
		return map[string]any{
			"taskId": id, "taskNumber": position,
			"criteria": map[string]any{
				"taskAchievementResponse": WritingCriterion{Band: 5, Feedback: "Incomplete task coverage."},
				"coherence":               WritingCriterion{Band: 7}, "lexicalResource": WritingCriterion{Band: 7},
				"grammar": WritingCriterion{Band: 7},
			},
			"annotations": annotations, "modelParagraph": "An illustrative paragraph.",
		}
	}
	content, err := json.Marshal(map[string]any{"taskEvaluations": []any{
		taskFeedback(taskOneID, 1, []WritingAnnotation{
			annotation(4, "invented phrase", "grammar", "error"),
			annotation(4, "visitors rose", "vocabulary", "suggestion"),
			annotation(4, "visitors rose", "vocabulary", "suggestion"),
			annotation(4, "Visitors rose", "grammar", "error"),
			annotation(4, "task two only", "grammar", "error"),
			annotation(4, "rose", "unknown", "error"),
			annotation(4, "rose", "grammar", "unknown"), missingExplanation,
			annotation(4, "São Paulo", "cohesion", "suggestion"),
		}),
		taskFeedback(taskTwoID, 2, []WritingAnnotation{
			annotation(-1, "visitors rose", "grammar", "error"),
			annotation(-1, "task two only", "grammar", "suggestion"),
		}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"message": map[string]string{"content": string(content)}},
		}})
	}))
	defer server.Close()
	evaluator := NewChatCompletionsEvaluator(server.URL, "test-key", "test-model", server.Client())
	evaluation, err := evaluator.Evaluate(context.Background(), WritingEvaluationRequest{Tasks: []WritingTaskAnswer{
		{Task: WritingTask{ID: taskOneID, Position: 1}, Text: "The visitors rose in São Paulo."},
		{Task: WritingTask{ID: taskTwoID, Position: 2}, Text: "This is task two only."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	wantOne := []WritingAnnotation{
		annotation(1, "visitors rose", "vocabulary", "suggestion"),
		annotation(2, "São Paulo", "cohesion", "suggestion"),
	}
	if !reflect.DeepEqual(evaluation.Tasks[0].Annotations, wantOne) {
		t.Fatalf("task 1 annotations = %#v, want %#v", evaluation.Tasks[0].Annotations, wantOne)
	}
	wantTwo := []WritingAnnotation{annotation(1, "task two only", "grammar", "suggestion")}
	if !reflect.DeepEqual(evaluation.Tasks[1].Annotations, wantTwo) {
		t.Fatalf("task 2 annotations = %#v, want %#v", evaluation.Tasks[1].Annotations, wantTwo)
	}
	for _, task := range evaluation.Tasks {
		if task.Criteria.TaskResponse.Band != 5 || task.Criteria.Grammar.Band != 7 || task.ModelParagraph == "" {
			t.Fatalf("independent scores or paragraph changed while validating annotations: %#v", task)
		}
	}
}

func TestChatCompletionsEvaluatorRejectsMismatchedTaskPositions(t *testing.T) {
	taskOneID, taskTwoID := uuid.New(), uuid.New()
	content := `{"taskEvaluations":[{"taskId":"` + taskTwoID.String() + `","taskNumber":1,"criteria":{"taskAchievementResponse":{"band":7},"coherence":{"band":7},"lexicalResource":{"band":7},"grammar":{"band":7}}},{"taskId":"` + taskOneID.String() + `","taskNumber":2,"criteria":{"taskAchievementResponse":{"band":7},"coherence":{"band":7},"lexicalResource":{"band":7},"grammar":{"band":7}}}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"message": map[string]string{"content": content}},
		}})
	}))
	defer server.Close()
	evaluator := NewChatCompletionsEvaluator(server.URL, "test-key", "test-model", server.Client())
	_, err := evaluator.Evaluate(context.Background(), WritingEvaluationRequest{Tasks: []WritingTaskAnswer{
		{Task: WritingTask{ID: taskOneID, Position: 1}},
		{Task: WritingTask{ID: taskTwoID, Position: 2}},
	}})
	if !errors.Is(err, ErrAIEvaluationFailed) {
		t.Fatalf("Evaluate() error = %v, want ErrAIEvaluationFailed", err)
	}
}
