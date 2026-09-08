package controllers

import (
	"errors"
	"product/common"
	"product/repositories"
	"product/services"
	"strconv"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/mvc"
)

type OrderController struct {
	Ctx          iris.Context
	OrderService services.IOrderService
}

func (o *OrderController) GetBy(id int64) {
	userID, _ := common.AuthenticatedUserID(o.Ctx)
	order, err := o.OrderService.GetOrderForUser(o.Ctx.Request().Context(), id, userID)
	if err != nil {
		o.Ctx.StatusCode(iris.StatusNotFound)
		_ = o.Ctx.JSON(common.NewFailResult("order not found"))
		return
	}
	_ = o.Ctx.JSON(common.NewSuccessResult("ok", order))
}

func (o *OrderController) GetList() {
	userID, _ := common.AuthenticatedUserID(o.Ctx)
	page, _ := strconv.Atoi(o.Ctx.URLParamDefault("page", "1"))
	pageSize, _ := strconv.Atoi(o.Ctx.URLParamDefault("pageSize", "20"))
	orders, total, err := o.OrderService.ListOrdersForUser(o.Ctx.Request().Context(), userID, page, pageSize)
	if err != nil {
		o.Ctx.StatusCode(iris.StatusInternalServerError)
		_ = o.Ctx.JSON(common.NewFailResult("order list failed"))
		return
	}
	_ = o.Ctx.JSON(common.NewSuccessResult("ok", iris.Map{"items": orders, "page": page, "pageSize": pageSize, "total": total}))
}

func (o *OrderController) Get() mvc.View {
	return mvc.View{
		Name: "order/view.html",
		Data: iris.Map{"order": []map[string]any{}},
	}
}

func (o *OrderController) PostCreate() {
	order, err := common.BuildOrderForCreateFromContext(o.Ctx)
	if err != nil {
		o.Ctx.StatusCode(iris.StatusBadRequest)
		_ = o.Ctx.JSON(common.NewFailResult(err.Error()))
		return
	}
	authUserID, ok := common.AuthenticatedUserID(o.Ctx)
	if !ok || authUserID != order.UserID {
		o.Ctx.StatusCode(iris.StatusForbidden)
		_ = o.Ctx.JSON(common.NewFailResult("cannot create an order for another user"))
		return
	}
	order.IdempotencyKey = o.Ctx.GetHeader("Idempotency-Key")
	if len(order.IdempotencyKey) < 8 || len(order.IdempotencyKey) > 128 {
		o.Ctx.StatusCode(iris.StatusBadRequest)
		_ = o.Ctx.JSON(common.NewFailResult("Idempotency-Key header must be between 8 and 128 characters"))
		return
	}
	orderID, err := o.OrderService.InsertOrder(o.Ctx.Request().Context(), order)
	if err != nil {
		if errors.Is(err, services.ErrOrderUserNotFound) {
			o.Ctx.StatusCode(iris.StatusBadRequest)
			_ = o.Ctx.JSON(common.NewFailResult("user not found"))
			return
		}
		if errors.Is(err, services.ErrOrderProductNotFound) {
			o.Ctx.StatusCode(iris.StatusBadRequest)
			_ = o.Ctx.JSON(common.NewFailResult("product not found"))
			return
		}
		if errors.Is(err, services.ErrOrderInsufficientStock) {
			o.Ctx.StatusCode(iris.StatusConflict)
			_ = o.Ctx.JSON(common.NewFailResult("insufficient stock"))
			return
		}
		if errors.Is(err, services.ErrOrderIdempotencyConflict) {
			o.Ctx.StatusCode(iris.StatusConflict)
			_ = o.Ctx.JSON(common.NewFailResult("Idempotency-Key was already used with a different request"))
			return
		}
		if errors.Is(err, services.ErrInvalidOrder) {
			o.Ctx.StatusCode(iris.StatusBadRequest)
			_ = o.Ctx.JSON(common.NewFailResult("invalid order request"))
			return
		}
		o.Ctx.StatusCode(iris.StatusInternalServerError)
		_ = o.Ctx.JSON(common.NewFailResult("订单创建失败"))
		return
	}
	_ = o.Ctx.JSON(common.NewSuccessResult("order created", iris.Map{
		"orderID":     orderID,
		"userID":      order.UserID,
		"productId":   order.ProductId,
		"quantity":    order.Quantity,
		"orderStatus": order.OrderStatus,
	}))
}

func (o *OrderController) PostUpdate() {
	id, err := strconv.ParseInt(o.Ctx.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		o.Ctx.StatusCode(iris.StatusBadRequest)
		_ = o.Ctx.JSON(common.NewFailResult("id must be a positive integer"))
		return
	}
	status, err := strconv.ParseInt(o.Ctx.FormValue("orderStatus"), 10, 64)
	if err != nil {
		o.Ctx.StatusCode(iris.StatusBadRequest)
		_ = o.Ctx.JSON(common.NewFailResult("orderStatus must be an integer"))
		return
	}
	userID, _ := common.AuthenticatedUserID(o.Ctx)
	if err = o.OrderService.UpdateOrderStatus(o.Ctx.Request().Context(), id, userID, status); err != nil {
		if errors.Is(err, repositories.ErrOrderNotFound) {
			o.Ctx.StatusCode(iris.StatusNotFound)
			_ = o.Ctx.JSON(common.NewFailResult("order not found"))
			return
		}
		o.Ctx.StatusCode(iris.StatusConflict)
		_ = o.Ctx.JSON(common.NewFailResult("invalid order state transition"))
		return
	}
	_ = o.Ctx.JSON(common.NewSuccessResult("order status updated", nil))
}

func (o *OrderController) PostDelete() {
	id, err := strconv.ParseInt(o.Ctx.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		o.Ctx.StatusCode(iris.StatusBadRequest)
		_ = o.Ctx.JSON(common.NewFailResult("id must be a positive integer"))
		return
	}
	userID, _ := common.AuthenticatedUserID(o.Ctx)
	if err = o.OrderService.CancelOrder(o.Ctx.Request().Context(), id, userID); err != nil {
		if errors.Is(err, repositories.ErrOrderNotFound) {
			o.Ctx.StatusCode(iris.StatusNotFound)
		} else {
			o.Ctx.StatusCode(iris.StatusConflict)
		}
		_ = o.Ctx.JSON(common.NewFailResult(err.Error()))
		return
	}
	_ = o.Ctx.JSON(common.NewSuccessResult("order cancelled and stock restored", nil))
}
