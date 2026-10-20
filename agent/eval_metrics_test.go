package agent

import "testing"

func TestCore8StratifiedSample(t *testing.T) {
	cases := make([]longContextCase, 0, 25)
	scenarios := []string{
		"跨轮次订单指代", "跨轮次订单指代", "跨轮次订单指代",
		"巨型JSON工具载荷", "巨型JSON工具载荷", "长程约束记忆",
		"工具错误重试", "越权订单安全拦截",
	}
	for i := 0; i < 25; i++ {
		scenario := "普通政策问答"
		if i < len(scenarios) {
			scenario = scenarios[i]
		}
		cases = append(cases, longContextCase{ID: string(rune('A' + i)), Scenario: scenario})
	}
	selected := Core8StratifiedSample(cases)
	if len(selected) != 8 {
		t.Fatalf("want 8 stratified cases, got %d", len(selected))
	}
}

func TestMatchToolArguments(t *testing.T) {
	if !MatchToolArguments(map[string]any{"order_id": float64(18), "query": "退款 到账"}, map[string]any{"order_id": float64(18), "query": "请查询退款到账时间"}) {
		t.Fatal("expected exact ID and keyword query match")
	}
	if MatchToolArguments(map[string]any{"order_id": float64(18)}, map[string]any{"order_id": float64(19)}) {
		t.Fatal("different order IDs must not match")
	}
}

func TestTeacherForcedHistoryUsesGoldPrefix(t *testing.T) {
	turns := []longContextTurn{{User: "查订单", ExpectedTool: "list_my_orders"}, {User: "查规则", ExpectedTool: "search_knowledge"}}
	messages := TeacherForcedHistory("system", turns, 1, func(longContextTurn, int) string { return `{"ok":true}` })
	if len(messages) != 5 || messages[2].ToolCalls[0].Name != "list_my_orders" {
		t.Fatalf("unexpected teacher-forced history: %+v", messages)
	}
}
