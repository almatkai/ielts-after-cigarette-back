package assistant

type ChatRole string

const (
	RoleUser      ChatRole = "user"
	RoleAssistant ChatRole = "assistant"
	RoleSystem    ChatRole = "system"
	RoleTool      ChatRole = "tool"
)

type ChatMessage struct {
	Role       ChatRole `json:"role"`
	Content    string   `json:"content"`
	ToolCallID string   `json:"toolCallId,omitempty"`
	Name       string   `json:"name,omitempty"`
}

type PageContext struct {
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
}

type ChatRequest struct {
	Messages    []ChatMessage `json:"messages"`
	PageContext *PageContext  `json:"pageContext,omitempty"`
}

type ToolCallInfo struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type ChatResponse struct {
	Message   ChatMessage    `json:"message"`
	ToolCalls []ToolCallInfo `json:"toolCalls,omitempty"`
}
