package agent

import (
	"context"
	"encoding/json"
	"testing"

	"product/datamodels"
)

type repeatingProvider struct{}

func (p *repeatingProvider) Name() string  { return "repeat-fixture" }
func (p *repeatingProvider) Model() string { return "repeat-fixture-v1" }
func (p *repeatingProvider) Complete(context.Context, CompletionRequest) (Completion, error) {
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: randomID(), Name: "get_order_status", Arguments: mustJSON(map[string]any{"order_id": 18})}}}}, nil
}

func TestRepeatedToolCallRequestsHandoff(t *testing.T) {
	orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
	svc := NewService(&repeatingProvider{}, orders, &productServiceMock{})
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "查订单18"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Handoff == nil || response.Handoff.Reason != "repeated_tool_call" || len(response.ToolCalls) != 1 {
		t.Fatalf("expected bounded duplicate handoff, got %+v", response)
	}
	if orders.cancelCallCount != 0 || response.Confirmation != nil {
		t.Fatal("unexpected side effect")
	}
}

func TestReadOnlyTimeoutRetriesOnceAndRequestsHandoff(t *testing.T) {
	svc := NewService(&chaosProvider{scenario: chaosScenario{tool: "search_products", arguments: mustJSON(map[string]any{"keyword": "", "low_stock_only": false})}}, &orderServiceMock{}, &failingProductService{})
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "查商品"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Handoff == nil || response.Handoff.Reason != "tool_unavailable" || len(response.ToolCalls) != 1 || response.ToolCalls[0].Attempts != 2 || response.ToolCalls[0].FailureClass != "transient" {
		t.Fatalf("expected classified retry, got %+v", response)
	}
}

type rotatingProvider struct{ n int }

func (p *rotatingProvider) Name() string  { return "rotating-fixture" }
func (p *rotatingProvider) Model() string { return "rotating-fixture-v1" }
func (p *rotatingProvider) Complete(context.Context, CompletionRequest) (Completion, error) {
	p.n++
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: randomID(), Name: "get_order_status", Arguments: json.RawMessage(mustJSON(map[string]any{"order_id": int64(p.n)}))}}}}, nil
}

func TestMaxTurnsRequestsHandoff(t *testing.T) {
	svc := NewService(&rotatingProvider{}, &orderServiceMock{}, &productServiceMock{})
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "查多个订单"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Handoff == nil || response.Handoff.Reason != "max_turns" || len(response.ToolCalls) != maxAgentTurns {
		t.Fatalf("expected turn limit handoff, got %+v", response)
	}
}

func TestHandoffRevokesPendingConfirmation(t *testing.T) {
	orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
	svc := NewService(&repeatCancelProvider{}, orders, &productServiceMock{})
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "取消订单18"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Handoff == nil || response.Confirmation != nil {
		t.Fatalf("expected handoff without confirmation: %+v", response)
	}
	if len(svc.pending) != 0 || orders.cancelCallCount != 0 {
		t.Fatal("handoff left a pending cancellation")
	}
}

type repeatCancelProvider struct{}

func (p *repeatCancelProvider) Name() string  { return "repeat-cancel" }
func (p *repeatCancelProvider) Model() string { return "repeat-cancel-v1" }
func (p *repeatCancelProvider) Complete(context.Context, CompletionRequest) (Completion, error) {
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: randomID(), Name: "prepare_cancel_order", Arguments: mustJSON(map[string]any{"order_id": 18})}}}}, nil
}

func TestBoundedContextPreservesSystemAndUserTurn(t *testing.T) {
	messages := []Message{{Role: "system", Content: systemPrompt}}
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: "用户请求"})
		messages = append(messages, Message{Role: "assistant", Content: "工具调用"})
		messages = append(messages, Message{Role: "tool", Name: "search_products", Content: `{"ok":true}`})
		messages = append(messages, Message{Role: "assistant", Content: "已处理"})
	}
	bounded := boundedContext(messages)
	if len(bounded) < 2 || bounded[0].Role != "system" || bounded[len(bounded)-1].Role != "assistant" {
		t.Fatalf("invalid bounded context: %+v", bounded)
	}
	for i := 1; i < len(bounded); i++ {
		if bounded[i].Role == "tool" && (i == 1 || bounded[i-1].Role != "assistant") {
			t.Fatalf("tool message detached from assistant call at %d", i)
		}
	}
}
