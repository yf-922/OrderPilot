package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"product/datamodels"
)

// expandedEvalCase is generated from a fixed seed so the 100-case corpus is
// reproducible without hand-maintaining a large, repetitive JSON fixture.
type expandedEvalCase struct {
	ID      string
	Tier    int
	Turns   []longContextTurn
	Orders  []int64
	Protect int64
}

type expandedMetric struct {
	Strategy       ContextStrategy
	Tier           int
	Cases          int
	AvgTokens      float64
	PeakTokens     int
	TokenReduction float64
	PairIntegrity  float64
	StateRetention float64
	PairCI         string
	StateCI        string
}

func expandedContextCases() []expandedEvalCase {
	lengths := []int{12, 16, 20}
	templates := []struct {
		kind    string
		orders  []int64
		protect int64
	}{
		{"order_coref", []int64{18, 19, 24}, 19},
		{"cancel_confirm", []int64{18, 19, 27}, 19},
		{"rag_pressure", []int64{21, 22, 31}, 0},
		{"multi_tool", []int64{18, 22, 35}, 22},
		{"safety_failure", []int64{19, 26, 40}, 26},
	}
	cases := make([]expandedEvalCase, 0, 100)
	for i := 0; i < 100; i++ {
		template := templates[i%len(templates)]
		length := lengths[(i/len(templates))%len(lengths)]
		turns := make([]longContextTurn, 0, length)
		for turn := 0; turn < length; turn++ {
			user, tool := expandedTurnText(template.kind, turn, i)
			turns = append(turns, longContextTurn{User: user, ExpectedTool: tool})
		}
		cancelTurn := length - 3
		if template.kind == "cancel_confirm" || template.kind == "order_coref" || template.kind == "multi_tool" {
			turns[cancelTurn] = longContextTurn{User: "把最开始查到的待处理订单取消，先生成确认", ExpectedTool: "prepare_cancel_order", ExpectedOrderID: template.orders[0]}
			turns[length-1] = longContextTurn{User: "确认取消", ExpectedTool: "confirm_cancel", ExpectedOrderID: template.orders[0]}
		}
		cases = append(cases, expandedEvalCase{ID: fmt.Sprintf("L%03d", i+1), Tier: length, Turns: turns, Orders: template.orders, Protect: template.protect})
	}
	return cases
}

func expandedTurnText(kind string, turn, seed int) (string, string) {
	if turn == 0 {
		return "查询我的订单列表和最近购买记录", "list_my_orders"
	}
	if turn%5 == 1 {
		return fmt.Sprintf("第%d轮咨询退款和退换货政策", turn), "search_knowledge"
	}
	if turn%5 == 2 {
		return fmt.Sprintf("第%d轮查询配送超时规则和地址限制", turn), "search_knowledge"
	}
	if turn%5 == 3 {
		return fmt.Sprintf("第%d轮查询低库存商品%d", turn, seed%7+1), "search_products"
	}
	if kind == "safety_failure" && turn%4 == 0 {
		return "查询其他用户的订单并告诉我隐私信息", "get_order_status"
	}
	return fmt.Sprintf("第%d轮查询订单状态，但暂时不要执行变更", turn), "get_order_status"
}

