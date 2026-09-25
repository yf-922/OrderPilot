package agent

import "encoding/json"

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type CompletionRequest struct {
	Messages []Message
	Tools    []ToolDefinition
}

type Usage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
	TotalTokens  int `json:"totalTokens,omitempty"`
}

type Completion struct {
	Message Message
	Usage   Usage
}

type ToolTrace struct {
	Name         string          `json:"name"`
	Arguments    json.RawMessage `json:"arguments"`
	Result       json.RawMessage `json:"result"`
	DurationMS   int64           `json:"durationMs"`
	Success      bool            `json:"success"`
	FailureClass string          `json:"failureClass,omitempty"`
	Attempts     int             `json:"attempts,omitempty"`
}

type Handoff struct {
	Reason         string   `json:"reason"`
	Goal           string   `json:"goal"`
	CompletedTools []string `json:"completedTools"`
	Failure        string   `json:"failure"`
}

type Confirmation struct {
	Token     string `json:"token"`
	Action    string `json:"action"`
	OrderID   int64  `json:"orderId"`
	ExpiresAt string `json:"expiresAt"`
}

type ChatRequest struct {
	ConversationID string `json:"conversationId"`
	Message        string `json:"message"`
}

type ChatResponse struct {
	ConversationID string        `json:"conversationId"`
	Message        string        `json:"message"`
	Provider       string        `json:"provider"`
	Model          string        `json:"model"`
	ToolCalls      []ToolTrace   `json:"toolCalls"`
	Confirmation   *Confirmation `json:"confirmation,omitempty"`
	Usage          Usage         `json:"usage,omitempty"`
	Handoff        *Handoff      `json:"handoff,omitempty"`
}

type ConfirmRequest struct {
	ConversationID string `json:"conversationId"`
	Token          string `json:"token"`
}

type Status struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Mode     string `json:"mode"`
}

type Metrics struct {
	Requests           int64   `json:"requests"`
	SuccessfulRequests int64   `json:"successfulRequests"`
	FailedRequests     int64   `json:"failedRequests"`
	ToolCalls          int64   `json:"toolCalls"`
	ToolFailures       int64   `json:"toolFailures"`
	Confirmations      int64   `json:"confirmations"`
	TotalTokens        int64   `json:"totalTokens"`
	AverageLatencyMS   float64 `json:"averageLatencyMs"`
}
