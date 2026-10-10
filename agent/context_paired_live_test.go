package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"product/datamodels"
	"testing"
	"time"
)

// Both arms have identical simulated history and state. Only compaction and
// full-turn pruning differ. These are seeded multi-turn challenge continuations.
func TestContextPairedLiveEvaluation(t *testing.T) {
	if os.Getenv("AGENT_CONTEXT_PAIRED_LIVE") != "1" {
		t.Skip("explicit paid evaluation only")
	}
	provider := NewProviderFromEnv()
	if provider.Name() == "local" {
		t.Fatal("live provider required")
	}
	data, err := os.ReadFile("testdata/challenge.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []challengeCase
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	type pair struct {
		ID  string     `json:"id"`
		Off evalResult `json:"off"`
		On  evalResult `json:"on"`
	}
	pairs := []pair{}
	totalTokens := 0
	reportPath := os.Getenv("AGENT_CONTEXT_PAIRED_REPORT")
	if reportPath == "" {
		t.Fatal("report path required")
	}
	writeReport := func() {
		report := map[string]any{"dataset": "challenge-v4-seeded-history", "model": provider.Model(), "provider": provider.Name(), "retrieval": "expanded BM25, same in both arms", "repetitions": 1, "seedHistoryTurns": 6, "plannedPairs": len(cases), "completedPairs": len(pairs), "usageTokens": totalTokens, "budgetTokens": 250000, "pairs": pairs}
		b, e := json.MarshalIndent(report, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(reportPath, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	for index, tc := range cases {
		p := pair{ID: tc.ID}
		arms := []bool{false, true}
		if index%2 == 1 {
			arms = []bool{true, false}
		}
		for _, on := range arms {
			orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
			products := &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "键盘", ProductNum: 3}}}
			svc := NewServiceWithRedisAndKnowledge(provider, orders, products, nil, &BM25Retriever{Chunks: policyChunks(policyDocuments())})
			svc.disableContextCompression = !on
			history := []Message{}
			for turn := 0; turn < 6; turn++ {
				call := ToolCall{ID: fmt.Sprintf("history-%d", turn), Name: "search_products", Arguments: mustJSON(map[string]any{"keyword": "键盘", "low_stock_only": false})}
				result := mustJSON(map[string]any{"ok": true, "count": 3, "items": []map[string]any{{"id": 1, "productName": "键盘", "productNum": 3, "productImage": "https://shop.example/key.png", "productUrl": "https://shop.example/product/1"}, {"id": 2, "productName": "鼠标", "productNum": 8}, {"id": 3, "productName": "耳机", "productNum": 12}}})
				observation := string(result)
				if on {
					observation = compactToolResult(call.Name, result)
				}
				history = append(history, Message{Role: "user", Content: "看看商品库存，暂时只查询，不购买。"}, Message{Role: "assistant", ToolCalls: []ToolCall{call}}, Message{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: observation}, Message{Role: "assistant", Content: "键盘库存3、鼠标8、耳机12；未执行购买。"})
			}
			svc.chats["paired"] = &conversation{UserID: 42, Messages: history, Updated: svc.now()}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			started := time.Now()
			response, chatErr := svc.Chat(ctx, 42, ChatRequest{ConversationID: "paired", Message: tc.Input})
			cancel()
			r := evalResult{ID: tc.ID, Tier: tc.Tier, Run: 1, Steps: len(response.ToolCalls), InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, Tokens: response.Usage.TotalTokens, LatencyMS: time.Since(started).Milliseconds(), TaskStatus: response.TaskStatus, Answer: response.Message, ObservedTools: response.ToolCalls}
			r.ToolPathCorrect, r.ArgumentsCorrect = scoreToolDimensions(tc.Tool, tc.Tools, tc.Arguments, tc.ToolArguments, response.ToolCalls)
			if response.Handoff != nil {
				r.HandoffReason = response.Handoff.Reason
			}
			if len(tc.Tools) > 0 {
				r.Reason = scoreChallengeTools(tc, response, chatErr, orders)
			} else {
				r.Reason = scoreAgentCase(tc.evalCase, response, chatErr, orders)
			}
			r.Passed = r.Reason == ""
			if orders.cancelCallCount != 0 {
				t.Fatal("unconfirmed cancellation")
			}
			totalTokens += r.Tokens
			if on {
				p.On = r
			} else {
				p.Off = r
			}
			t.Logf("PAIR %s on=%v passed=%v status=%s input=%d", tc.ID, on, r.Passed, r.TaskStatus, r.InputTokens)
		}
		pairs = append(pairs, p)
		writeReport()
		if totalTokens >= 250000 {
			break
		}
	}
}

func TestContextSwitchRetainsState(t *testing.T) {
	messages := []Message{{Role: "system", Content: systemPrompt}}
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: "查询"}, Message{Role: "assistant", Content: "答复"})
	}
	state := SessionState{ProtectedOrderIDs: []int64{19}, AddressLocked: true}
	s := NewService(NewLocalProvider(), &orderServiceMock{}, &productServiceMock{})
	on := s.modelContext(messages, state)
	s.disableContextCompression = true
	off := s.modelContext(messages, state)
	if len(on) >= len(off) {
		t.Fatal("switch did not change history")
	}
	for _, arm := range [][]Message{on, off} {
		found := false
		for _, m := range arm {
			if m.Name == "session_state" {
				found = true
			}
		}
		if !found {
			t.Fatal("state missing")
		}
	}
}
