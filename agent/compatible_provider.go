package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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

// CheckTokenizerHealth is used by production startup probes. Strict mode must
// not be advertised when the resident tokenizer is unavailable.
func CheckTokenizerHealth(ctx context.Context) error {
	endpoint := strings.TrimSpace(os.Getenv("AGENT_TOKENIZER_URL"))
	if endpoint == "" {
		return fmt.Errorf("AGENT_TOKENIZER_URL is not configured")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 500 * time.Millisecond}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tokenizer health HTTP %d", resp.StatusCode)
	}
	return nil
}

// CountTokens uses a fixed tiktoken encoding for the provider request
// envelope. Set AGENT_TOKEN_ENCODING when the gateway/model requires another
// encoding; cl100k_base is the default compatible encoding.
func (p *CompatibleProvider) CountTokens(input CompletionRequest) (int, error) {
	counts, err := p.CountTokensBatch([]CompletionRequest{input})
	if err != nil {
		return 0, err
	}
	return counts[0], nil
}

// CountTokensBatch amortizes the Python tokenizer startup for large offline evaluations.
func (p *CompatibleProvider) CountTokensBatch(inputs []CompletionRequest) ([]int, error) {
	if len(inputs) == 0 {
		return []int{}, nil
	}
	if endpoint := strings.TrimSpace(os.Getenv("AGENT_TOKENIZER_URL")); endpoint != "" {
		counts := make([]int, len(inputs))
		for i, input := range inputs {
			count, err := p.countTokensHTTP(endpoint, input)
			if err != nil {
				return nil, err
			}
			counts[i] = count
		}
		return counts, nil
	}
	batch := make([]json.RawMessage, len(inputs))
	for i, input := range inputs {
		payload, err := json.Marshal(tokenEnvelope(input))
		if err != nil {
			return nil, err
		}
		batch[i] = payload
	}
	encoding := os.Getenv("AGENT_TOKEN_ENCODING")
	if strings.TrimSpace(encoding) == "" {
		encoding = "cl100k_base"
	}
	// Base64 keeps large UTF-8 JSON payloads intact across Windows process pipes.
	code := "import base64,json,sys,tiktoken; ds=json.loads(base64.b64decode(sys.stdin.read()).decode('utf-8')); e=tiktoken.get_encoding(sys.argv[1]); print(' '.join(str(len(e.encode(json.dumps(d,ensure_ascii=False,separators=(',',':'))))) for d in ds))"
	cmd := exec.Command("python", "-c", code, encoding)
	encoded, _ := json.Marshal(batch)
	b64 := make([]byte, base64.StdEncoding.EncodedLen(len(encoded)))
	base64.StdEncoding.Encode(b64, encoded)
	cmd.Stdin = bytes.NewReader(b64)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tokenizer unavailable: %w: %s", err, strings.TrimSpace(string(output)))
	}
	fields := strings.Fields(string(output))
	if len(fields) != len(inputs) {
		return nil, fmt.Errorf("invalid tokenizer output: got %d counts, want %d", len(fields), len(inputs))
	}
	counts := make([]int, len(fields))
	for i, field := range fields {
		if _, err := fmt.Sscanf(field, "%d", &counts[i]); err != nil {
			return nil, fmt.Errorf("invalid tokenizer output: %w", err)
		}
	}
	return counts, nil
}

// tokenEnvelope uses the same function-call wire representation as Complete.
func tokenEnvelope(input CompletionRequest) map[string]any {
	messages := make([]compatibleMessage, 0, len(input.Messages))
	for _, message := range input.Messages {
		m := compatibleMessage{Role: message.Role, Content: message.Content, Name: message.Name, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, compatibleToolCall{ID: call.ID, Type: "function", Function: compatibleToolFunction{Name: call.Name, Arguments: string(call.Arguments)}})
		}
		messages = append(messages, m)
	}
	definitions := make([]map[string]any, 0, len(input.Tools))
	for _, tool := range input.Tools {
		definitions = append(definitions, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters, "strict": true}})
	}
	return map[string]any{"messages": messages, "tools": definitions}
}

