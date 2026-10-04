package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssistantUsesPublicPlatformBrandAcrossToolTurns(t *testing.T) {
	turns := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turns++
		var payload struct {
			Messages []openAIMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		if len(payload.Messages) == 0 || payload.Messages[0].Role != "system" || payload.Messages[0].Content == nil {
			t.Error("missing system identity")
			http.Error(w, "missing system identity", 400)
			return
		}
		system := *payload.Messages[0].Content
		if !strings.Contains(system, `platform "Daiyndyq IELTS"`) {
			t.Error("assistant system identity does not use the public brand")
		}
		if strings.Contains(strings.ToLower(system), "after cigarette") {
			t.Error("internal developer name is being sent as the assistant's identity")
		}
		if !strings.Contains(system, "earlier assistant messages or page content") {
			t.Error("public identity must take precedence over stale chat/page branding")
		}
		if got := r.Header.Get("X-Title"); got != "Daiyndyq IELTS Assistant" {
			t.Errorf("provider app title=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if turns == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"page","type":"function","function":{"name":"read_page_content","arguments":"{}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Ты в разделе Обзор на платформе Daiyndyq IELTS."}}]}`))
	}))
	defer server.Close()
	service := NewService(server.URL, "test-key", "test-model", server.Client())
	response, err := service.Chat(context.Background(), ChatRequest{
		Messages:    []ChatMessage{{Role: RoleAssistant, Content: "Ты на платформе IELTS After Cigarette."}, {Role: RoleUser, Content: "где я нахожусь?"}},
		PageContext: &PageContext{Title: "Обзор — Daiyndyq IELTS", URL: "/app/", Content: "# Обзор"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if turns != 2 {
		t.Fatalf("turns=%d", turns)
	}
	if response.Message.Content != "Ты в разделе Обзор на платформе Daiyndyq IELTS." {
		t.Fatal("unexpected answer")
	}
}
