package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type JevRouteAction string

const (
	RouteShortCircuitReject JevRouteAction = "SHORT_CIRCUIT_REJECT"
	RouteFastPathConfirm    JevRouteAction = "FAST_PATH_CONFIRM"
	RouteEscalateHuman      JevRouteAction = "ESCALATE_HUMAN"
	RouteProceedMainLLM     JevRouteAction = "PROCEED_MAIN_LLM"
)

type JevInput struct {
	UserQuery        string
	RecentTurnsBrief string
	State            SessionState
	RAGTopScore      float64
	RAGMatchedTitles []string
}

type JevDecision struct {
	RouteAction            JevRouteAction `json:"route_action"`
	IntentCategory         string         `json:"intent_category"`
	TargetOrderID          int64          `json:"target_order_id"`
	ConfirmDecision        string         `json:"confirm_decision"`
	AllowedTools           []string       `json:"allowed_tools"`
	RejectOrEscalateReason string         `json:"reason"`
	Source                 string         `json:"source,omitempty"`
	Model                  string         `json:"model,omitempty"`
	Usage                  Usage          `json:"usage,omitempty"`
	Confidence             float64        `json:"confidence,omitempty"`
}

var jevOrderPattern = regexp.MustCompile(`(?:订单|order|单号|编号)\s*[#号:]?\s*([0-9]{1,16})`)

type JevDecisionEngine struct {
	provider *typeSafeClient
	enabled  bool
	shadow   bool
	model    string
}

