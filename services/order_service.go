package services

import (
	"context"
	"errors"
	"product/datamodels"
	"product/repositories"
)

var ErrOrderUserNotFound = errors.New("user not found")
var ErrOrderProductNotFound = errors.New("product not found")
var ErrOrderInsufficientStock = errors.New("insufficient stock")

// IOrderService 定义订单业务层对外提供的方法。
type IOrderService interface {
	InsertOrder(context.Context, *datamodels.Order) (int64, error)
	GetOrderForUser(context.Context, int64, int64) (*datamodels.Order, error)
	ListOrdersForUser(context.Context, int64, int, int) ([]*datamodels.Order, int64, error)
	UpdateOrderStatus(context.Context, int64, int64, int64) error
	CancelOrder(context.Context, int64, int64) error
}

func (o *OrderService) GetOrderForUser(ctx context.Context, orderID, userID int64) (*datamodels.Order, error) {
	return o.OrderRepository.SelectByKeyForUser(ctx, orderID, userID)
}

func (o *OrderService) ListOrdersForUser(ctx context.Context, userID int64, page, pageSize int) ([]*datamodels.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return o.OrderRepository.ListByUser(ctx, userID, pageSize, (page-1)*pageSize)
}

func (o *OrderService) UpdateOrderStatus(ctx context.Context, orderID, userID, status int64) error {
	if status != datamodels.OrderSuccess && status != datamodels.OrderFailed {
		return repositories.ErrInvalidOrderState
	}
	if status == datamodels.OrderFailed {
		return o.CancelOrder(ctx, orderID, userID)
	}
	return o.OrderRepository.UpdateStatus(ctx, orderID, userID, status)
}

func (o *OrderService) CancelOrder(ctx context.Context, orderID, userID int64) error {
	err := o.OrderRepository.CancelAndRestock(ctx, orderID, userID)
	if err == nil {
		o.ProductService.InvalidateCache()
	}
	return err
}

// NewOrderService 创建订单业务层实例。
func NewOrderService(orderRepository repositories.OrderRepository, productService IProductService) IOrderService {
	return &OrderService{
		OrderRepository: orderRepository,
		ProductService:  productService,
	}
}

// OrderService 是订单业务层实现。
type OrderService struct {
	OrderRepository repositories.OrderRepository
	ProductService  IProductService
}

// InsertOrder 新增订单。
func (o *OrderService) InsertOrder(ctx context.Context, order *datamodels.Order) (int64, error) {
	if order.Quantity <= 0 {
		order.Quantity = 1
	}
	id, err := o.OrderRepository.CreateWithStock(ctx, order)
	if errors.Is(err, repositories.ErrOrderUserNotFound) {
		return 0, ErrOrderUserNotFound
	}
	if errors.Is(err, repositories.ErrProductNotFound) {
		return 0, ErrOrderProductNotFound
	}
	if errors.Is(err, repositories.ErrInsufficientStock) {
		return 0, ErrOrderInsufficientStock
	}
	if err == nil {
		o.ProductService.InvalidateCache()
	}
	return id, err
}