func (p *CompatibleProvider) countTokensHTTP(endpoint string, input CompletionRequest) (int, error) {
	payload := tokenEnvelope(input)
	payload["encoding"] = envOr("AGENT_TOKEN_ENCODING", "cl100k_base")
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/v1/count"
	client := p.client
	if client == nil {
		client = &http.Client{Timeout: 500 * time.Millisecond}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("tokenizer service unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("tokenizer service returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Tokens   int    `json:"tokens"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode tokenizer response: %w", err)
	}
	if result.Tokens < 0 {
		return 0, fmt.Errorf("invalid tokenizer token count")
	}
	return result.Tokens, nil
}

func NewCompatibleProvider(config CompatibleConfig) *CompatibleProvider {
	timeout := 35 * time.Second
	if raw := strings.TrimSpace(os.Getenv("AI_REQUEST_TIMEOUT_MS")); raw != "" {
		if millis, err := time.ParseDuration(raw + "ms"); err == nil && millis > 0 {
			timeout = millis
		}
	}
	return &CompatibleProvider{config: config, client: &http.Client{Timeout: timeout}}
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

func (p *CompatibleProvider) Complete(ctx context.Context, input CompletionRequest) (completion Completion, completeErr error) {
	requestStarted := time.Now()
	lastAttempt := 0
	defer func() {
		completion.NetworkLatencyMS = time.Since(requestStarted).Milliseconds()
		completion.RetryCount = lastAttempt
	}()
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
	payload := map[string]any{
		"model": p.config.Model, "messages": messages, "tools": tools,
		"tool_choice": "auto", "parallel_tool_calls": false, "temperature": 0.2,
	}
	if input.MaxOutputTokens > 0 {
		payload["max_tokens"] = input.MaxOutputTokens
	}
	if input.ResponseFormat != nil {
		payload["response_format"] = input.ResponseFormat
		if len(input.Tools) == 0 {
			delete(payload, "tools")
			delete(payload, "tool_choice")
			delete(payload, "parallel_tool_calls")
		}
	}
	if input.Temperature != nil {
		payload["temperature"] = *input.Temperature
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Completion{}, err
	}
	endpoint := strings.TrimRight(p.config.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	maxRetries := providerMaxRetries()
	for attempt := 0; ; attempt++ {
		lastAttempt = attempt
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return Completion{}, err
		}
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			if attempt < maxRetries && ctx.Err() == nil {
				if err := waitProviderRetry(ctx, attempt); err == nil {
					continue
				}
			}
			return Completion{}, fmt.Errorf("AI provider request failed: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			status := resp.StatusCode
			resp.Body.Close()
			if (status == http.StatusTooManyRequests || status >= 500) && attempt < maxRetries {
				if err := waitProviderRetry(ctx, attempt); err == nil {
					continue
				}
			}
			return Completion{}, fmt.Errorf("AI provider returned HTTP %d", status)
		}
		var output struct {
			Choices []struct {
				Message compatibleMessage `json:"message"`
			} `json:"choices"`
			Usage struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				TotalTokens         int `json:"total_tokens"`
				PromptTokensDetails *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&output)
		resp.Body.Close()
		if decodeErr != nil {
			return Completion{}, fmt.Errorf("decode AI provider response: %w", decodeErr)
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
		usage := Usage{InputTokens: output.Usage.PromptTokens, OutputTokens: output.Usage.CompletionTokens, TotalTokens: output.Usage.TotalTokens}
		if output.Usage.PromptTokensDetails != nil {
			usage.CachedTokens = output.Usage.PromptTokensDetails.CachedTokens
			usage.CacheUsageReported = true
		}
		return Completion{
			Message:          result,
			Usage:            usage,
			NetworkLatencyMS: time.Since(requestStarted).Milliseconds(),
			RetryCount:       attempt,
		}, nil
	}
}

func providerMaxRetries() int {
	value := strings.TrimSpace(os.Getenv("AI_PROVIDER_MAX_RETRIES"))
	if value == "" {
		return 0
	}
	var retries int
	if _, err := fmt.Sscanf(value, "%d", &retries); err != nil || retries < 0 {
		return 0
	}
	if retries > 3 {
		return 3
	}
	return retries
}

func waitProviderRetry(ctx context.Context, attempt int) error {
	delay := 500 * time.Millisecond
	for i := 0; i < attempt; i++ {
		delay *= 2
	}
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
