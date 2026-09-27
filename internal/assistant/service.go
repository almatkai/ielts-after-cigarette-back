package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

var (
	ErrAIUnavailable = errors.New("ai service is not configured")
	ErrAIChatFailed  = errors.New("ai chat completion failed")
)

const yukiSystemPrompt = `You are Yuki (Юки), an expert, friendly, and supportive IELTS mentor and tutor for the IELTS exam preparation platform "IELTS After Cigarette".
Your mission is to help students achieve high Band scores (7.0–9.0) across all four modules: Reading, Listening, Writing, and Speaking.

Character & Tone:
- You are a wise and encouraging arctic fox mentor named Yuki (Юки) 🦊.
- Be pedagogical, structured, clear, and inspiring.
- Reply in Russian by default (or the language the student addresses you in), but keep IELTS terms, band descriptors, instructions, examples, quotes, and vocabulary phrases in English.
- Use clean Markdown formatting: headings, bullet points, bold key terms, and blockquotes for quotes.
- Keep responses well-paced and readable in a chat messenger: when analyzing a page with many items or mistakes, highlight the top 3–5 most critical findings and actionable recommendations first, then ask the student which specific question or skill they want to break down next. Avoid overwhelming text dumps in a single reply.

Tools:
- You have access to the tool 'read_page_content'.
- When the student asks about the current page, asks what is on screen, requests explanations for a question/passage/mistake/writing topic currently viewed, or when you need the context of the user's active screen, ALWAYS call 'read_page_content'.
- The tool returns the page content structured in README / Markdown format.
- Use that content to provide precise, accurate, and tailored IELTS guidance.`

var assistantTools = []map[string]any{
	{
		"type": "function",
		"function": map[string]any{
			"name":        "read_page_content",
			"description": "Reads the full textual content and structure of the current webpage that the student is viewing, formatted as clean README / Markdown. Call this tool whenever the student asks about what's on their page, questions, text, reading passage, writing prompt, audio transcript, mistakes, or asks you to read or review the current page.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":        "string",
						"description": "Optional brief reason for reading the page.",
					},
				},
			},
		},
	},
}

type openAIToolCallItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIMessage struct {
	Role       string               `json:"role"`
	Content    *string              `json:"content"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	Name       string               `json:"name,omitempty"`
	ToolCalls  []openAIToolCallItem `json:"tool_calls,omitempty"`
}

type Service struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

func NewService(endpoint, apiKey, model string, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Service{
		endpoint: strings.TrimSpace(endpoint),
		apiKey:   strings.TrimSpace(apiKey),
		model:    strings.TrimSpace(model),
		client:   client,
	}
}

func (s *Service) IsAvailable() bool {
	return s.endpoint != "" && s.apiKey != "" && s.model != ""
}

func (s *Service) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if !s.IsAvailable() {
		return ChatResponse{}, ErrAIUnavailable
	}

	// Prepare initial message history
	messages := make([]openAIMessage, 0, len(req.Messages)+3)

	sysContent := yukiSystemPrompt
	messages = append(messages, openAIMessage{
		Role:    string(RoleSystem),
		Content: &sysContent,
	})

	// Add up to last 20 messages to keep context concise
	startIdx := 0
	if len(req.Messages) > 20 {
		startIdx = len(req.Messages) - 20
	}
	for _, m := range req.Messages[startIdx:] {
		text := strings.TrimSpace(m.Content)
		if text == "" && m.Role != RoleTool {
			continue
		}
		role := string(m.Role)
		if role == "" {
			role = string(RoleUser)
		}
		contentStr := text
		messages = append(messages, openAIMessage{
			Role:       role,
			Content:    &contentStr,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}

	var recordedToolCalls []ToolCallInfo

	// First completion turn
	respMsg, err := s.callCompletions(ctx, messages, assistantTools)
	if err != nil {
		return ChatResponse{}, err
	}

	// Check if tool calls were requested by the model
	if len(respMsg.ToolCalls) > 0 {
		messages = append(messages, *respMsg)

		for _, toolCall := range respMsg.ToolCalls {
			recordedToolCalls = append(recordedToolCalls, ToolCallInfo{
				ID:        toolCall.ID,
				Name:      toolCall.Function.Name,
				Arguments: toolCall.Function.Arguments,
			})

			var toolResultContent string
			if toolCall.Function.Name == "read_page_content" {
				toolResultContent = formatPageContextAsReadme(req.PageContext)
			} else {
				toolResultContent = fmt.Sprintf("Unknown tool %q", toolCall.Function.Name)
			}

			messages = append(messages, openAIMessage{
				Role:       string(RoleTool),
				ToolCallID: toolCall.ID,
				Name:       toolCall.Function.Name,
				Content:    &toolResultContent,
			})
		}

		// Second completion turn with tool results
		secondRespMsg, err := s.callCompletions(ctx, messages, assistantTools)
		if err != nil {
			return ChatResponse{}, err
		}
		respMsg = secondRespMsg
	}

	var finalContent string
	if respMsg.Content != nil {
		finalContent = strings.TrimSpace(*respMsg.Content)
	}
	if finalContent == "" {
		finalContent = "Я внимательно изучил контекст. Чем конкретно я могу тебе помочь по текущему заданию?"
	}

	return ChatResponse{
		Message: ChatMessage{
			Role:    RoleAssistant,
			Content: finalContent,
		},
		ToolCalls: recordedToolCalls,
	}, nil
}

func (s *Service) callCompletions(ctx context.Context, messages []openAIMessage, tools []map[string]any) (*openAIMessage, error) {
	payload := map[string]any{
		"model":       s.model,
		"messages":    messages,
		"temperature": 0.4,
		"max_tokens":  4096,
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal request: %v", ErrAIChatFailed, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: create request: %v", ErrAIChatFailed, err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Title", "IELTS After Cigarette Assistant")

	res, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: http request failed: %v", ErrAIChatFailed, err)
	}
	defer res.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrAIChatFailed, err)
	}

	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		slog.Default().ErrorContext(ctx, "ai completion error", "status", res.StatusCode, "body", string(respBytes))
		return nil, fmt.Errorf("%w: provider status %d", ErrAIChatFailed, res.StatusCode)
	}

	var completion struct {
		Choices []struct {
			Message      openAIMessage `json:"message"`
			FinishReason string        `json:"finish_reason"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBytes, &completion); err != nil || len(completion.Choices) == 0 {
		return nil, fmt.Errorf("%w: invalid response json or empty choices", ErrAIChatFailed)
	}

	msg := completion.Choices[0].Message
	if completion.Choices[0].FinishReason == "length" && msg.Content != nil {
		appended := *msg.Content + "\n\n*(Ответ был приостановлен по лимиту длины. Напиши «продолжи», если хочешь разобрать остальные детали)*"
		msg.Content = &appended
	}
	return &msg, nil
}

func formatPageContextAsReadme(pc *PageContext) string {
	if pc == nil || strings.TrimSpace(pc.Content) == "" {
		return "# Page Content (README format)\n\n*No readable content was detected on the current page.*"
	}

	var sb strings.Builder
	sb.WriteString("# Current Page: ")
	if pc.Title != "" {
		sb.WriteString(pc.Title)
	} else {
		sb.WriteString("IELTS Platform Page")
	}
	sb.WriteString("\n")

	if pc.URL != "" {
		sb.WriteString(fmt.Sprintf("**URL**: `%s`\n\n", pc.URL))
	} else {
		sb.WriteString("\n")
	}

	sb.WriteString(strings.TrimSpace(pc.Content))
	return sb.String()
}
