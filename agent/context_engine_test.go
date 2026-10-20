package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestThreeLayerContextEngineDehydratesOnlyHistoricalPayloads(t *testing.T) {
	rawRAG := `{"ok":true,"found":true,"items":[{"documentId":"d1","title":"退款规则","content":"` + strings.Repeat("原文 ", 200) + `"}]}`
	rawProducts := `{"ok":true,"items":[{"id":7,"productName":"键盘","productNum":3,"productImage":"long","productUrl":"long"}]}`
	messages := []Message{{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "查询订单"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "search_knowledge"}}},
		{Role: "tool", Name: "search_knowledge", ToolCallID: "c1", Content: rawRAG},
		{Role: "assistant", Content: "退款规则已说明"},
		{Role: "user", Content: "查询低库存商品"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c2", Name: "search_products"}}},
		{Role: "tool", Name: "search_products", ToolCallID: "c2", Content: rawProducts},
	}
	state := SessionState{ActiveOrderIDs: []int64{18}, ActiveProductIDs: []int64{7}}
	window := assembleContext(messages, state, StrategyOursFull)
	encoded, _ := json.Marshal(window)
	if strings.Contains(string(encoded), "原文 原文") {
		t.Fatal("historical RAG payload was not dehydrated")
	}
	if !strings.Contains(string(encoded), "long") {
		t.Fatal("latest tool payload was unexpectedly dehydrated")
	}
	if len(window) < 2 || window[1].Name != "session_state" {
		t.Fatalf("state anchor missing: %+v", window)
	}
	if !validToolPairs(window) {
		t.Fatal("context contains an orphaned tool message")
	}
}

func TestSafeSlidingWindowAlignsAtUserBoundaryAndSummarizes(t *testing.T) {
	messages := []Message{{Role: "system", Content: systemPrompt}}
	for i := 0; i < 9; i++ {
		id := "call-" + string(rune('a'+i))
		messages = append(messages,
			Message{Role: "user", Content: "查询订单" + string(rune('0'+i))},
			Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Name: "get_order_status", Arguments: mustJSON(map[string]any{"order_id": i + 1})}}},
			Message{Role: "tool", Name: "get_order_status", ToolCallID: id, Content: `{"ok":true,"orderId":18,"statusText":"待处理"}`},
			Message{Role: "assistant", Content: "订单状态已说明"},
		)
	}
	window := assembleContext(messages, SessionState{ActiveOrderIDs: []int64{18}}, StrategyOursFull)
	if len(window) >= len(messages) {
		t.Fatalf("history was not compacted: %d/%d", len(window), len(messages))
	}
	if !validToolPairs(window) {
		t.Fatal("window split a tool call pair")
	}
	foundSummary := false
	for _, message := range window {
		if message.Name == "context_summary" {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatal("early turns were not represented by a structured summary")
	}
}

func TestSessionStateAnchorRedactsConfirmationCapability(t *testing.T) {
	message := sessionStateMessage(SessionState{PendingCancel: ConfirmTokenInfo{OrderID: 18, Token: "secret-token", TokenStatus: "pending", RemainingSeconds: 120}})
	if !strings.Contains(message.Content, "[当前会话状态锚点]") {
		t.Fatal("state anchor marker missing")
	}
	if strings.Contains(message.Content, "secret-token") {
		t.Fatal("confirmation token leaked into model context")
	}
	if !strings.Contains(message.Content, "pending") || !strings.Contains(message.Content, "120") {
		t.Fatal("pending token metadata missing from state anchor")
	}
}
