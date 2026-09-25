package agent

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"product/datamodels"
)

func TestAgentChallengeEvaluation(t *testing.T) {
	data, err := os.ReadFile("testdata/challenge.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatal("challenge set is too small")
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
	if os.Getenv("AGENT_CHALLENGE_LIVE") == "1" {
		provider = NewProviderFromEnv()
		if provider.Name() == "local" {
			t.Fatal("AGENT_CHALLENGE_LIVE requires an API key")
		}
		mode = "live"
	}
	order := rand.New(rand.NewSource(seed)).Perm(len(cases))
	results := make([]evalResult, 0, len(cases))
	passed := 0
	for _, index := range order {
		tc := cases[index]
		orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
		products := &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "键盘", ProductNum: 3}}}
		svc := NewService(provider, orders, products)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		started := time.Now()
		response, chatErr := svc.Chat(ctx, 42, ChatRequest{Message: tc.Input})
		cancel()
		result := evalResult{ID: tc.ID, Tier: tc.Tier, Run: 1, Steps: len(response.ToolCalls), Tokens: response.Usage.TotalTokens, LatencyMS: time.Since(started).Milliseconds()}
		result.Reason = scoreAgentCase(tc, response, chatErr, orders)
		result.Passed = result.Reason == ""
		if result.Passed {
			passed++
		}
		results = append(results, result)
		if orders.cancelCallCount != 0 {
			t.Fatalf("%s: cancellation without user confirmation", tc.ID)
		}
	}
	report := map[string]any{"schemaVersion": 1, "dataset": "challenge-v1", "seed": seed, "mode": mode, "provider": provider.Name(), "model": provider.Model(), "passed": passed, "total": len(results), "results": results}
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
