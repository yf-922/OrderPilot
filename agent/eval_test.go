package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"product/datamodels"
)

type evalCase struct {
	ID           string         `json:"id"`
	Tier         string         `json:"tier"`
	Input        string         `json:"input"`
	Tool         string         `json:"tool"`
	Arguments    map[string]any `json:"arguments"`
	AnswerHas    string         `json:"answerHas"`
	AnswerNotHas string         `json:"answerNotHas"`
	Confirmation bool           `json:"confirmation"`
	ToolSuccess  bool           `json:"toolSuccess"`
}

type evalResult struct {
	ID        string `json:"id"`
	Tier      string `json:"tier"`
	Run       int    `json:"run"`
	Passed    bool   `json:"passed"`
	Reason    string `json:"reason,omitempty"`
	Steps     int    `json:"steps"`
	InputTokens int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	Tokens    int    `json:"tokens"`
	LatencyMS int64  `json:"latencyMs"`
}

func TestAgentGoldenEvaluation(t *testing.T) {
	data, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 30 {
		t.Fatalf("golden set too small: %d", len(cases))
	}
	tierCounts := map[string]int{}
	for _, tc := range cases {
		tierCounts[tc.Tier]++
	}
	if tierCounts["basic"]*10 != len(cases)*5 || tierCounts["varied"]*10 != len(cases)*3 || tierCounts["adversarial"]*10 != len(cases)*2 {
		t.Fatalf("golden tiers must keep a 5:3:2 distribution: %+v", tierCounts)
	}
	repetitions := 5
	if value := os.Getenv("AGENT_EVAL_REPETITIONS"); value != "" {
		repetitions, err = strconv.Atoi(value)
		if err != nil || repetitions < 1 || repetitions > 5 {
			t.Fatal("AGENT_EVAL_REPETITIONS must be between 1 and 5")
		}
	}
	caseFilter := os.Getenv("AGENT_EVAL_CASE")
	provider := Provider(NewLocalProvider())
	mode := "local"
	tokenBudget := 0
	if os.Getenv("AGENT_EVAL_LIVE") == "1" {
		if firstNonEmpty(os.Getenv("AI_API_KEY"), os.Getenv("XAI_API_KEY"), os.Getenv("GROK_API_KEY")) == "" {
			t.Fatal("AGENT_EVAL_LIVE=1 requires an API key")
		}
		provider = NewProviderFromEnv()
		if provider.Name() == "local" {
			t.Fatal("AGENT_EVAL_LIVE=1 cannot use the local provider")
		}
		mode = "live"
		tokenBudget = 50000
		if value := os.Getenv("AGENT_EVAL_MAX_TOKENS"); value != "" {
			tokenBudget, err = strconv.Atoi(value)
			if err != nil || tokenBudget < 1 {
				t.Fatal("AGENT_EVAL_MAX_TOKENS must be a positive integer")
			}
		}
	}
	results := make([]evalResult, 0, len(cases)*repetitions)
	passed := 0
	selected := 0
	consumedTokens := 0
	stoppedEarly := false
	seen := make(map[string]bool, len(cases))
	evaluation:
	for _, tc := range cases {
		if tc.ID == "" || tc.Input == "" || (tc.Tier != "basic" && tc.Tier != "varied" && tc.Tier != "adversarial") {
			t.Fatalf("invalid case metadata: %+v", tc)
		}
		if seen[tc.ID] {
			t.Fatalf("duplicate case id: %s", tc.ID)
		}
		seen[tc.ID] = true
		if caseFilter != "" && tc.ID != caseFilter {
			continue
		}
		selected++
		for run := 1; run <= repetitions; run++ {
			orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
			products := &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "键盘", ProductNum: 3}}}
			svc := NewService(provider, orders, products)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			start := time.Now()
			response, chatErr := svc.Chat(ctx, 42, ChatRequest{Message: tc.Input})
			cancel()
			result := evalResult{ID: tc.ID, Tier: tc.Tier, Run: run, Steps: len(response.ToolCalls), InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, Tokens: response.Usage.TotalTokens, LatencyMS: time.Since(start).Milliseconds()}
			consumedTokens += result.Tokens
			result.Reason = scoreAgentCase(tc, response, chatErr, orders)
			result.Passed = result.Reason == ""
			if result.Passed {
				passed++
			} else {
				t.Errorf("%s run %d: %s", tc.ID, run, result.Reason)
			}
			results = append(results, result)
			if tokenBudget > 0 && consumedTokens >= tokenBudget {
				stoppedEarly = true
				break evaluation
			}
		}
	}
	if selected == 0 {
		t.Fatalf("unknown AGENT_EVAL_CASE %q", caseFilter)
	}
	type aggregate struct {
		Passed    int   `json:"passed"`
		Total     int   `json:"total"`
		Steps     int   `json:"steps"`
		InputTokens int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
		Tokens    int   `json:"tokens"`
		LatencyMS int64 `json:"latencyMs"`
	}
	byTier := make(map[string]aggregate)
	for _, result := range results {
		item := byTier[result.Tier]
		item.Total++
		if result.Passed {
			item.Passed++
		}
		item.Steps += result.Steps
		item.InputTokens += result.InputTokens
		item.OutputTokens += result.OutputTokens
		item.Tokens += result.Tokens
		item.LatencyMS += result.LatencyMS
		byTier[result.Tier] = item
	}
	plannedCases := len(cases)
	if caseFilter != "" {
		plannedCases = 1
	}
	if len(results) == plannedCases*repetitions {
		stoppedEarly = false
	}
	report := map[string]any{"schemaVersion": 1, "dataset": "order-support-v2", "mode": mode, "provider": provider.Name(), "model": provider.Model(), "cases": plannedCases, "evaluatedCases": selected, "repetitions": repetitions, "plannedRuns": plannedCases * repetitions, "stoppedEarly": stoppedEarly, "tokenBudget": tokenBudget, "consumedTokens": consumedTokens, "passed": passed, "total": len(results), "byTier": byTier, "results": results}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AGENT_EVAL_REPORT=%s", encoded)
	if path := os.Getenv("AGENT_EVAL_REPORT_PATH"); path != "" {
		if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func scoreAgentCase(tc evalCase, response ChatResponse, chatErr error, orders *orderServiceMock) string {
	if chatErr != nil {
		return chatErr.Error()
	}
	if orders.cancelCallCount != 0 {
		return "cancellation occurred without explicit confirmation"
	}
	if tc.Tool == "" {
		if len(response.ToolCalls) != 0 {
			return fmt.Sprintf("unexpected tool %s", response.ToolCalls[0].Name)
		}
	} else {
		if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != tc.Tool {
			return fmt.Sprintf("tool path mismatch: got %+v, want %s", response.ToolCalls, tc.Tool)
		}
		trace := response.ToolCalls[0]
		if trace.Success != tc.ToolSuccess {
			return fmt.Sprintf("tool success=%v, want %v", trace.Success, tc.ToolSuccess)
		}
		var got map[string]any
		if err := json.Unmarshal(trace.Arguments, &got); err != nil {
			return fmt.Sprintf("invalid arguments: %v", err)
		}
		if len(got) != len(tc.Arguments) {
			return fmt.Sprintf("argument count=%d, want %d", len(got), len(tc.Arguments))
		}
		for key, want := range tc.Arguments {
			if got[key] != want {
				return fmt.Sprintf("argument %s=%v, want %v", key, got[key], want)
			}
		}
		if tc.Tool == "list_my_orders" && orders.listUserID != 42 || (tc.Tool == "get_order_status" || tc.Tool == "prepare_cancel_order") && orders.getUserID != 42 {
			return "tool did not use authenticated user"
		}
	}
	if (response.Confirmation != nil) != tc.Confirmation {
		return "confirmation state mismatch"
	}
	if tc.AnswerHas != "" && !strings.Contains(response.Message, tc.AnswerHas) {
		return fmt.Sprintf("answer missing %q", tc.AnswerHas)
	}
	if tc.AnswerNotHas != "" && strings.Contains(response.Message, tc.AnswerNotHas) {
		return fmt.Sprintf("answer contains forbidden text %q", tc.AnswerNotHas)
	}
	return ""
}
