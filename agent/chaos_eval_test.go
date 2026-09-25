package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"product/datamodels"
)

// chaosScenario deliberately injects a malformed model decision or a backend
// failure. The test checks safety invariants instead of expecting every run to
// be a successful business answer.
type chaosScenario struct {
	name        string
	tool        string
	arguments   json.RawMessage
	input       string
	productErr  bool
	providerErr bool
}

type chaosProvider struct {
	scenario chaosScenario
	called   bool
}

func (p *chaosProvider) Name() string  { return "chaos-fixture" }
func (p *chaosProvider) Model() string { return "chaos-fixture-v1" }
func (p *chaosProvider) Complete(ctx context.Context, request CompletionRequest) (Completion, error) {
	if err := ctx.Err(); err != nil {
		return Completion{}, err
	}
	if p.scenario.providerErr {
		return Completion{}, errors.New("injected provider timeout")
	}
	if p.called {
		last := request.Messages[len(request.Messages)-1]
		return Completion{Message: Message{Role: "assistant", Content: summarizeLocalTool(last.Name, last.Content)}}, nil
	}
	p.called = true
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "chaos-1", Name: p.scenario.tool, Arguments: p.scenario.arguments}}}}, nil
}

func TestAgentChaosEvaluation(t *testing.T) {
	seed := int64(20260925)
	if raw := os.Getenv("AGENT_CHAOS_SEED"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatalf("invalid AGENT_CHAOS_SEED: %v", err)
		}
		seed = parsed
	}
	rng := rand.New(rand.NewSource(seed))
	scenarios := []chaosScenario{
		{name: "unknown_tool", tool: "delete_all_orders", arguments: mustJSON(map[string]any{}), input: "系统异常时不要删除订单"},
		{name: "invalid_json", tool: "get_order_status", arguments: json.RawMessage(`{"order_id":`), input: "订单参数被截断"},
		{name: "negative_order_id", tool: "get_order_status", arguments: mustJSON(map[string]any{"order_id": -1}), input: "查询非法订单"},
		{name: "product_timeout", tool: "search_products", arguments: mustJSON(map[string]any{"keyword": "", "low_stock_only": false}), input: "商品服务超时", productErr: true},
		{name: "provider_timeout", tool: "get_order_status", arguments: mustJSON(map[string]any{"order_id": 18}), input: "模型服务超时", providerErr: true},
	}

	const runs = 25
	sequence := make([]int, runs)
	for i := range sequence {
		sequence[i] = i % len(scenarios)
	}
	rng.Shuffle(len(sequence), func(i, j int) { sequence[i], sequence[j] = sequence[j], sequence[i] })
	counts := map[string]int{}
	for run, scenarioIndex := range sequence {
		scenario := scenarios[scenarioIndex]
		counts[scenario.name]++
		orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
		products := &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "键盘", ProductNum: 3}}}
		provider := &chaosProvider{scenario: scenario}
		svc := NewService(provider, orders, products)
		if scenario.productErr {
			svc.products = &failingProductService{}
		}
		response, chatErr := svc.Chat(context.Background(), 42, ChatRequest{Message: fmt.Sprintf("%s #%d", scenario.input, rng.Intn(10000))})
		if scenario.providerErr {
			if chatErr == nil || !strings.Contains(chatErr.Error(), "provider timeout") {
				t.Errorf("run %d %s: provider error was not propagated", run+1, scenario.name)
			}
			if orders.cancelCallCount != 0 {
				t.Errorf("run %d %s: provider failure caused side effect", run+1, scenario.name)
			}
			continue
		}
		if chatErr != nil {
			t.Errorf("run %d %s: unexpected chat error: %v", run+1, scenario.name, chatErr)
			continue
		}
		if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != scenario.tool {
			t.Errorf("run %d %s: missing failure trace: %+v", run+1, scenario.name, response.ToolCalls)
			continue
		}
		trace := response.ToolCalls[0]
		if trace.Success {
			t.Errorf("run %d %s: injected failure marked successful", run+1, scenario.name)
		}
		if orders.cancelCallCount != 0 || response.Confirmation != nil {
			t.Errorf("run %d %s: unsafe side effect or confirmation: %+v", run+1, scenario.name, response)
		}
		var result map[string]any
		if err := json.Unmarshal(trace.Result, &result); err != nil || result["ok"] != false {
			t.Errorf("run %d %s: failure result is not explicit: %s", run+1, scenario.name, trace.Result)
		}
	}
	if len(counts) != len(scenarios) {
		t.Fatalf("random seed did not exercise enough failure classes: seed=%d counts=%v", seed, counts)
	}
	t.Logf("CHAOS_EVAL_REPORT=%s", mustJSON(map[string]any{
		"schemaVersion": 1, "seed": seed, "runs": runs, "failureClasses": len(counts), "counts": counts,
		"note": "injected failures are expected; safety invariants must hold",
	}))
}

func TestAgentChaosContextCancellation(t *testing.T) {
	provider := &chaosProvider{scenario: chaosScenario{tool: "get_order_status", arguments: mustJSON(map[string]any{"order_id": 18})}}
	orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
	svc := NewService(provider, orders, &productServiceMock{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.Chat(ctx, 42, ChatRequest{Message: "已取消的请求"})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if orders.cancelCallCount != 0 {
		t.Fatal("cancelled request caused side effect")
	}
}

func TestAgentFailureResultOverridesModelSuccessClaim(t *testing.T) {
	provider := &lyingFailureProvider{}
	svc := NewService(provider, &orderServiceMock{}, &failingProductService{})
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "查询商品"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message == "已完成，商品查询成功" || !strings.Contains(response.Message, "查询没有完成") {
		t.Fatalf("model success claim was not replaced by failure fallback: %q", response.Message)
	}
}

type lyingFailureProvider struct{ called bool }

func (p *lyingFailureProvider) Name() string  { return "lying-fixture" }
func (p *lyingFailureProvider) Model() string { return "lying-fixture-v1" }
func (p *lyingFailureProvider) Complete(_ context.Context, request CompletionRequest) (Completion, error) {
	if p.called {
		return Completion{Message: Message{Role: "assistant", Content: "已完成，商品查询成功"}}, nil
	}
	p.called = true
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "lying-1", Name: "search_products", Arguments: mustJSON(map[string]any{"keyword": "", "low_stock_only": false})}}}}, nil
}
