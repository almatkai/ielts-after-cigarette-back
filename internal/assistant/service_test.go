package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssistantServiceRequiresConfiguration(t *testing.T) {
	svc := NewService("", "", "", nil)
	_, err := svc.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "Hello"}},
	})
	if err != ErrAIUnavailable {
		t.Fatalf("expected ErrAIUnavailable, got %v", err)
	}
}

func TestAssistantServiceDirectChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "Привет! Я Юки.",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := NewService(server.URL, "test-key", "qwen3-8", server.Client())
	resp, err := svc.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "Привет"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "Привет! Я Юки." {
		t.Fatalf("unexpected content: %s", resp.Message.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("expected 0 tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestAssistantServiceToolCallExecution(t *testing.T) {
	turn := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		w.Header().Set("Content-Type", "application/json")
		if turn == 1 {
			// First turn: assistant requests read_page_content
			resp := map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"role": "assistant",
							"tool_calls": []map[string]any{
								{
									"id":   "call_abc123",
									"type": "function",
									"function": map[string]any{
										"name":      "read_page_content",
										"arguments": "{}",
									},
								},
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Second turn: assistant receives tool output and answers
		var payload struct {
			Messages []struct {
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
				Content    string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)

		foundToolMsg := false
		for _, m := range payload.Messages {
			if m.Role == "tool" && m.ToolCallID == "call_abc123" {
				foundToolMsg = true
			}
		}
		if !foundToolMsg {
			http.Error(w, "missing tool response in second turn", http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "На вашей странице Reading Test 1.",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := NewService(server.URL, "test-key", "qwen3-8", server.Client())
	resp, err := svc.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "Что у меня на странице?"}},
		PageContext: &PageContext{
			Title:   "Reading Practice",
			URL:     "/dashboard/reading/1",
			Content: "## Passage 1\nSome text...",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "На вашей странице Reading Test 1." {
		t.Fatalf("unexpected content: %s", resp.Message.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_page_content" {
		t.Fatalf("expected read_page_content tool call, got %+v", resp.ToolCalls)
	}
}
