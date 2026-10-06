package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Replay measures the same conversation at three stages. It does not claim
// that a live model remembers facts removed by the history window.
func TestRealisticCustomerConversationContext(t *testing.T) {
	turns := []struct {
		user, tool, answer string
		args, result       any
	}{
		{"想买一个键盘，看看有哪些商品", "search_products", "有两款键盘，库存分别为12和4。", map[string]any{"keyword": "键盘", "low_stock_only": false}, map[string]any{"ok": true, "count": 2, "items": []map[string]any{
			{"id": 1, "productName": "无线键盘", "productNum": 12, "productImage": "https://shop.example/assets/keyboard-wireless.jpg"},
			{"id": 2, "productName": "机械键盘", "productNum": 4, "productImage": "https://shop.example/assets/keyboard-mechanical.jpg"},
		}}},
		{"只看库存不足的键盘", "search_products", "机械键盘库存为4。", map[string]any{"keyword": "键盘", "low_stock_only": true}, map[string]any{"ok": true, "count": 1, "items": []map[string]any{{"id": 2, "productName": "机械键盘", "productNum": 4, "productImage": "https://shop.example/assets/keyboard-mechanical.jpg"}}}},
		{"配送时间受什么影响？", "search_knowledge", "配送时效受库存、地区和承运商影响。", map[string]any{"query": "配送时间"}, map[string]any{"ok": true, "found": true, "items": []KnowledgeChunk{{DocumentID: "delivery-policy-v1", Title: "配送时效说明", Content: defaultKnowledgeDocuments()[2].Content}}}},
		{"看看我的最近订单", "list_my_orders", "你有订单18和19。", map[string]any{"page": 1, "page_size": 10}, map[string]any{"ok": true, "total": 2, "items": []map[string]any{{"id": 18, "productId": 2, "quantity": 1, "orderStatus": 0}, {"id": 19, "productId": 1, "quantity": 1, "orderStatus": 1}}}},
		{"查一下订单18现在是什么状态", "get_order_status", "订单18待处理，可以申请取消。", map[string]any{"order_id": 18}, map[string]any{"ok": true, "orderId": 18, "status": 0, "statusText": "待处理", "canCancel": true}},
		{"先别取消，退款多久能到账？", "search_knowledge", "到账时间取决于支付渠道，不能保证立即到账。", map[string]any{"query": "退款到账时间"}, map[string]any{"ok": true, "found": true, "items": []KnowledgeChunk{{DocumentID: "refund-policy-v1", Title: "退款处理说明", Content: defaultKnowledgeDocuments()[1].Content}}}},
		{"确认是订单18，不要动19，帮我申请取消", "prepare_cancel_order", "订单18可以取消，请点击确认按钮。", map[string]any{"order_id": 18}, map[string]any{"ok": true, "orderId": 18, "requiresConfirmation": true}},
		{"我还没点击确认，18已经取消了吗？", "get_order_status", "订单18仍待处理，尚未执行取消。", map[string]any{"order_id": 18}, map[string]any{"ok": true, "orderId": 18, "status": 0, "statusText": "待处理", "canCancel": true}},
	}
	raw := []Message{{Role: "system", Content: systemPrompt}}
	compact := append([]Message(nil), raw...)
	for i, turn := range turns {
		call := ToolCall{ID: fmt.Sprintf("replay-%d", i), Name: turn.tool, Arguments: mustJSON(turn.args)}
		prefix := []Message{{Role: "user", Content: turn.user}, {Role: "assistant", ToolCalls: []ToolCall{call}}}
		raw = append(raw, prefix...)
		compact = append(compact, prefix...)
		result := mustJSON(turn.result)
		raw = append(raw, Message{Role: "tool", Name: turn.tool, ToolCallID: call.ID, Content: string(result)}, Message{Role: "assistant", Content: turn.answer})
		compact = append(compact, Message{Role: "tool", Name: turn.tool, ToolCallID: call.ID, Content: compactToolResult(turn.tool, result)}, Message{Role: "assistant", Content: turn.answer})
	}
	window := boundedContext(compact)
	size := func(messages []Message) int {
		// Same JSON envelope for all stages, including the shared tool schemas.
		encoded, err := json.Marshal(map[string]any{"messages": messages, "tools": toolDefinitions()})
		if err != nil {
			t.Fatal(err)
		}
		return len(encoded)
	}
	rawBytes, compactBytes, windowBytes := size(raw), size(compact), size(window)
	t.Logf("REALISTIC_CONTEXT turns=%d rawBytes=%d fieldCompactedBytes=%d windowedBytes=%d fieldReduction=%.2f%% combinedReduction=%.2f%% rawMessages=%d retainedMessages=%d", len(turns), rawBytes, compactBytes, windowBytes, 100*float64(rawBytes-compactBytes)/float64(rawBytes), 100*float64(rawBytes-windowBytes)/float64(rawBytes), len(raw), len(window))
	if compactBytes >= rawBytes || windowBytes >= compactBytes {
		t.Fatal("expected field reduction and history reduction")
	}
	if window[0].Content != systemPrompt {
		t.Fatal("lost system prompt")
	}
	for i, message := range window {
		if message.Role == "tool" {
			if i == 0 || len(window[i-1].ToolCalls) != 1 || window[i-1].ToolCalls[0].ID != message.ToolCallID {
				t.Fatal("orphan tool result")
			}
		}
	}
	encoded, _ := json.Marshal(window)
	for _, fact := range []string{"不要动19", "requiresConfirmation", "尚未执行取消"} {
		if !strings.Contains(string(encoded), fact) {
			t.Fatalf("lost recent critical fact: %s", fact)
		}
	}
}
