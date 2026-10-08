package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/almatkai/ielts-after-cigarette-back/internal/aiproviders"
	"github.com/google/uuid"
)

type StreamEvent struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	Response *ChatResponse `json:"response,omitempty"`
}
type Emit func(StreamEvent) error

func initialMessages(req ChatRequest) []openAIMessage {
	system := yukiSystemPrompt
	messages := []openAIMessage{{Role: "system", Content: &system}}
	start := 0
	if len(req.Messages) > 20 {
		start = len(req.Messages) - 20
	}
	for _, message := range req.Messages[start:] {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		role := string(message.Role)
		if role == "" {
			role = "user"
		}
		// Clients never supply system instructions or tool output. Page data is
		// supplied by the server-owned read_page_content tool only.
		if role != "user" && role != "assistant" {
			continue
		}
		messages = append(messages, openAIMessage{Role: role, Content: &text})
	}
	return messages
}

func (s *Service) Stream(ctx context.Context, req ChatRequest, emit Emit) (ChatResponse, error) {
	if s.providers != nil {
		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		gate := newStreamGate(emit, cancel)
		defer gate.close()
		type candidate struct {
			response ChatResponse
			id       uuid.UUID
		}
		result, err := aiproviders.Execute(streamCtx, s.providers, "assistant", func(callCtx context.Context, p aiproviders.Provider) (candidate, error) {
			copy := NewService(p.Endpoint, p.APIKey, p.Model, s.providers.Client(p))
			response, err := copy.Stream(callCtx, req, gate.emitter(callCtx, p.ID))
			if err != nil {
				gate.failed(p.ID)
			}
			return candidate{response: response, id: p.ID}, err
		})
		if err != nil {
			return ChatResponse{}, err
		}
		if err := gate.commit(result.id); err != nil {
			return ChatResponse{}, err
		}
		return result.response, nil
	}
	if !s.IsAvailable() {
		return ChatResponse{}, ErrAIUnavailable
	}
	messages := initialMessages(req)
	calls := []ToolCallInfo{}
	for round := 0; round < 3; round++ {
		message, err := s.streamCompletion(ctx, messages, emit)
		if err != nil {
			return ChatResponse{}, err
		}
		if len(message.ToolCalls) == 0 {
			if message.Content == nil || strings.TrimSpace(*message.Content) == "" {
				return ChatResponse{}, ErrAIChatFailed
			}
			return ChatResponse{Message: ChatMessage{Role: RoleAssistant, Content: strings.TrimSpace(*message.Content)}, ToolCalls: calls}, nil
		}
		messages = append(messages, message)
		for _, call := range message.ToolCalls {
			if call.ID == "" || call.Function.Name != "read_page_content" {
				return ChatResponse{}, ErrAIChatFailed
			}
			content := formatPageContextAsReadme(req.PageContext)
			messages = append(messages, openAIMessage{Role: "tool", Content: &content, ToolCallID: call.ID, Name: call.Function.Name})
			calls = append(calls, ToolCallInfo{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
		}
		// A tool preamble is not the final answer. The UI removes it without an
		// error banner before streaming the answer grounded in the page context.
		if err := emit(StreamEvent{Type: "reset"}); err != nil {
			return ChatResponse{}, err
		}
	}
	return ChatResponse{}, ErrAIChatFailed
}

func (s *Service) streamCompletion(ctx context.Context, messages []openAIMessage, emit Emit) (openAIMessage, error) {
	body, err := json.Marshal(map[string]any{"model": s.model, "messages": messages, "tools": assistantTools, "stream": true, "temperature": 0.4, "max_tokens": 4096})
	if err != nil {
		return openAIMessage{}, ErrAIChatFailed
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.endpoint, bytes.NewReader(body))
	if err != nil {
		return openAIMessage{}, ErrAIChatFailed
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("X-Title", "Daiyndyq IELTS Assistant")
	response, err := s.client.Do(req)
	if err != nil {
		return openAIMessage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return openAIMessage{}, &aiproviders.HTTPError{Status: response.StatusCode}
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return openAIMessage{}, ErrAIChatFailed
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 2<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var text strings.Builder
	calls := map[int]*openAIToolCallItem{}
	finished, done := false, false
	var data []string
	consume := func() error {
		if len(data) == 0 {
			return nil
		}
		raw := strings.Join(data, "\n")
		data = nil
		if raw == "[DONE]" {
			done = true
			return nil
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content   *string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			return ErrAIChatFailed
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return ErrAIChatFailed
		}
		if len(chunk.Choices) == 0 {
			return nil
		} // usage-only chunk
		choice := chunk.Choices[0]
		if choice.Delta.Content != nil {
			if *choice.Delta.Content != "" {
				aiproviders.MarkFirstToken(ctx)
			}
			if text.Len()+len(*choice.Delta.Content) > 128<<10 {
				return ErrAIChatFailed
			}
			text.WriteString(*choice.Delta.Content)
			if err := emit(StreamEvent{Type: "delta", Text: *choice.Delta.Content}); err != nil {
				return err
			}
		}
		for _, delta := range choice.Delta.ToolCalls {
			if delta.Index < 0 || delta.Index > 7 {
				return ErrAIChatFailed
			}
			call := calls[delta.Index]
			if call == nil {
				call = &openAIToolCallItem{Type: "function"}
				calls[delta.Index] = call
			}
			if delta.ID != "" {
				call.ID = delta.ID
			}
			call.Function.Name += delta.Function.Name
			call.Function.Arguments += delta.Function.Arguments
			if len(call.Function.Arguments) > 16<<10 || len(call.Function.Name) > 256 {
				return ErrAIChatFailed
			}
		}
		if choice.FinishReason != nil {
			if *choice.FinishReason != "stop" && *choice.FinishReason != "tool_calls" {
				return ErrAIChatFailed
			}
			finished = true
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := consume(); err != nil {
				return openAIMessage{}, err
			}
			if done {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return openAIMessage{}, err
	}
	if err := consume(); err != nil {
		return openAIMessage{}, err
	}
	// EOF after partial content is never success, even with HTTP 200.
	if !done || !finished {
		return openAIMessage{}, ErrAIChatFailed
	}
	content := text.String()
	result := openAIMessage{Role: "assistant", Content: &content}
	indices := make([]int, 0, len(calls))
	for index := range calls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		result.ToolCalls = append(result.ToolCalls, *calls[index])
	}
	return result, nil
}

func validRequest(req ChatRequest) bool {
	if len(req.Messages) == 0 || len(req.Messages) > 100 {
		return false
	}
	for _, m := range req.Messages {
		if (m.Role != RoleUser && m.Role != RoleAssistant && m.Role != "") || len(m.Content) > 128<<10 || (m.Role != RoleAssistant && len(m.Content) > 32000) {
			return false
		}
	}
	if req.PageContext != nil && (len(req.PageContext.Content) > 26000 || len(req.PageContext.Title) > 1000 || len(req.PageContext.URL) > 2000) {
		return false
	}
	return true
}
