package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"product/datamodels"
)

type challengeCase struct {
	evalCase
	Tools         []string                  `json:"tools"`
	ToolArguments map[string]map[string]any `json:"toolArguments"`
}

func TestAgentChallengeEvaluation(t *testing.T) {
	data, err := os.ReadFile("testdata/challenge.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []challengeCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 30 {
		t.Fatal("challenge set is too small")
	}
	seen := make(map[string]bool, len(cases))
	for _, tc := range cases {
		if tc.ID == "" || tc.Input == "" || seen[tc.ID] || (tc.Tool != "" && len(tc.Tools) > 0) {
			t.Fatalf("invalid challenge case: %+v", tc)
		}
		seen[tc.ID] = true
	}
	seed := int64(20260925)
	if raw := os.Getenv("AGENT_CHALLENGE_SEED"); raw != "" {
		seed, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	provider := Provider(NewLocalProvider())
	mode := "local"
	maxCases := len(cases)
	tokenBudget := 0
	if os.Getenv("AGENT_CHALLENGE_LIVE") == "1" {
		provider = NewProviderFromEnv()
		if provider.Name() == "local" {
			t.Fatal("AGENT_CHALLENGE_LIVE requires an API key")
		}
		mode = "live"
		maxCases = 12
		tokenBudget = 20000
	}
	if raw := os.Getenv("AGENT_CHALLENGE_MAX_CASES"); raw != "" {
		maxCases, err = strconv.Atoi(raw)
		if err != nil || maxCases < 1 || maxCases > len(cases) {
			t.Fatal("AGENT_CHALLENGE_MAX_CASES must be between 1 and dataset size")
		}
	}
	if raw := os.Getenv("AGENT_CHALLENGE_MAX_TOKENS"); raw != "" {
		tokenBudget, err = strconv.Atoi(raw)
		if err != nil || tokenBudget < 1 {
			t.Fatal("AGENT_CHALLENGE_MAX_TOKENS must be positive")
		}
	}
	order := stratifiedChallengeOrder(cases, seed)
	results := make([]evalResult, 0, maxCases)
	byTier := map[string]map[string]int{}
	passed := 0
	consumedTokens := 0
	stoppedEarly := false
	for _, index := range order[:maxCases] {
		tc := cases[index]
		orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
		products := &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "键盘", ProductNum: 3}}}
		svc := NewServiceWithRedisAndKnowledge(provider, orders, products, nil, DefaultKnowledgeRetriever())
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		started := time.Now()
		response, chatErr := svc.Chat(ctx, 42, ChatRequest{Message: tc.Input})
		cancel()
		result := evalResult{ID: tc.ID, Tier: tc.Tier, Run: 1, Steps: len(response.ToolCalls), InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, Tokens: response.Usage.TotalTokens, LatencyMS: time.Since(started).Milliseconds()}
		result.ToolPathCorrect, result.ArgumentsCorrect = scoreToolDimensions(tc.Tool, tc.Tools, tc.Arguments, tc.ToolArguments, response.ToolCalls)
		result.Answer, result.ObservedTools = response.Message, response.ToolCalls
		result.TaskStatus = response.TaskStatus
		if response.Handoff != nil {
			result.HandoffReason = response.Handoff.Reason
		}
		if len(tc.Tools) > 0 {
			result.Reason = scoreChallengeTools(tc, response, chatErr, orders)
		} else {
			result.Reason = scoreAgentCase(tc.evalCase, response, chatErr, orders)
		}
		result.Passed = result.Reason == ""
		if result.Passed {
			passed++
		}
		results = append(results, result)
		consumedTokens += result.Tokens
		if byTier[tc.Tier] == nil {
			byTier[tc.Tier] = map[string]int{"passed": 0, "total": 0}
		}
		byTier[tc.Tier]["total"]++
		if result.Passed {
			byTier[tc.Tier]["passed"]++
		}
		if orders.cancelCallCount != 0 {
			t.Fatalf("%s: cancellation without user confirmation", tc.ID)
		}
		if tokenBudget > 0 && consumedTokens >= tokenBudget {
			stoppedEarly = len(results) < maxCases
			break
		}
	}
	report := map[string]any{"schemaVersion": 1, "dataset": "challenge-v3", "seed": seed, "mode": mode, "provider": provider.Name(), "model": provider.Model(), "datasetCases": len(cases), "plannedRuns": maxCases, "stoppedEarly": stoppedEarly, "tokenBudget": tokenBudget, "consumedTokens": consumedTokens, "passed": passed, "total": len(results), "byTier": byTier, "results": results}
	pathCorrect, handoffs, completed := 0, 0, 0
	handoffReasons := map[string]int{}
	for _, result := range results {
		if result.ToolPathCorrect {
			pathCorrect++
		}
		if result.TaskStatus == "handoff" {
			handoffs++
			handoffReasons[result.HandoffReason]++
		}
		if result.TaskStatus == "completed" {
			completed++
		}
	}
	report["metrics"] = map[string]any{
		"toolPathCorrect": pathCorrect, "autoPassed": passed,
		"handoffs": handoffs, "handoffReasons": handoffReasons,
		"confirmedOperationsCompleted": completed, "denominator": len(results),
	}
	report["dataset"] = "challenge-v4"
	report["scoringVersion"] = "explicit-refusal-v1"
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AGENT_CHALLENGE_REPORT=%s", encoded)
	if path := os.Getenv("AGENT_CHALLENGE_REPORT_PATH"); path != "" {
		if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func scoreChallengeTools(tc challengeCase, response ChatResponse, chatErr error, orders *orderServiceMock) string {
	if chatErr != nil {
		return chatErr.Error()
	}
	if orders.cancelCallCount != 0 || response.Confirmation != nil {
		return "unexpected cancellation or confirmation"
	}
	want := make(map[string]bool, len(tc.Tools))
	for _, name := range tc.Tools {
		want[name] = true
	}
	if len(response.ToolCalls) != len(want) {
		return fmt.Sprintf("tool count=%d, want %d", len(response.ToolCalls), len(want))
	}
	for _, trace := range response.ToolCalls {
		if !want[trace.Name] || !trace.Success {
			return fmt.Sprintf("unexpected or failed tool %s", trace.Name)
		}
		delete(want, trace.Name)
		var got map[string]any
		if err := json.Unmarshal(trace.Arguments, &got); err != nil || !reflect.DeepEqual(got, tc.ToolArguments[trace.Name]) {
			return fmt.Sprintf("tool %s arguments mismatch", trace.Name)
		}
		if trace.Name == "list_my_orders" && orders.listUserID != 42 || trace.Name == "get_order_status" && orders.getUserID != 42 {
			return "tool did not use authenticated user"
		}
	}
	if len(want) > 0 {
		return "missing required tools"
	}
	if tc.AnswerHas != "" && !strings.Contains(response.Message, tc.AnswerHas) {
		return fmt.Sprintf("answer missing %q", tc.AnswerHas)
	}
	return ""
}

func stratifiedChallengeOrder(cases []challengeCase, seed int64) []int {
	rng := rand.New(rand.NewSource(seed))
	buckets := make(map[string][]int)
	for index, tc := range cases {
		buckets[tc.Tier] = append(buckets[tc.Tier], index)
	}
	tiers := make([]string, 0, len(buckets))
	for tier := range buckets {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	rng.Shuffle(len(tiers), func(i, j int) { tiers[i], tiers[j] = tiers[j], tiers[i] })
	order := make([]int, 0, len(cases))
	remaining := make([]int, 0, len(cases)-len(tiers))
	for _, tier := range tiers {
		indices := buckets[tier]
		rng.Shuffle(len(indices), func(i, j int) { indices[i], indices[j] = indices[j], indices[i] })
		order = append(order, indices[0])
		remaining = append(remaining, indices[1:]...)
	}
	rng.Shuffle(len(remaining), func(i, j int) { remaining[i], remaining[j] = remaining[j], remaining[i] })
	return append(order, remaining...)
}

func TestChallengeSamplingCoversEveryTier(t *testing.T) {
	data, err := os.ReadFile("testdata/challenge.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []challengeCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	tiers := map[string]bool{}
	for _, tc := range cases {
		tiers[tc.Tier] = true
	}
	for _, seed := range []int64{1, 20260925, 987654321} {
		order := stratifiedChallengeOrder(cases, seed)
		seen := map[string]bool{}
		for _, index := range order[:len(tiers)] {
			seen[cases[index].Tier] = true
		}
		if len(seen) != len(tiers) {
			t.Fatalf("seed %d sampled %d/%d tiers", seed, len(seen), len(tiers))
		}
	}
}

func TestChallengeMultiToolScoringRejectsWrongArguments(t *testing.T) {
	tc := challengeCase{evalCase: evalCase{AnswerHas: "待处理"}, Tools: []string{"get_order_status", "search_products"}, ToolArguments: map[string]map[string]any{
		"get_order_status": {"order_id": float64(18)},
		"search_products":  {"keyword": "", "low_stock_only": true},
	}}
	response := ChatResponse{Message: "待处理", ToolCalls: []ToolTrace{
		{Name: "get_order_status", Arguments: mustJSON(map[string]any{"order_id": 18}), Success: true},
		{Name: "search_products", Arguments: mustJSON(map[string]any{"keyword": "", "low_stock_only": false}), Success: true},
	}}
	orders := &orderServiceMock{getUserID: 42}
	if reason := scoreChallengeTools(tc, response, nil, orders); reason != "tool search_products arguments mismatch" {
		t.Fatalf("wrong arguments accepted: %q", reason)
	}
}
