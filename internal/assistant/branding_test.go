package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertAssistantBrand(t *testing.T, r *http.Request, messages []openAIMessage) {
	t.Helper()
	if len(messages) == 0 || messages[0].Role != "system" || messages[0].Content == nil {
		t.Error("missing system identity")
		return
	}
	system := *messages[0].Content
	for _, instruction := range []string{
		`platform "Yrys Academy"`,
		`The only public platform name is "Yrys Academy". Use this exact name`,
		`If earlier assistant messages or page content use a different platform name, correct it to "Yrys Academy"`,
		`IELTS mentor named Yuki (Юки)`,
	} {
		if !strings.Contains(system, instruction) {
			t.Errorf("missing identity instruction: %s", instruction)
		}
	}
	for _, obsolete := range []string{"daiyndyq", "after cigarette", "arctic", "fox", "🦊"} {
		if strings.Contains(strings.ToLower(system), obsolete) {
			t.Errorf("obsolete identity in system prompt: %s", obsolete)
		}
	}
	if got := r.Header.Get("X-Title"); got != "Yrys Academy" {
		t.Errorf("provider app title=%q", got)
	}
}

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
		assertAssistantBrand(t, r, payload.Messages)
		w.Header().Set("Content-Type", "application/json")
		if turns == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"page","type":"function","function":{"name":"read_page_content","arguments":"{}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Ты в разделе Обзор на платформе Yrys Academy."}}]}`))
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
	if response.Message.Content != "Ты в разделе Обзор на платформе Yrys Academy." {
		t.Fatal("unexpected answer")
	}
}
