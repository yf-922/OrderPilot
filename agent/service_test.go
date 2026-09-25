package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"product/datamodels"
	"product/services"
)

type orderServiceMock struct {
	order           *datamodels.Order
	listUserID      int64
	getUserID       int64
	cancelUserID    int64
	cancelOrderID   int64
	cancelCallCount int
}

func (m *orderServiceMock) InsertOrder(context.Context, *datamodels.Order) (int64, error) {
	return 0, nil
}
func (m *orderServiceMock) GetOrderForUser(_ context.Context, orderID, userID int64) (*datamodels.Order, error) {
	m.getUserID = userID
	if m.order == nil || m.order.ID != orderID {
		return nil, errors.New("not found")
	}
	copy := *m.order
	return &copy, nil
}
func (m *orderServiceMock) ListOrdersForUser(_ context.Context, userID int64, _, _ int) ([]*datamodels.Order, int64, error) {
	m.listUserID = userID
	if m.order == nil {
		return nil, 0, nil
	}
	return []*datamodels.Order{m.order}, 1, nil
}
func (m *orderServiceMock) UpdateOrderStatus(context.Context, int64, int64, int64) error { return nil }
func (m *orderServiceMock) CancelOrder(_ context.Context, orderID, userID int64) error {
	m.cancelCallCount++
	m.cancelOrderID, m.cancelUserID = orderID, userID
	return nil
}

type productServiceMock struct{ items []*datamodels.Product }

func (m *productServiceMock) GetProductById(int64) (*datamodels.Product, error) { return nil, nil }
func (m *productServiceMock) GetProductAll() ([]*datamodels.Product, error)     { return m.items, nil }
func (m *productServiceMock) DeleteProductById(int64) error                     { return nil }
func (m *productServiceMock) InsertProduct(*datamodels.Product) (int64, error)  { return 0, nil }
func (m *productServiceMock) UpdateProduct(*datamodels.Product) error           { return nil }
func (m *productServiceMock) InvalidateCache()                                  {}
func (m *productServiceMock) ListProducts(int, int, string) ([]*datamodels.Product, int64, error) {
	return m.items, int64(len(m.items)), nil
}

var _ services.IOrderService = (*orderServiceMock)(nil)
var _ services.IProductService = (*productServiceMock)(nil)

func TestAgentListsOnlyAuthenticatedUsersOrders(t *testing.T) {
	orders := &orderServiceMock{order: &datamodels.Order{ID: 7, UserID: 42, OrderStatus: datamodels.OrderWait}}
	svc := NewService(NewLocalProvider(), orders, &productServiceMock{})
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "我的最近订单有哪些？"})
	if err != nil {
		t.Fatal(err)
	}
	if orders.listUserID != 42 {
		t.Fatalf("tool used user %d, want 42", orders.listUserID)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "list_my_orders" {
		t.Fatalf("unexpected traces: %+v", response.ToolCalls)
	}
	if response.ConversationID == "" || response.Provider != "local" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestAgentCancellationRequiresSingleUseUserBoundConfirmation(t *testing.T) {
	orders := &orderServiceMock{order: &datamodels.Order{ID: 8, UserID: 42, OrderStatus: datamodels.OrderWait}}
	svc := NewService(NewLocalProvider(), orders, &productServiceMock{})
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "帮我取消订单 8"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Confirmation == nil || orders.cancelCallCount != 0 {
		t.Fatalf("expected confirmation without side effect: %+v", response)
	}
	request := ConfirmRequest{ConversationID: response.ConversationID, Token: response.Confirmation.Token}
	if _, err := svc.Confirm(context.Background(), 99, request); !errors.Is(err, ErrInvalidConfirmation) {
		t.Fatalf("cross-user confirmation err=%v", err)
	}
	if orders.cancelCallCount != 0 {
		t.Fatal("cross-user confirmation executed cancellation")
	}
	// A failed ownership attempt consumes the capability token, preventing replay.
	if _, err := svc.Confirm(context.Background(), 42, request); !errors.Is(err, ErrInvalidConfirmation) {
		t.Fatalf("replayed token err=%v", err)
	}

	response, err = svc.Chat(context.Background(), 42, ChatRequest{ConversationID: response.ConversationID, Message: "取消订单 8"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(context.Background(), 42, ConfirmRequest{ConversationID: response.ConversationID, Token: response.Confirmation.Token}); err != nil {
		t.Fatal(err)
	}
	if orders.cancelCallCount != 1 || orders.cancelOrderID != 8 || orders.cancelUserID != 42 {
		t.Fatalf("unexpected cancellation: %+v", orders)
	}
}

func TestAgentConversationOwnershipAndInputLimits(t *testing.T) {
	svc := NewService(NewLocalProvider(), &orderServiceMock{}, &productServiceMock{})
	response, err := svc.Chat(context.Background(), 1, ChatRequest{Message: "你好"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Chat(context.Background(), 2, ChatRequest{ConversationID: response.ConversationID, Message: "继续"}); !errors.Is(err, ErrInvalidConversation) {
		t.Fatalf("cross-user conversation err=%v", err)
	}
	if _, err := svc.Chat(context.Background(), 1, ChatRequest{Message: ""}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("empty message err=%v", err)
	}
}

func TestAgentFiltersLowStockProducts(t *testing.T) {
	products := &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "Low", ProductNum: 3}, {ID: 2, ProductName: "Enough", ProductNum: 20}}}
	svc := NewService(NewLocalProvider(), &orderServiceMock{}, products)
	response, err := svc.Chat(context.Background(), 1, ChatRequest{Message: "哪些商品库存不足？"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 1 || !response.ToolCalls[0].Success {
		t.Fatalf("unexpected traces: %+v", response.ToolCalls)
	}
	var result struct {
		Items []datamodels.Product `json:"items"`
	}
	if err := json.Unmarshal(response.ToolCalls[0].Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != 1 {
		t.Fatalf("unexpected low stock result: %+v", result.Items)
	}
}

func TestAgentMetricsTrackRequestsToolsTokensAndConfirmations(t *testing.T) {
	orders := &orderServiceMock{order: &datamodels.Order{ID: 8, UserID: 42, OrderStatus: datamodels.OrderWait}}
	svc := NewService(NewLocalProvider(), orders, &productServiceMock{})
	if _, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "我的订单"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Chat(context.Background(), 42, ChatRequest{Message: ""}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal(err)
	}
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "取消订单 8"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(context.Background(), 42, ConfirmRequest{ConversationID: response.ConversationID, Token: response.Confirmation.Token}); err != nil {
		t.Fatal(err)
	}
	metrics := svc.Metrics()
	if metrics.Requests != 3 || metrics.SuccessfulRequests != 2 || metrics.FailedRequests != 1 {
		t.Fatalf("unexpected request metrics: %+v", metrics)
	}
	if metrics.ToolCalls != 2 || metrics.Confirmations != 1 {
		t.Fatalf("unexpected tool/confirmation metrics: %+v", metrics)
	}
}

func TestNormalizeOrderListArgumentsKeepsBusinessDefaults(t *testing.T) {
	call := normalizeToolCall(ToolCall{Name: "list_my_orders", Arguments: mustJSON(map[string]any{"page": 0, "page_size": 20})})
	var args map[string]any
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args["page"] != float64(1) || args["page_size"] != float64(10) {
		t.Fatalf("unexpected normalized arguments: %+v", args)
	}
}
