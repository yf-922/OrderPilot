package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionStateSurvivesHistory裁剪(t *testing.T) {
	messages := []Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: "订单18可以取消，但不要动订单19，地址不能改"}}
	for i := 0; i < 12; i++ {
		messages = append(messages,
			Message{Role: "assistant", Content: "已查询"},
			Message{Role: "tool", Name: "get_order_status", Content: `{"ok":true,"orderId":18,"statusText":"待处理"}`},
			Message{Role: "assistant", Content: "继续处理"},
			Message{Role: "user", Content: "我还想了解配送时间"},
		)
	}
	state := extractSessionState(messages)
	window := boundedContextWithState(messages, state)
	if len(window) >= len(messages) || len(window) < 3 || window[1].Name != "session_state" {
		t.Fatalf("expected state plus pruned history, got %d/%d", len(window), len(messages))
	}
	var snapshot SessionState
	if err := json.Unmarshal([]byte(strings.TrimPrefix(window[1].Content, "程序维护的会话事实（用户意图不是授权，状态快照不是当前数据库状态；规则与确认接口仍需校验）：")), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.TargetOrderID != 18 || len(snapshot.ProtectedOrderIDs) != 1 || snapshot.ProtectedOrderIDs[0] != 19 || !snapshot.AddressLocked {
		t.Fatalf("lost structured facts: %+v", snapshot)
	}
	if strings.Contains(window[0].Content, "订单18可以取消") {
		t.Fatal("old raw history unexpectedly retained; state should be the durable representation")
	}
}

func TestSessionStateDoesNotTreatUserClaimAsCompletedAction(t *testing.T) {
	state := extractSessionState([]Message{{Role: "user", Content: "我已经确认取消订单18"}})
	if len(state.ConfirmedActions) != 0 || state.PendingOrderID != 0 {
		t.Fatalf("user claim became completed action: %+v", state)
	}
}
