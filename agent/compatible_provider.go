package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type CompatibleConfig struct {
	Name    string
	APIKey  string
	BaseURL string
	Model   string
}

type CompatibleProvider struct {
	config CompatibleConfig
	client *http.Client
}

func NewCompatibleProvider(config CompatibleConfig) *CompatibleProvider {
	return &CompatibleProvider{config: config, client: &http.Client{Timeout: 35 * time.Second}}
}

func (p *CompatibleProvider) Name() string  { return p.config.Name }
func (p *CompatibleProvider) Model() string { return p.config.Model }

type compatibleMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content,omitempty"`
	Name       string               `json:"name,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	ToolCalls  []compatibleToolCall `json:"tool_calls,omitempty"`
}

type compatibleToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function compatibleToolFunction `json:"function"`
}

type compatibleToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (p *CompatibleProvider) Complete(ctx context.Context, input CompletionRequest) (Completion, error) {
	messages := make([]compatibleMessage, 0, len(input.Messages))
	for _, message := range input.Messages {
		converted := compatibleMessage{Role: message.Role, Content: message.Content, Name: message.Name, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			converted.ToolCalls = append(converted.ToolCalls, compatibleToolCall{ID: call.ID, Type: "function", Function: compatibleToolFunction{Name: call.Name, Arguments: string(call.Arguments)}})
		}
		messages = append(messages, converted)
	}
	tools := make([]map[string]any, 0, len(input.Tools))
	for _, tool := range input.Tools {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
			"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters, "strict": true,
		}})
	}
	body, err := json.Marshal(map[string]any{
		"model": p.config.Model, "messages": messages, "tools": tools,
		"tool_choice": "auto", "parallel_tool_calls": false, "temperature": 0.2,
	})
	if err != nil {
		return Completion{}, err
	}
	endpoint := strings.TrimRight(p.config.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Completion{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return Completion{}, fmt.Errorf("AI provider request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Completion{}, fmt.Errorf("AI provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var output struct {
		Choices []struct {
			Message compatibleMessage `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&output); err != nil {
		return Completion{}, fmt.Errorf("decode AI provider response: %w", err)
	}
	if len(output.Choices) == 0 {
		return Completion{}, fmt.Errorf("AI provider returned no choices")
	}
	result := Message{Role: "assistant", Content: output.Choices[0].Message.Content}
	for _, call := range output.Choices[0].Message.ToolCalls {
		arguments := json.RawMessage(call.Function.Arguments)
		if !json.Valid(arguments) {
			return Completion{}, fmt.Errorf("AI provider returned invalid tool arguments for %s", call.Function.Name)
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	return Completion{Message: result, Usage: Usage{InputTokens: output.Usage.PromptTokens, OutputTokens: output.Usage.CompletionTokens, TotalTokens: output.Usage.TotalTokens}}, nil
}