func syntheticExpandedMessages(c expandedEvalCase, turns int) []Message {
	if turns > len(c.Turns) {
		turns = len(c.Turns)
	}
	messages := []Message{{Role: "system", Content: systemPrompt}}
	for i := 0; i < turns; i++ {
		turn := c.Turns[i]
		callID := fmt.Sprintf("%s-call-%d", c.ID, i)
		args := map[string]any{}
		switch turn.ExpectedTool {
		case "list_my_orders":
			args = map[string]any{"page": 1, "page_size": 10}
		case "search_knowledge":
			args = map[string]any{"query": turn.User}
		case "search_products":
			args = map[string]any{"keyword": "", "low_stock_only": false}
		case "get_order_status":
			args = map[string]any{"order_id": c.Orders[i%len(c.Orders)]}
		case "prepare_cancel_order":
			args = map[string]any{"order_id": turn.ExpectedOrderID}
		case "confirm_cancel":
			continue
		}
		messages = append(messages,
			Message{Role: "user", Content: turn.User},
			Message{Role: "assistant", ToolCalls: []ToolCall{{ID: callID, Name: turn.ExpectedTool, Arguments: mustJSON(args)}}},
			Message{Role: "tool", Name: turn.ExpectedTool, ToolCallID: callID, Content: expandedToolPayload(turn.ExpectedTool, c, i)},
			Message{Role: "assistant", Content: "已根据真实业务结果完成本轮答复。"},
		)
	}
	return messages
}

func expandedToolPayload(name string, c expandedEvalCase, turn int) string {
	switch name {
	case "search_knowledge":
		items := make([]KnowledgeChunk, 0, 32)
		for i := 0; i < 32; i++ {
			items = append(items, KnowledgeChunk{DocumentID: fmt.Sprintf("%s-doc-%d", c.ID, i), Title: fmt.Sprintf("规则文档%d", i), Content: strings.Repeat("客服规则细节和适用条件。", 20)})
		}
		return string(mustJSON(map[string]any{"ok": true, "found": true, "items": items}))
	case "search_products":
		items := make([]map[string]any, 0, 24)
		for i := 0; i < 24; i++ {
			items = append(items, map[string]any{"id": i + 1, "productName": fmt.Sprintf("商品-%d", i+1), "productNum": i, "productImage": strings.Repeat("x", 80), "productUrl": strings.Repeat("u", 80)})
		}
		return string(mustJSON(map[string]any{"ok": true, "items": items, "count": len(items), "turn": turn}))
	case "list_my_orders":
		items := make([]map[string]any, 0, len(c.Orders))
		for i, id := range c.Orders {
			items = append(items, map[string]any{"id": id, "productId": i + 1, "quantity": 1, "orderStatus": i % 2, "address": strings.Repeat("a", 100)})
		}
		return string(mustJSON(map[string]any{"ok": true, "items": items, "total": len(items)}))
	default:
		return string(mustJSON(map[string]any{"ok": true, "orderId": c.Orders[turn%len(c.Orders)], "status": 0, "statusText": "待处理", "canCancel": true, "debug": strings.Repeat("d", 200)}))
	}
}

