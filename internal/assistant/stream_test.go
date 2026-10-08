package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/aiproviders"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

type streamRepo struct{ items []aiproviders.Provider }

func (r streamRepo) List(context.Context) ([]aiproviders.Provider, error) { return r.items, nil }
func (r streamRepo) Get(context.Context, uuid.UUID) (aiproviders.Provider, error) {
	return aiproviders.Provider{}, errors.New("unused")
}
func (r streamRepo) Save(context.Context, aiproviders.Provider, uuid.UUID, bool) (aiproviders.Provider, error) {
	return aiproviders.Provider{}, errors.New("unused")
}
func (r streamRepo) Delete(context.Context, uuid.UUID, int64) error { return errors.New("unused") }

type streamSink struct{ events []httpx.ErrorEvent }

func (s *streamSink) ReportError(_ context.Context, event httpx.ErrorEvent) {
	s.events = append(s.events, event)
}
func writeChunk(w http.ResponseWriter, delta any, reason any) {
	body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
	fmt.Fprintf(w, "data: %s\n\n", body)
	w.(http.Flusher).Flush()
}
func streamRequest() ChatRequest {
	return ChatRequest{Messages: []ChatMessage{{Role: RoleUser, Content: "Help with IELTS"}}}
}
func routedStream(first, second string) *Service {
	// Test doubles mark localhost fixtures as trusted; real database rows never
	// carry FromEnv, so production URLs always use protected DNS/TLS transport.
	repo := streamRepo{[]aiproviders.Provider{{ID: uuid.New(), Endpoint: first, APIKey: "first-secret", Model: "first", FromEnv: true, Enabled: true, Scopes: []string{"assistant"}, TimeoutSeconds: 5}}}
	providers := aiproviders.NewService(repo, nil, aiproviders.Provider{Endpoint: second, APIKey: "last-secret", Model: "reserve", TimeoutSeconds: 5}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewService("", "", "", nil).WithProviders(providers)
}

func TestBrokenStreamResetsPartialAnswerAndReportsRecoveredFailure(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeChunk(w, map[string]any{"content": "unfinished-provider-text"}, nil)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeChunk(w, map[string]any{"content": "Correct answer"}, nil)
		writeChunk(w, map[string]any{}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer second.Close()
	sink := &streamSink{}
	httpx.SetErrorReporter(sink.ReportError)
	defer httpx.SetErrorReporter(nil)
	var visible string
	var events []StreamEvent
	result, err := routedStream(first.URL, second.URL).Stream(context.Background(), streamRequest(), func(event StreamEvent) error {
		events = append(events, event)
		if event.Type == "reset" {
			visible = ""
		}
		if event.Type == "delta" {
			visible += event.Text
		}
		return nil
	})
	if err != nil || visible != "Correct answer" || result.Message.Content != "Correct answer" {
		t.Fatalf("mixed or missing answer: visible=%q result=%+v err=%v", visible, result, err)
	}
	if len(events) < 4 || events[0].Type != "reset" || events[1].Text != "unfinished-provider-text" || events[2].Type != "reset" {
		t.Fatalf("no mid-stream reset: %+v", events)
	}
	if len(sink.events) != 1 || strings.Contains(sink.events[0].Error.Error(), "secret") {
		t.Fatal("recovered stream error not safely reported")
	}
}

func TestAllFailuresProduceNeutralSSEWithoutProviderDetails(t *testing.T) {
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		fmt.Fprint(w, "first-secret last-secret provider-stacktrace")
	}))
	defer failed.Close()
	sink := &streamSink{}
	httpx.SetErrorReporter(sink.ReportError)
	defer httpx.SetErrorReporter(nil)
	handler := NewHandler(routedStream(failed.URL, failed.URL), slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20)
	body, _ := json.Marshal(streamRequest())
	recorder := httptest.NewRecorder()
	handler.Stream(recorder, httptest.NewRequest("POST", "/assistant/chat/stream", strings.NewReader(string(body))))
	output := recorder.Body.String()
	if recorder.Code != 200 || !strings.Contains(output, `"type":"unavailable"`) || !strings.Contains(output, "Попробуй") {
		t.Fatalf("bad user failure: %d %s", recorder.Code, output)
	}
	for _, technical := range []string{"secret", "stacktrace", "HTTP status", "provider_id"} {
		if strings.Contains(output, technical) {
			t.Fatal("technical details reached student")
		}
	}
	if len(sink.events) != 2 {
		t.Fatal("not every failed provider was reported")
	}
}

func TestStreamHandlesFragmentedToolCallsAndPageContext(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []openAIMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		if calls == 1 {
			writeChunk(w, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call-page", "type": "function", "function": map[string]string{"name": "read_page_", "arguments": "{"}}}}, nil)
			writeChunk(w, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]string{"name": "content", "arguments": "}"}}}}, nil)
			writeChunk(w, map[string]any{}, "tool_calls")
		} else {
			last := payload.Messages[len(payload.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "call-page" || last.Content == nil || !strings.Contains(*last.Content, "Reading passage") {
				t.Errorf("missing tool context: %+v", last)
			}
			writeChunk(w, map[string]any{"content": "Page-aware answer"}, nil)
			writeChunk(w, map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	request := streamRequest()
	request.PageContext = &PageContext{Title: "Reading", Content: "Reading passage", URL: "https://example.test/reading"}
	service := NewService(server.URL, "key", "model", server.Client())
	result, err := service.Stream(context.Background(), request, func(StreamEvent) error { return nil })
	if err != nil || calls != 2 || result.Message.Content != "Page-aware answer" || len(result.ToolCalls) != 1 {
		t.Fatalf("tool stream failed: %+v %v", result, err)
	}
}

func TestTruncatedOrErrorChunksAreNotSuccessful(t *testing.T) {
	for name, body := range map[string]string{
		"truncated":      `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n",
		"upstream-error": `data: {"error":{"message":"secret error"}}` + "\n\n",
		"length":         `data: {"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}` + "\n\ndata: [DONE]\n\n",
		"missing-finish": "data: [DONE]\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			if _, err := NewService(server.URL, "key", "model", server.Client()).Stream(context.Background(), streamRequest(), func(StreamEvent) error { return nil }); err == nil {
				t.Fatal("failed stream was accepted")
			}
		})
	}
}
