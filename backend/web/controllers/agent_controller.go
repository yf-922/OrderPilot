package controllers

import (
	"errors"
	"strings"

	"product/agent"
	"product/common"
	"product/repositories"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/mvc"
)

type AgentController struct {
	Ctx          iris.Context
	AgentService agent.ChatService
}

func (a *AgentController) Get() mvc.View {
	return mvc.View{Name: "agent/view.html", Data: iris.Map{"Title": "订单客服 Agent"}}
}

func (a *AgentController) GetStatus() {
	_ = a.Ctx.JSON(common.NewSuccessResult("ok", a.AgentService.Status()))
}

func (a *AgentController) GetMetrics() {
	metrics, ok := a.AgentService.(interface{ Metrics() agent.Metrics })
	if !ok {
		a.Ctx.StatusCode(iris.StatusNotImplemented)
		_ = a.Ctx.JSON(common.NewFailResult("metrics unavailable"))
		return
	}
	_ = a.Ctx.JSON(common.NewSuccessResult("ok", metrics.Metrics()))
}

func (a *AgentController) PostChat() {
	var request agent.ChatRequest
	if err := a.Ctx.ReadJSON(&request); err != nil {
		a.Ctx.StatusCode(iris.StatusBadRequest)
		_ = a.Ctx.JSON(common.NewFailResult("invalid JSON request"))
		return
	}
	userID, _ := common.AuthenticatedUserID(a.Ctx)
	response, err := a.AgentService.Chat(a.Ctx.Request().Context(), userID, request)
	if err != nil {
		a.writeError(err)
		return
	}
	_ = a.Ctx.JSON(common.NewSuccessResult("agent response", response))
}

func (a *AgentController) PostConfirm() {
	var request agent.ConfirmRequest
	if err := a.Ctx.ReadJSON(&request); err != nil {
		a.Ctx.StatusCode(iris.StatusBadRequest)
		_ = a.Ctx.JSON(common.NewFailResult("invalid JSON request"))
		return
	}
	userID, _ := common.AuthenticatedUserID(a.Ctx)
	response, err := a.AgentService.Confirm(a.Ctx.Request().Context(), userID, request)
	if err != nil {
		a.writeError(err)
		return
	}
	_ = a.Ctx.JSON(common.NewSuccessResult("action confirmed", response))
}

func (a *AgentController) PostReset() {
	var request struct {
		ConversationID string `json:"conversationId"`
	}
	if err := a.Ctx.ReadJSON(&request); err != nil || strings.TrimSpace(request.ConversationID) == "" {
		a.Ctx.StatusCode(iris.StatusBadRequest)
		_ = a.Ctx.JSON(common.NewFailResult("conversationId is required"))
		return
	}
	userID, _ := common.AuthenticatedUserID(a.Ctx)
	a.AgentService.Reset(userID, request.ConversationID)
	_ = a.Ctx.JSON(common.NewSuccessResult("conversation reset", nil))
}

func (a *AgentController) writeError(err error) {
	switch {
	case errors.Is(err, agent.ErrInvalidMessage), errors.Is(err, agent.ErrInvalidConversation), errors.Is(err, agent.ErrInvalidConfirmation):
		a.Ctx.StatusCode(iris.StatusBadRequest)
	case errors.Is(err, repositories.ErrOrderNotFound):
		a.Ctx.StatusCode(iris.StatusNotFound)
	case errors.Is(err, repositories.ErrInvalidOrderState):
		a.Ctx.StatusCode(iris.StatusConflict)
	default:
		a.Ctx.StatusCode(iris.StatusBadGateway)
		_ = a.Ctx.JSON(common.NewFailResult("AI provider is temporarily unavailable"))
		return
	}
	_ = a.Ctx.JSON(common.NewFailResult(err.Error()))
}