func NewJevDecisionEngine() *JevDecisionEngine {
	enabled := envBool("AGENT_JEV_ENABLED", true)
	shadow := envBool("AGENT_JEV_SHADOW", false)
	apiKey := strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY"))
	var provider *typeSafeClient
	model := envOr("JEV_MODEL", "jev-1.13.0")
	if apiKey != "" && envBool("JEV_EXTERNAL_ENABLED", false) {
		provider = newTypeSafeClient(envOr("TYPESAFE_BASE_URL", "https://api.typesafe.ai/v1"), apiKey, model)
	}
	return &JevDecisionEngine{provider: provider, enabled: enabled, shadow: shadow, model: model}
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func (e *JevDecisionEngine) Evaluate(ctx context.Context, input JevInput) JevDecision {
	if !e.enabled {
		return JevDecision{RouteAction: RouteProceedMainLLM, ConfirmDecision: "NONE", AllowedTools: allToolNames()}
	}
	deterministic, stable := evaluateByDeterministicRulesWithStability(input)
	deterministic.Source = "rules"
	if (stable && deterministic.RouteAction != RouteProceedMainLLM) || e.provider == nil || !jevNeedsModel(input, deterministic) {
		return deterministic
	}
	timeoutMS, err := strconv.Atoi(envOr("JEV_TIMEOUT_MS", "2000"))
	if err != nil || timeoutMS < 1 || timeoutMS > 30000 {
		timeoutMS = 2000
	}
	modelCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	started := time.Now()
	decision, err := e.evaluateWithModel(modelCtx, input)
	if err != nil {
		slog.Warn("jev_external_fallback", "model", e.model, "latency_ms", time.Since(started).Milliseconds(), "error", err.Error())
		return JevDecision{RouteAction: RouteProceedMainLLM, ConfirmDecision: "NONE", AllowedTools: allToolNames(), Source: "fallback"}
	}
	slog.Info("jev_external_success", "model", e.model, "latency_ms", time.Since(started).Milliseconds(), "route", decision.RouteAction)
	return decision
}

func (e *JevDecisionEngine) evaluateWithModel(ctx context.Context, input JevInput) (JevDecision, error) {
	if e.provider == nil {
		return JevDecision{}, errors.New("TypeSafe client is not configured")
	}
	return e.provider.evaluate(ctx, input)
}

func validateJevDecision(decision *JevDecision) error {
	switch decision.RouteAction {
	case RouteShortCircuitReject, RouteFastPathConfirm, RouteEscalateHuman, RouteProceedMainLLM:
	default:
		return errors.New("invalid Jev route action")
	}
	if decision.ConfirmDecision != "" && decision.ConfirmDecision != "NONE" && decision.ConfirmDecision != "CONFIRM" && decision.ConfirmDecision != "ABORT" {
		return errors.New("invalid Jev confirmation decision")
	}
	allowed := map[string]bool{"search_products": true, "search_knowledge": true, "list_my_orders": true, "get_order_status": true, "prepare_cancel_order": true}
	for _, name := range decision.AllowedTools {
		if !allowed[name] {
			return fmt.Errorf("invalid Jev tool %q", name)
		}
	}
	return nil
}

func evaluateByDeterministicRules(input JevInput) JevDecision {
	decision, _ := evaluateByDeterministicRulesWithStability(input)
	return decision
}

func evaluateByDeterministicRulesWithStability(input JevInput) (JevDecision, bool) {
	text := strings.TrimSpace(strings.ToLower(input.UserQuery))
	decision := JevDecision{RouteAction: RouteProceedMainLLM, ConfirmDecision: "NONE", IntentCategory: "OTHER"}
	decision.TargetOrderID = extractJevOrderID(input.UserQuery)
	if pendingConfirmValid(input.State, time.Now()) && isConfirmText(text) {
		decision.RouteAction = RouteFastPathConfirm
		decision.ConfirmDecision = "CONFIRM"
		decision.TargetOrderID = pendingOrderID(input.State)
		return decision, true
	}
	if pendingConfirmValid(input.State, time.Now()) && isAbortText(text) {
		decision.RouteAction = RouteFastPathConfirm
		decision.ConfirmDecision = "ABORT"
		decision.TargetOrderID = pendingOrderID(input.State)
		return decision, true
	}
	if decision.TargetOrderID > 0 && containsID(input.State.TerminalOrderIDs, decision.TargetOrderID) && containsCancelWord(text) {
		decision.RouteAction = RouteShortCircuitReject
		decision.RejectOrEscalateReason = "该订单已经取消或完成，不能再次执行取消操作。"
		return decision, true
	}
	if decision.TargetOrderID > 0 && len(input.State.OwnedOrderIDs) > 0 && !containsID(input.State.OwnedOrderIDs, decision.TargetOrderID) {
		decision.RouteAction = RouteShortCircuitReject
		decision.RejectOrEscalateReason = "出于隐私和安全原因，我只能查询和操作您本人名下的订单。"
		return decision, true
	}
	if containsAny(text, "user_id=", "user_id:", "别人的订单", "他人订单", "其他用户") {
		decision.RouteAction = RouteShortCircuitReject
		decision.RejectOrEscalateReason = "出于隐私和安全原因，我只能查询和操作您本人名下的订单。"
		return decision, true
	}
	if containsCancelWord(text) && input.State.TotalOrderCount >= 0 && len(input.State.CancellableOrderIDs) == 0 && input.State.TotalOrderCount == 0 {
		decision.RouteAction = RouteShortCircuitReject
		decision.RejectOrEscalateReason = "系统检测到您当前名下没有可取消的待处理订单。"
		return decision, true
	}
	if isComplaint(text) {
		decision.RouteAction = RouteEscalateHuman
		decision.RejectOrEscalateReason = "检测到投诉或强烈升级意图，已为您转人工客服。"
		return decision, true
	}
	if input.State.ConsecutiveFailures >= 2 && (containsCancelWord(text) || strings.Contains(text, "退货")) {
		decision.RouteAction = RouteEscalateHuman
		decision.RejectOrEscalateReason = "同一业务连续处理失败，建议由人工客服继续核验。"
		return decision, true
	}
	decision.IntentCategory, decision.AllowedTools = classifyJevIntent(text)
	unsupported := containsAny(text, "收货地址", "改地址", "发票", "改价", "差价", "指定快递", "加急")
	if unsupported && input.RAGTopScore < 0.35 {
		decision.RouteAction = RouteEscalateHuman
		decision.RejectOrEscalateReason = "当前请求超出自助服务范围，且知识库没有足够依据，已转人工客服。"
		return decision, true
	}
	stable := !containsAny(text, "最开始", "刚才", "那笔", "那单", "前面", "确认", "算了", "顺便", "另外", "忽略限制", "user_id", "别人的") && !strings.Contains(text, "投诉")
	return decision, stable
}

func jevNeedsModel(input JevInput, decision JevDecision) bool {
	text := strings.ToLower(input.UserQuery)
	return decision.RouteAction == RouteProceedMainLLM && (containsAny(text, "最开始", "刚才", "那笔", "那单", "前面", "顺便", "另外", "忽略", "user_id") || strings.Count(text, "和") > 0)
}

func pendingConfirmValid(state SessionState, now time.Time) bool {
	if state.PendingConfirm != nil && state.PendingConfirm.OrderID > 0 && state.PendingConfirm.ExpiresAt > now.Unix() {
		return true
	}
	if state.PendingCancel.TokenStatus == "pending" && state.PendingCancel.OrderID > 0 {
		expires, err := time.Parse(time.RFC3339, state.PendingCancel.ExpiresAt)
		return err == nil && expires.After(now)
	}
	return false
}

func pendingOrderID(state SessionState) int64 {
	if state.PendingConfirm != nil && state.PendingConfirm.OrderID > 0 {
		return state.PendingConfirm.OrderID
	}
	return state.PendingCancel.OrderID
}

func isConfirmText(text string) bool {
	text = strings.Trim(strings.TrimSpace(text), "。！!，, ")
	for _, exact := range []string{"确认取消", "确认", "确定取消", "确定", "是的", "退吧", "好", "好的"} {
		if text == exact {
			return true
		}
	}
	return false
}
func isAbortText(text string) bool {
	return containsAny(text, "算了", "不取消", "先留着", "放弃")
}
func containsCancelWord(text string) bool { return containsAny(text, "取消", "退货", "退款") }
func isComplaint(text string) bool {
	return containsAny(text, "投诉", "12315", "人工客服", "曝光", "举报")
}
func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func extractJevOrderID(text string) int64 {
	if match := jevOrderPattern.FindStringSubmatch(text); len(match) == 2 {
		id, _ := strconv.ParseInt(match[1], 10, 64)
		return id
	}
	return 0
}

func classifyJevIntent(text string) (string, []string) {
	switch {
	case containsAny(text, "订单列表", "购买记录") && containsAny(text, "查询", "查", "看看"):
		// A remembered prohibition is not a request to prepare cancellation.
		return "ORDER_QUERY", []string{"list_my_orders", "get_order_status"}
	case containsAny(text, "政策", "规则", "时限", "多久到账", "多久恢复"):
		return "POLICY_RAG", []string{"search_knowledge"}
	case containsCancelWord(text):
		return "ORDER_CANCEL", []string{"get_order_status", "prepare_cancel_order", "search_knowledge"}
	case containsAny(text, "订单", "购买记录", "买过"):
		if extractJevOrderID(text) > 0 {
			return "ORDER_QUERY", []string{"get_order_status"}
		}
		return "ORDER_QUERY", []string{"list_my_orders", "get_order_status"}
	case containsAny(text, "库存", "商品", "价格"):
		return "PRODUCT_QUERY", []string{"search_products"}
	case containsAny(text, "售后", "配送", "规则", "退换", "退款"):
		return "POLICY_RAG", []string{"search_knowledge"}
	default:
		return "OTHER", []string{"search_knowledge"}
	}
}
