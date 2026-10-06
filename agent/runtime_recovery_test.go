package agent

import (
	"context"
	"product/datamodels"
	"strings"
	"testing"
)

type runtimeScriptProvider struct {
	steps []Message
	calls int
}

func (p *runtimeScriptProvider) Name() string  { return "runtime-fixture" }
func (p *runtimeScriptProvider) Model() string { return "fixture" }
func (p *runtimeScriptProvider) Complete(_ context.Context, _ CompletionRequest) (Completion, error) {
	p.calls++
	if p.calls > len(p.steps) {
		return Completion{Message: Message{Role: "assistant", Content: "取消成功"}}, nil
	}
	return Completion{Message: p.steps[p.calls-1]}, nil
}

func TestRuntimeTerminalFailureSkipsOutstandingTools(t *testing.T) {
	p := &runtimeScriptProvider{steps: []Message{{Role: "assistant", ToolCalls: []ToolCall{
		{ID: "rejected", Name: "prepare_cancel_order", Arguments: mustJSON(map[string]any{"order_id": 18})},
		{ID: "skipped", Name: "search_products", Arguments: mustJSON(map[string]any{"keyword": "keyboard"})},
	}}}}
	s := NewService(p, &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderSuccess}}, &productServiceMock{})
	r, err := s.Chat(context.Background(), 42, ChatRequest{Message: "取消订单18"})
	if err != nil || p.calls != 1 || len(r.ToolCalls) != 1 || r.TaskStatus != "handoff" {
		t.Fatalf("terminal failure continued: %+v %v, model calls=%d", r, err, p.calls)
	}
	results := make(map[string]string)
	for _, m := range s.chats[r.ConversationID].Messages {
		if m.Role == "tool" {
			results[m.ToolCallID] = m.Content
		}
	}
	if !strings.Contains(results["rejected"], `"nextAction":"handoff"`) || !strings.Contains(results["skipped"], `"errorType":"skipped"`) {
		t.Fatalf("incomplete tool protocol: %+v", results)
	}
}

func TestRuntimeUnrelatedSuccessCannotHideInvalidArguments(t *testing.T) {
	p := &runtimeScriptProvider{steps: []Message{{Role: "assistant", ToolCalls: []ToolCall{
		{ID: "bad", Name: "get_order_status", Arguments: mustJSON(map[string]any{"order_id": "bad"})},
		{ID: "unrelated", Name: "search_products", Arguments: mustJSON(map[string]any{"keyword": "keyboard"})},
	}}}}
	s := NewService(p, &orderServiceMock{}, &productServiceMock{})
	r, err := s.Chat(context.Background(), 42, ChatRequest{Message: "查询订单"})
	if err != nil || r.TaskStatus != "needs_clarification" || strings.Contains(r.Message, "取消成功") {
		t.Fatalf("unresolved failure hidden by answer: %+v %v", r, err)
	}
}

func TestRuntimeStrictArgumentsRejectIdentityAndTrailingJSON(t *testing.T) {
	s := NewService(NewLocalProvider(), &orderServiceMock{}, &productServiceMock{})
	for _, tc := range []struct{ name, args string }{
		{"get_order_status", `{"order_id":18,"user_id":99}`},
		{"get_order_status", `{"order_id":18} {}`},
		{"get_order_status", `null`},
		{"list_my_orders", `{"page":1,"user_id":99}`},
		{"search_products", `{"keyword":1}`},
	} {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			call := normalizeToolCall(ToolCall{Name: tc.name, Arguments: []byte(tc.args)})
			result, confirmation, err := s.executeTool(context.Background(), 42, "test", call)
			if err == nil || confirmation != nil || classifyToolFailure(call.Name, result, err) != "invalid_arguments" {
				t.Fatalf("invalid arguments accepted: %s %v", result, err)
			}
		})
	}
}

type correctingProvider struct {
	calls    int
	observed bool
}

func (p *correctingProvider) Name() string  { return "correction-fixture" }
func (p *correctingProvider) Model() string { return "fixture" }
func (p *correctingProvider) Complete(_ context.Context, r CompletionRequest) (Completion, error) {
	p.calls++
	if p.calls == 1 {
		return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "bad", Name: "get_order_status", Arguments: mustJSON(map[string]any{"order_id": "bad"})}}}}, nil
	}
	if p.calls == 2 {
		last := r.Messages[len(r.Messages)-1]
		p.observed = last.Role == "tool" && strings.Contains(last.Content, "invalid_arguments") && strings.Contains(last.Content, `"recoverable":true`)
		return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "fixed", Name: "get_order_status", Arguments: mustJSON(map[string]any{"order_id": 18})}}}}, nil
	}
	return Completion{Message: Message{Role: "assistant", Content: "订单18待处理"}}, nil
}
func TestRuntimeInvalidArgumentsObservationAllowsCorrection(t *testing.T) {
	p := &correctingProvider{}
	s := NewService(p, &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42}}, &productServiceMock{})
	r, err := s.Chat(context.Background(), 42, ChatRequest{Message: "查询订单18"})
	if err != nil || !p.observed || len(r.ToolCalls) != 2 || !r.ToolCalls[1].Success || r.TaskStatus != "answered" {
		t.Fatalf("correction failed: %+v %v", r, err)
	}
}
func TestRuntimeBusinessFailureStopsBeforeNextModelTurn(t *testing.T) {
	p := &repeatCancelProvider{}
	s := NewService(p, &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderSuccess}}, &productServiceMock{})
	r, err := s.Chat(context.Background(), 42, ChatRequest{Message: "取消订单18"})
	if err != nil || r.TaskStatus != "handoff" || r.Handoff == nil || r.Handoff.Reason != "tool_business_rule" || len(r.ToolCalls) != 1 || r.Confirmation != nil {
		t.Fatalf("unsafe business failure: %+v %v", r, err)
	}
}
func TestRuntimeCancellationOnlyCompletedAfterConfirm(t *testing.T) {
	s := NewService(NewLocalProvider(), &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42}}, &productServiceMock{})
	r, err := s.Chat(context.Background(), 42, ChatRequest{Message: "取消订单18"})
	if err != nil || r.TaskStatus != "awaiting_confirmation" || r.Confirmation == nil {
		t.Fatalf("invalid pending status: %+v", r)
	}
	done, err := s.Confirm(context.Background(), 42, ConfirmRequest{ConversationID: r.ConversationID, Token: r.Confirmation.Token})
	if err != nil || done.TaskStatus != "completed" {
		t.Fatalf("invalid completion: %+v %v", done, err)
	}
}

func TestRuntimePreparedCancellationRejectsModelSuccessClaim(t *testing.T) {
	p := &runtimeScriptProvider{steps: []Message{{Role: "assistant", ToolCalls: []ToolCall{
		{ID: "prepare", Name: "prepare_cancel_order", Arguments: mustJSON(map[string]any{"order_id": 18})},
	}}}}
	s := NewService(p, &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42}}, &productServiceMock{})
	r, err := s.Chat(context.Background(), 42, ChatRequest{Message: "取消订单18"})
	if err != nil || r.TaskStatus != "awaiting_confirmation" || strings.Contains(r.Message, "取消成功") {
		t.Fatalf("premature completion claim: %+v %v", r, err)
	}
	history := s.chats[r.ConversationID].Messages
	if history[len(history)-1].Content != r.Message {
		t.Fatal("unsafe model response retained in history")
	}
}
