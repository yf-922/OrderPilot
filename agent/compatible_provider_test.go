package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompatibleProviderToolCallContract(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing bearer key")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"get_order_status","arguments":"{\"order_id\":7}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`))
	}))
	defer server.Close()

	provider := NewCompatibleProvider(CompatibleConfig{Name: "grok", APIKey: "test-key", BaseURL: server.URL, Model: "grok-test"})
	completion, err := provider.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "查询订单 7"}},
		Tools:    toolDefinitions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if received["model"] != "grok-test" || received["parallel_tool_calls"] != false {
		t.Fatalf("unexpected request: %+v", received)
	}
	tools, ok := received["tools"].([]any)
	if !ok || len(tools) != 5 {
		t.Fatalf("tools missing from request: %+v", received["tools"])
	}
	if len(completion.Message.ToolCalls) != 1 || completion.Message.ToolCalls[0].Name != "get_order_status" || completion.Usage.TotalTokens != 14 {
		t.Fatalf("unexpected completion: %+v", completion)
	}
}

func TestCompatibleProviderDoesNotExposeUpstreamErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key sk-sensitive-value"}`))
	}))
	defer server.Close()
	provider := NewCompatibleProvider(CompatibleConfig{Name: "test", APIKey: "sk-sensitive-value", BaseURL: server.URL, Model: "test"})
	_, err := provider.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "sk-sensitive-value") {
		t.Fatalf("upstream error was not safely redacted: %v", err)
	}
}

func TestProviderEnvironmentDefaultsAndXAIKeyAlias(t *testing.T) {
	t.Setenv("AI_PROVIDER", "")
	t.Setenv("AI_API_KEY", "")
	t.Setenv("XAI_API_KEY", "")
	if provider := NewProviderFromEnv(); provider.Name() != "local" {
		t.Fatalf("provider=%s want local", provider.Name())
	}
	t.Setenv("XAI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "grok-custom")
	provider := NewProviderFromEnv()
	if provider.Name() != "grok" || provider.Model() != "grok-custom" {
		t.Fatalf("unexpected provider: %s/%s", provider.Name(), provider.Model())
	}
}
