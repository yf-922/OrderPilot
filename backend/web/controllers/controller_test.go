package controllers

import (
	"context"
	"testing"

	"product/agent"
	"product/common"
	"product/datamodels"
	"product/repositories"
	"product/services"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/httptest"
	"github.com/kataras/iris/v12/mvc"
)

type userServiceHTTPMock struct {
	loginUser *datamodels.User
	loginOK   bool
	addErr    error
	updateErr error
}

func (m *userServiceHTTPMock) IsPwdSuccess(string, string) (*datamodels.User, bool) {
	return m.loginUser, m.loginOK
}
func (m *userServiceHTTPMock) AddUser(*datamodels.User) (int64, error)  { return 5, m.addErr }
func (m *userServiceHTTPMock) GetAllUsers() ([]*datamodels.User, error) { return nil, nil }
func (m *userServiceHTTPMock) GetUserById(id int64) (*datamodels.User, error) {
	return &datamodels.User{ID: int(id), Username: "demo"}, nil
}
func (m *userServiceHTTPMock) DeleteUserById(int64) error        { return nil }
func (m *userServiceHTTPMock) UpdateUser(*datamodels.User) error { return m.updateErr }

type productServiceHTTPMock struct {
	listErr error
	getErr  error
}

func (m *productServiceHTTPMock) GetProductById(id int64) (*datamodels.Product, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &datamodels.Product{ID: id, ProductName: "Go"}, nil
}
func (m *productServiceHTTPMock) GetProductAll() ([]*datamodels.Product, error) { return nil, nil }
func (m *productServiceHTTPMock) DeleteProductById(int64) error                 { return nil }
func (m *productServiceHTTPMock) InsertProduct(*datamodels.Product) (int64, error) {
	return 8, nil
}
func (m *productServiceHTTPMock) UpdateProduct(*datamodels.Product) error { return nil }
func (m *productServiceHTTPMock) InvalidateCache()                        {}
func (m *productServiceHTTPMock) ListProducts(int, int, string) ([]*datamodels.Product, int64, error) {
	return []*datamodels.Product{{ID: 1, ProductName: "Go"}}, 1, m.listErr
}

type orderServiceHTTPMock struct{ createErr error }

func (m *orderServiceHTTPMock) InsertOrder(context.Context, *datamodels.Order) (int64, error) {
	return 11, m.createErr
}
func (m *orderServiceHTTPMock) GetOrderForUser(context.Context, int64, int64) (*datamodels.Order, error) {
	return &datamodels.Order{ID: 1, UserID: 1}, nil
}
func (m *orderServiceHTTPMock) ListOrdersForUser(context.Context, int64, int, int) ([]*datamodels.Order, int64, error) {
	return []*datamodels.Order{}, 0, nil
}
func (m *orderServiceHTTPMock) UpdateOrderStatus(context.Context, int64, int64, int64) error {
	return nil
}
func (m *orderServiceHTTPMock) CancelOrder(context.Context, int64, int64) error { return nil }

func mountMVC(prefix string, auth bool, dependency any, controller any) *iris.Application {
	app := iris.New()
	party := app.Party(prefix)
	if auth {
		party.Use(common.RequireAuth)
	}
	m := mvc.New(party)
	m.Register(dependency)
	m.Handle(controller)
	return app
}

func bearer(userID int64) string { return "Bearer " + common.IssueToken(userID) }

func TestUserControllerLoginAndOwnership(t *testing.T) {
	service := &userServiceHTTPMock{loginUser: &datamodels.User{ID: 1, Username: "demo"}, loginOK: true}
	e := httptest.New(t, mountMVC("/user", false, service, new(UserController)))
	e.POST("/user/login").WithFormField("userName", "demo").WithFormField("password", "secret123").Expect().Status(iris.StatusOK).Body().Contains("\"success\":true").Contains("\"token\"")
	e.GET("/user/2").WithHeader("Authorization", bearer(1)).Expect().Status(iris.StatusForbidden).Body().Contains("cannot access another user")
	e.GET("/user/1").WithHeader("Authorization", bearer(1)).Expect().Status(iris.StatusOK).Body().Contains("\"userName\":\"demo\"")
}