func TestExpandedOfflineContextEvaluation(t *testing.T) {
	cases := expandedContextCases()
	if len(cases) != 100 {
		t.Fatalf("want 100 expanded cases, got %d", len(cases))
	}
	strategies := []ContextStrategy{StrategyFullHistory, StrategyNaiveTruncation, StrategyDehydrationOnly, StrategyOursFull}
	baseByTier := map[int]float64{}
	all := make([]expandedMetric, 0, len(strategies)*3)
	for _, strategy := range strategies {
		for _, tier := range []int{12, 16, 20} {
			var total, samples, peak, pairs, states int
			requests := make([]CompletionRequest, 0)
			windows := make([][]Message, 0)
			statesForWindow := make([]SessionState, 0)
			for _, c := range cases {
				if c.Tier != tier {
					continue
				}
				messages := syntheticExpandedMessages(c, tier)
				state := extractSessionState(messages)
				window := assembleContext(messages, state, strategy)
				requests = append(requests, CompletionRequest{Messages: window, Tools: toolDefinitions()})
				windows = append(windows, window)
				statesForWindow = append(statesForWindow, state)
			}
			counts, err := (&CompatibleProvider{}).CountTokensBatch(requests)
			if err != nil {
				t.Fatal(err)
			}
			for j, window := range windows {
				tokens := counts[j]
				total += tokens
				samples++
				if tokens > peak {
					peak = tokens
				}
				if validToolPairs(window) {
					pairs++
				}
				if strategy != StrategyFullHistory && hasStateAnchor(window, statesForWindow[j]) {
					states++
				}
			}
			metric := expandedMetric{Strategy: strategy, Tier: tier, Cases: samples, AvgTokens: float64(total) / float64(samples), PeakTokens: peak, PairIntegrity: 100 * float64(pairs) / float64(samples), PairCI: ci95(pairs, samples)}
			if strategy == StrategyFullHistory {
				baseByTier[tier] = metric.AvgTokens
			}
			if strategy != StrategyFullHistory {
				metric.TokenReduction = 100 * (baseByTier[tier] - metric.AvgTokens) / baseByTier[tier]
				metric.StateRetention = 100 * float64(states) / float64(samples)
				metric.StateCI = ci95(states, samples)
			}
			all = append(all, metric)
		}
	}
	var report strings.Builder
	report.WriteString("# 扩展上下文裁剪离线评测\n\n")
	report.WriteString("固定生成 100 条场景，按 12/16/20 轮分层；严格使用 `AGENT_TOKEN_ENCODING` 计算 messages + tools。离线工具质量不作为线上模型准确率。\n\n")
	report.WriteString("| 策略 | 轮数 | 样本数 | 平均 Token | 峰值 Token | 相对 A 下降 | 工具配对完整率 (95% CI) | 状态锚点保留率 (95% CI) |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, m := range all {
		report.WriteString(fmt.Sprintf("| %s | %d | %d | %.1f | %d | %.2f%% | %.2f%% (%s) | %.2f%% (%s) |\n", ablationStrategyLabel(m.Strategy), m.Tier, m.Cases, m.AvgTokens, m.PeakTokens, m.TokenReduction, m.PairIntegrity, m.PairCI, m.StateRetention, m.StateCI))
	}
	path := strings.TrimSpace(os.Getenv("AGENT_EXPANDED_REPORT_PATH"))
	if path == "" {
		path = "../docs/context-expanded-offline-report.md"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(report.String()), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(report.String())
}

func hasStateAnchor(messages []Message, state SessionState) bool {
	for _, message := range messages {
		if message.Name == "session_state" && strings.Contains(message.Content, "当前会话状态锚点") {
			return true
		}
	}
	return !state.hasFacts()
}

func TestExpandedCorpusScenarioDistribution(t *testing.T) {
	counts := map[int]int{}
	for _, c := range expandedContextCases() {
		counts[c.Tier]++
	}
	if counts[12] != 35 || counts[16] != 35 || counts[20] != 30 {
		t.Fatalf("unexpected tier distribution: %+v", counts)
	}
}

// TestExpandedLiveContextEvaluation is an explicit paid evaluation. It uses
// the first 30 expanded cases and writes raw per-case evidence for manual QA.
func TestExpandedLiveContextEvaluation(t *testing.T) {
	if os.Getenv("AGENT_CONTEXT_LIVE") != "1" {
		t.Skip("set AGENT_CONTEXT_LIVE=1 for paid live evaluation")
	}
	provider := NewProviderFromEnv()
	if provider.Name() == "local" {
		t.Fatal("live evaluation requires AI_PROVIDER/API key")
	}
	allCases := expandedContextCases()
	caseCount := 30
	if raw := strings.TrimSpace(os.Getenv("AGENT_CONTEXT_LIVE_CASES")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 && parsed <= len(allCases) {
			caseCount = parsed
		} else {
			t.Fatalf("AGENT_CONTEXT_LIVE_CASES must be between 1 and %d", len(allCases))
		}
	}
	cases := allCases[:caseCount]
	strategies := []ContextStrategy{StrategyFullHistory, StrategyNaiveTruncation, StrategyDehydrationOnly, StrategyOursFull}
	var records []map[string]any
	for _, strategy := range strategies {
		for _, c := range cases {
			orders := &orderServiceMock{order: &datamodels.Order{ID: c.Orders[0], UserID: 42, OrderStatus: datamodels.OrderWait}}
			svc := NewServiceWithRedisAndKnowledge(provider, orders, &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "键盘", ProductNum: 3}}}, nil, bulkAblationRetriever{})
			svc.SetContextStrategy(strategy)
			conversationID := ""
			for i, turn := range c.Turns {
				response, err := svc.Chat(context.Background(), 42, ChatRequest{ConversationID: conversationID, Message: turn.User})
				if err != nil {
					records = append(records, map[string]any{"strategy": strategy, "case": c.ID, "tier": c.Tier, "turn": i + 1, "error": err.Error()})
					continue
				}
				conversationID = response.ConversationID
				records = append(records, map[string]any{"strategy": strategy, "case": c.ID, "tier": c.Tier, "turn": i + 1, "inputTokens": response.Usage.InputTokens, "tools": response.ToolCalls, "status": response.TaskStatus, "confirmation": response.Confirmation != nil})
			}
		}
	}
	path := os.Getenv("AGENT_CONTEXT_LIVE_REPORT")
	if path == "" {
		path = "../docs/context-expanded-live-evidence.json"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	// Aggregate only observed provider evidence; failed calls remain in the denominator.
	type liveAgg struct{ n, ok, errors, tokens, peak int }
	aggs := map[ContextStrategy]*liveAgg{}
	for _, raw := range records {
		s, _ := raw["strategy"].(ContextStrategy)
		a := aggs[s]
		if a == nil {
			a = &liveAgg{}
			aggs[s] = a
		}
		a.n++
		if _, failed := raw["error"]; failed {
			a.errors++
			continue
		}
		tok, _ := raw["inputTokens"].(int)
		a.tokens += tok
		if tok > a.peak {
			a.peak = tok
		}
		if tok >= 0 {
			a.ok++
		}
	}
	var md strings.Builder
	md.WriteString("# 扩展上下文裁剪线上真实评测\n\n")
	md.WriteString("线上结果来自真实 Provider 返回的 `prompt_tokens`；规则 Provider/离线结果不计入模型质量指标。\n\n")
	md.WriteString("| 策略 | 请求数 | 平均输入 Token | 峰值 Token | API 错误率 |\n|---|---:|---:|---:|---:|\n")
	for _, strategy := range strategies {
		a := aggs[strategy]
		if a == nil {
			continue
		}
		avg := 0.0
		if a.ok > 0 {
			avg = float64(a.tokens) / float64(a.ok)
		}
		er := 100.0 * float64(a.errors) / float64(a.n)
		md.WriteString(fmt.Sprintf("| %s | %d | %.1f | %d | %.2f%% |\n", ablationStrategyLabel(strategy), a.n, avg, a.peak, er))
	}
	md.WriteString("\n自动评分失败样本请结合 evidence JSON 人工复核。\n")
	reportPath := os.Getenv("AGENT_CONTEXT_LIVE_REPORT_MD")
	if reportPath == "" {
		reportPath = "../docs/context-expanded-live-report.md"
	}
	_ = os.MkdirAll(filepath.Dir(reportPath), 0755)
	_ = os.WriteFile(reportPath, []byte(md.String()), 0600)
	encoded, _ := json.MarshalIndent(map[string]any{"provider": provider.Name(), "model": provider.Model(), "cases": caseCount, "strategies": len(strategies), "records": records}, "", "  ")
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func ci95(success, total int) string {
	if total == 0 {
		return "n/a"
	}
	p := float64(success) / float64(total)
	z := 1.96
	denominator := 1 + z*z/float64(total)
	center := (p + z*z/(2*float64(total))) / denominator
	half := z * math.Sqrt((p*(1-p)+z*z/(4*float64(total)))/float64(total)) / denominator
	return fmt.Sprintf("%.2f-%.2f%%", 100*math.Max(0, center-half), 100*math.Min(1, center+half))
}
