package services

import (
	"context"
	"errors"
	"testing"

	"product/datamodels"
	"product/repositories"
)

type orderRepositoryMock struct {
	createErr  error
	statusErr  error
	cancelErr  error
	lastLimit  int
	lastOffset int
}

func (m *orderRepositoryMock) Conn() error { return nil }
func (m *orderRepositoryMock) CreateWithStock(context.Context, *datamodels.Order) (int64, error) {
	return 12, m.createErr
}
func (m *orderRepositoryMock) SelectByKeyForUser(context.Context, int64, int64) (*datamodels.Order, error) {
	return &datamodels.Order{ID: 1}, nil
}
func (m *orderRepositoryMock) ListByUser(_ context.Context, _ int64, limit, offset int) ([]*datamodels.Order, int64, error) {
	m.lastLimit, m.lastOffset = limit, offset
	return []*datamodels.Order{}, 0, nil
}
func (m *orderRepositoryMock) UpdateStatus(context.Context, int64, int64, int64) error {
	return m.statusErr
}
func (m *orderRepositoryMock) CancelAndRestock(context.Context, int64, int64) error {
	return m.cancelErr
}

type productServiceMock struct{ invalidated int }

func (m *productServiceMock) GetProductById(int64) (*datamodels.Product, error) { return nil, nil }
func (m *productServiceMock) GetProductAll() ([]*datamodels.Product, error)     { return nil, nil }
func (m *productServiceMock) DeleteProductById(int64) error                     { return nil }
func (m *productServiceMock) InsertProduct(*datamodels.Product) (int64, error)  { return 1, nil }
func (m *productServiceMock) UpdateProduct(*datamodels.Product) error           { return nil }
func (m *productServiceMock) InvalidateCache()                                  { m.invalidated++ }
func (m *productServiceMock) ListProducts(int, int, string) ([]*datamodels.Product, int64, error) {
	return nil, 0, nil
}

func TestOrderServiceMapsRepositoryErrors(t *testing.T) {
	product := &productServiceMock{}
	repo := &orderRepositoryMock{createErr: repositories.ErrInsufficientStock}
	svc := NewOrderService(repo, product)
	_, err := svc.InsertOrder(context.Background(), &datamodels.Order{UserID: 1, ProductId: 1, Quantity: 2, OrderStatus: datamodels.OrderWait, IdempotencyKey: "valid-key"})
	if !errors.Is(err, ErrOrderInsufficientStock) {
		t.Fatalf("expected mapped stock error, got %v", err)
	}
}

func TestOrderServiceRejectsInvalidOrderBeforeRepository(t *testing.T) {
	svc := NewOrderService(&orderRepositoryMock{}, &productServiceMock{})
	tests := []*datamodels.Order{
		nil,
		{UserID: 0, ProductId: 1, Quantity: 1, OrderStatus: datamodels.OrderWait, IdempotencyKey: "valid-key"},
		{UserID: 1, ProductId: 1, Quantity: 0, OrderStatus: datamodels.OrderWait, IdempotencyKey: "valid-key"},
		{UserID: 1, ProductId: 1, Quantity: 1, OrderStatus: datamodels.OrderSuccess, IdempotencyKey: "valid-key"},
		{UserID: 1, ProductId: 1, Quantity: 1, OrderStatus: datamodels.OrderWait, IdempotencyKey: "short"},
	}
	for _, order := range tests {
		if _, err := svc.InsertOrder(context.Background(), order); !errors.Is(err, ErrInvalidOrder) {
			t.Fatalf("order=%+v expected invalid order, got %v", order, err)
		}
	}
}

func TestOrderServiceStatusRulesAndCacheInvalidation(t *testing.T) {
	product := &productServiceMock{}
	repo := &orderRepositoryMock{}
	svc := NewOrderService(repo, product)
	if err := svc.UpdateOrderStatus(context.Background(), 1, 1, datamodels.OrderWait); !errors.Is(err, repositories.ErrInvalidOrderState) {
		t.Fatalf("expected invalid state error, got %v", err)
	}
	if err := svc.CancelOrder(context.Background(), 1, 1); err != nil || product.invalidated != 1 {
		t.Fatalf("cancel err=%v invalidations=%d", err, product.invalidated)
	}
}