func TestUserControllerMapsDuplicateUpdate(t *testing.T) {
	service := &userServiceHTTPMock{updateErr: services.ErrUserNameExists}
	e := httptest.New(t, mountMVC("/user", false, service, new(UserController)))
	e.POST("/user/update").WithHeader("Authorization", bearer(1)).WithFormField("id", "1").WithFormField("nickName", "Demo").WithFormField("userName", "taken").WithFormField("password", "secret123").Expect().Status(iris.StatusConflict).Body().Contains("userName already exists")
}

func TestProductControllerAuthAndNotFound(t *testing.T) {
	service := &productServiceHTTPMock{getErr: repositories.ErrProductNotFound}
	e := httptest.New(t, mountMVC("/product", true, service, new(ProductController)))
	e.GET("/product/list").Expect().Status(iris.StatusUnauthorized)
	e.GET("/product/list").WithHeader("Authorization", bearer(1)).Expect().Status(iris.StatusOK).Body().Contains("\"total\":1")
	e.GET("/product/99").WithHeader("Authorization", bearer(1)).Expect().Status(iris.StatusNotFound).Body().Contains("product not found")
}

func TestOrderControllerOwnershipAndIdempotencyConflict(t *testing.T) {
	service := &orderServiceHTTPMock{createErr: services.ErrOrderIdempotencyConflict}
	e := httptest.New(t, mountMVC("/order", true, service, new(OrderController)))
	e.POST("/order/create").WithHeader("Authorization", bearer(1)).WithHeader("Idempotency-Key", "request-key-123").WithFormField("userID", "2").WithFormField("productID", "1").WithFormField("quantity", "1").WithFormField("orderStatus", "0").Expect().Status(iris.StatusForbidden)
	e.POST("/order/create").WithHeader("Authorization", bearer(1)).WithHeader("Idempotency-Key", "request-key-123").WithFormField("userID", "1").WithFormField("productID", "1").WithFormField("quantity", "1").WithFormField("orderStatus", "0").Expect().Status(iris.StatusConflict).Body().Contains("different request")
}

func TestOrderControllerMapsUnexpectedServiceValidation(t *testing.T) {
	service := &orderServiceHTTPMock{createErr: services.ErrInvalidOrder}
	e := httptest.New(t, mountMVC("/order", true, service, new(OrderController)))
	e.POST("/order/create").WithHeader("Authorization", bearer(1)).WithHeader("Idempotency-Key", "request-key-123").WithFormField("userID", "1").WithFormField("productID", "1").WithFormField("quantity", "1").WithFormField("orderStatus", "0").Expect().Status(iris.StatusBadRequest).Body().Contains("invalid order request")
}

type agentServiceHTTPMock struct {
	userID int64
}

func (m *agentServiceHTTPMock) Status() agent.Status {
	return agent.Status{Provider: "local", Model: "demo", Mode: "demo"}
}
func (m *agentServiceHTTPMock) Chat(_ context.Context, userID int64, request agent.ChatRequest) (agent.ChatResponse, error) {
	m.userID = userID
	return agent.ChatResponse{ConversationID: "chat-1", Message: "订单查询完成", Provider: "local", Model: "demo"}, nil
}
func (m *agentServiceHTTPMock) Confirm(context.Context, int64, agent.ConfirmRequest) (agent.ChatResponse, error) {
	return agent.ChatResponse{ConversationID: "chat-1", Message: "订单已取消"}, nil
}
func (m *agentServiceHTTPMock) Reset(int64, string) {}

func TestAgentControllerAuthenticationAndIdentityInjection(t *testing.T) {
	service := &agentServiceHTTPMock{}
	e := httptest.New(t, mountMVC("/agent", true, service, new(AgentController)))
	e.GET("/agent/status").Expect().Status(iris.StatusUnauthorized)
	e.POST("/agent/chat").WithJSON(agent.ChatRequest{Message: "我的订单"}).Expect().Status(iris.StatusUnauthorized)
	e.POST("/agent/chat").WithHeader("Authorization", bearer(42)).WithJSON(agent.ChatRequest{Message: "我的订单"}).Expect().Status(iris.StatusOK).Body().Contains("订单查询完成")
	if service.userID != 42 {
		t.Fatalf("controller injected user %d, want 42", service.userID)
	}
}
