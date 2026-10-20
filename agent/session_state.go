package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// State survives history pruning, but is scoped to the authenticated conversation.
// User intent is never treated as an authorization or proof of completed work.
type SessionState struct {
	// The fields below are the compact, deterministic state slot used by the
	// Jev router and the context compressor. They are additive so old Redis
	// snapshots continue to decode correctly.
	UserID              int64                 `json:"user_id,omitempty"`
	TotalOrderCount     int                   `json:"total_order_count,omitempty"`
	OrdersQueried       bool                  `json:"orders_queried,omitempty"`
	OwnedOrderIDs       []int64               `json:"owned_order_ids"`
	CancellableOrderIDs []int64               `json:"cancellable_order_ids"`
	TerminalOrderIDs    []int64               `json:"terminal_order_ids"`
	FocusedProductIDs   []int64               `json:"focused_product_ids"`
	PendingConfirm      *ConfirmTokenSnapshot `json:"pending_confirm,omitempty"`
	ConsecutiveFailures int                   `json:"consecutive_failures,omitempty"`
	LastFailedOrderID   int64                 `json:"last_failed_order_id,omitempty"`
	RollingSummary      string                `json:"rolling_summary,omitempty"`
	Version             int64                 `json:"version,omitempty"`
	UpdatedAt           int64                 `json:"updated_at,omitempty"`

	// ActiveOrderIDs and ActiveProductIDs are deterministic anchors collected
	// from authenticated tool results. They survive history compaction.
	ActiveOrderIDs      []int64           `json:"activeOrderIds,omitempty"`
	ActiveProductIDs    []int64           `json:"activeProductIds,omitempty"`
	PendingCancel       ConfirmTokenInfo  `json:"pendingCancel,omitempty"`
	TargetOrderID       int64             `json:"targetOrderId,omitempty"`
	ProtectedOrderIDs   []int64           `json:"protectedOrderIds,omitempty"`
	AddressLocked       bool              `json:"addressLocked,omitempty"`
	CancelPaused        bool              `json:"cancelPaused,omitempty"`
	PendingOrderID      int64             `json:"pendingOrderId,omitempty"`
	LastVerifiedOrderID int64             `json:"lastVerifiedOrderId,omitempty"`
	LastVerifiedStatus  string            `json:"lastVerifiedStatus,omitempty"`
	ConfirmedActions    []ConfirmedAction `json:"confirmedActions,omitempty"`
}

type ConfirmTokenSnapshot struct {
	Token     string `json:"token,omitempty"`
	OrderID   int64  `json:"order_id,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Status    string `json:"status,omitempty"`
}

// MarshalJSON keeps the zero-value state genuinely lightweight. Go's default
// encoder does not omit a non-pointer struct even with `omitempty`.
func (s SessionState) MarshalJSON() ([]byte, error) {
	type sessionStateAlias SessionState
	encoded, err := json.Marshal(sessionStateAlias(s))
	if err != nil {
		return nil, err
	}
	if s.PendingCancel == (ConfirmTokenInfo{}) {
		var payload map[string]any
		if err := json.Unmarshal(encoded, &payload); err != nil {
			return nil, err
		}
		delete(payload, "pendingCancel")
		return json.Marshal(payload)
	}
	return encoded, nil
}

// ConfirmTokenInfo intentionally carries token lifecycle metadata, rather
// than relying on a model-generated summary to remember a pending action.
type ConfirmTokenInfo struct {
	OrderID          int64  `json:"orderId,omitempty"`
	Token            string `json:"token,omitempty"`
	TokenStatus      string `json:"tokenStatus,omitempty"`
	ExpiresAt        string `json:"expiresAt,omitempty"`
	RemainingSeconds int64  `json:"remainingSeconds,omitempty"`
}

type ConfirmedAction struct {
	OrderID int64  `json:"orderId"`
	Action  string `json:"action"`
}

var protectedOrderPattern = regexp.MustCompile(`(?:不要动|不要操作|不能取消|别动|别取消|不要取消)\s*(?:订单)?\s*([0-9]{1,12})`)
var targetOrderPattern = regexp.MustCompile(`(?:订单|取消|是|查|查询)\s*([0-9]{1,12})`)

func (s *SessionState) observeUser(text string) {
	for _, match := range protectedOrderPattern.FindAllStringSubmatch(text, -1) {
		id, _ := strconv.ParseInt(match[1], 10, 64)
		if id <= 0 {
			continue
		}
		found := false
		for _, existing := range s.ProtectedOrderIDs {
			if existing == id {
				found = true
			}
		}
		if !found {
			s.ProtectedOrderIDs = append(s.ProtectedOrderIDs, id)
		}
	}
	sort.Slice(s.ProtectedOrderIDs, func(i, j int) bool { return s.ProtectedOrderIDs[i] < s.ProtectedOrderIDs[j] })
	// Negative clauses cannot overwrite the intended target.
	positive := protectedOrderPattern.ReplaceAllString(text, "")
	for _, match := range targetOrderPattern.FindAllStringSubmatch(positive, -1) {
		id, _ := strconv.ParseInt(match[1], 10, 64)
		if id > 0 {
			s.TargetOrderID = id
			if !containsID(s.ActiveOrderIDs, id) {
				s.ActiveOrderIDs = append(s.ActiveOrderIDs, id)
				sort.Slice(s.ActiveOrderIDs, func(i, j int) bool { return s.ActiveOrderIDs[i] < s.ActiveOrderIDs[j] })
			}
		}
	}
	if strings.Contains(text, "地址不能改") || strings.Contains(text, "地址不能修改") || strings.Contains(text, "不可修改地址") || strings.Contains(text, "不要改地址") || strings.Contains(text, "不要修改地址") {
		s.AddressLocked = true
	}
	if strings.Contains(text, "先别取消") || strings.Contains(text, "先不要取消") || strings.Contains(text, "暂不取消") || strings.Contains(text, "先不取消") {
		s.CancelPaused = true
	}
	if !strings.Contains(text, "不要") && !strings.Contains(text, "别取消") && (strings.Contains(text, "申请取消") || strings.Contains(text, "继续取消") || strings.HasPrefix(text, "取消订单")) {
		s.CancelPaused = false
	}
}

func containsID(values []int64, wanted int64) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *SessionState) observeTool(name string, raw json.RawMessage) {
	var generic map[string]any
	if json.Unmarshal(raw, &generic) == nil {
		s.observeIDs(name, generic)
	}
	var result struct {
		OK                   bool   `json:"ok"`
		OrderID              int64  `json:"orderId"`
		Status               string `json:"statusText"`
		StatusCode           int64  `json:"status"`
		RequiresConfirmation bool   `json:"requiresConfirmation"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.OK || result.OrderID <= 0 {
		if name == "get_order_status" || name == "prepare_cancel_order" {
			s.observeBusinessFailure(name, raw)
		}
		return
	}
	if name == "get_order_status" {
		if result.Status == "" {
			result.Status = statusText(result.StatusCode)
		}
		s.LastVerifiedOrderID = result.OrderID
		s.LastVerifiedStatus = result.Status
		s.syncOrderStatus(result.OrderID, result.Status, result.Status == "待处理" || result.Status == "PENDING" || result.Status == "0")
	}
	if name == "prepare_cancel_order" && result.RequiresConfirmation {
		s.PendingOrderID = result.OrderID
		s.syncOrderStatus(result.OrderID, "待处理", true)
	}
}

func (s *SessionState) observeBusinessFailure(name string, raw json.RawMessage) {
	var body struct {
		Error   string `json:"error"`
		OrderID int64  `json:"orderId"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return
	}
	if body.Error == "order not found" || body.Error == "only pending orders can be cancelled" || body.Error == "permission denied" {
		s.ConsecutiveFailures++
		if body.OrderID > 0 {
			s.LastFailedOrderID = body.OrderID
		}
	}
}

func (s *SessionState) syncOrderStatus(orderID int64, status string, cancellable bool) {
	if orderID <= 0 {
		return
	}
	addID := func(values *[]int64) {
		if !containsID(*values, orderID) {
			*values = append(*values, orderID)
			sort.Slice(*values, func(i, j int) bool { return (*values)[i] < (*values)[j] })
		}
	}
	addID(&s.OwnedOrderIDs)
	addID(&s.ActiveOrderIDs)
	if cancellable {
		addID(&s.CancellableOrderIDs)
		return
	}
	if containsID(s.CancellableOrderIDs, orderID) {
		filtered := s.CancellableOrderIDs[:0]
		for _, id := range s.CancellableOrderIDs {
			if id != orderID {
				filtered = append(filtered, id)
			}
		}
		s.CancellableOrderIDs = filtered
	}
	if strings.Contains(strings.ToLower(status), "cancel") || strings.Contains(status, "已完成") || strings.Contains(status, "已取消") || status == "CANCELLED" || status == "COMPLETED" {
		addID(&s.TerminalOrderIDs)
	}
}

func (s *SessionState) observeIDs(name string, payload map[string]any) {
	add := func(dst *[]int64, id int64) {
		if id <= 0 {
			return
		}
		for _, existing := range *dst {
			if existing == id {
				return
			}
		}
		*dst = append(*dst, id)
		sort.Slice(*dst, func(i, j int) bool { return (*dst)[i] < (*dst)[j] })
	}
	readID := func(value any) int64 {
		switch v := value.(type) {
		case float64:
			return int64(v)
		case json.Number:
			n, _ := v.Int64()
			return n
		case string:
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		default:
			return 0
		}
	}
	if name == "get_order_status" || name == "prepare_cancel_order" {
		add(&s.ActiveOrderIDs, readID(payload["orderId"]))
		add(&s.OwnedOrderIDs, readID(payload["orderId"]))
	}
	if name == "list_my_orders" {
		s.OrdersQueried = true
		if items, ok := payload["items"].([]any); ok {
			s.TotalOrderCount = len(items)
			if total := readID(payload["total"]); total >= 0 {
				s.TotalOrderCount = int(total)
			}
			for _, item := range items {
				if object, ok := item.(map[string]any); ok {
					add(&s.ActiveOrderIDs, readID(object["id"]))
					add(&s.OwnedOrderIDs, readID(object["id"]))
					add(&s.ActiveProductIDs, readID(object["productId"]))
					add(&s.FocusedProductIDs, readID(object["productId"]))
					status := fmt.Sprint(object["statusText"])
					if status == "" || status == "<nil>" {
						status = fmt.Sprint(object["status"])
					}
					if status == "" || status == "<nil>" {
						status = fmt.Sprint(object["orderStatus"])
					}
					s.syncOrderStatus(readID(object["id"]), status, status == "待处理" || status == "PENDING" || status == "0")
				}
			}
		}
	}
	if name == "search_products" {
		if items, ok := payload["items"].([]any); ok {
			for _, item := range items {
				if object, ok := item.(map[string]any); ok {
					add(&s.ActiveProductIDs, readID(object["id"]))
					add(&s.FocusedProductIDs, readID(object["id"]))
				}
			}
		}
	}
}

func (s *SessionState) observeConfirmation(action *Confirmation, now time.Time) {
	if action == nil {
		return
	}
	expires, _ := time.Parse(time.RFC3339, action.ExpiresAt)
	remaining := int64(0)
	if !expires.IsZero() && expires.After(now) {
		remaining = int64(expires.Sub(now).Seconds())
	}
	s.PendingCancel = ConfirmTokenInfo{OrderID: action.OrderID, Token: action.Token, TokenStatus: "pending", ExpiresAt: action.ExpiresAt, RemainingSeconds: remaining}
	s.PendingConfirm = &ConfirmTokenSnapshot{Token: action.Token, OrderID: action.OrderID, ExpiresAt: expires.Unix(), Status: "pending"}
	s.PendingOrderID = action.OrderID
}

func (s *SessionState) expirePending(now time.Time) {
	if s.PendingCancel.TokenStatus != "pending" {
		return
	}
	expires, _ := time.Parse(time.RFC3339, s.PendingCancel.ExpiresAt)
	if expires.IsZero() {
		return
	}
	if !expires.After(now) {
		s.PendingCancel.TokenStatus = "expired"
		s.PendingCancel.RemainingSeconds = 0
		s.PendingCancel.Token = ""
		s.PendingConfirm = nil
		return
	}
	s.PendingCancel.RemainingSeconds = int64(expires.Sub(now).Seconds())
	if s.PendingConfirm != nil {
		s.PendingConfirm.Token = ""
		s.PendingConfirm.ExpiresAt = expires.Unix()
		s.PendingConfirm.Status = "pending"
	}
}

func extractSessionState(messages []Message) SessionState {
	state := SessionState{TotalOrderCount: -1}
	for _, message := range messages {
		switch message.Role {
		case "user":
			state.observeUser(message.Content)
		case "tool":
			state.observeTool(message.Name, json.RawMessage(message.Content))
		}
	}
	return state
}

func sessionStateMessage(state SessionState) Message {
	state.expirePending(time.Now())
	if !state.hasFacts() {
		return Message{}
	}
	// Keep the legacy JSON representation for callers that still consume the
	// old durable state prefix; newly populated state uses the compact anchor.
	if state.TargetOrderID > 0 || len(state.ProtectedOrderIDs) > 0 || state.AddressLocked || state.CancelPaused {
		legacy := state
		legacy.PendingCancel.Token = ""
		if legacy.PendingConfirm != nil {
			copy := *legacy.PendingConfirm
			copy.Token = ""
			legacy.PendingConfirm = &copy
		}
		encoded, _ := json.Marshal(legacy)
		var payload map[string]any
		if json.Unmarshal(encoded, &payload) == nil {
			payload["_anchor"] = "[当前会话状态锚点]"
			encoded, _ = json.Marshal(payload)
		}
		return Message{Role: "system", Name: "session_state", Content: "程序维护的会话事实（用户意图不是授权，状态快照不是当前数据库状态；规则与确认接口仍需校验）：" + string(encoded)}
	}
	// Keep this deterministic anchor short. The bearer capability itself is
	// intentionally absent; Go reads the real token for confirmation.
	pending := "None"
	orderID := state.PendingCancel.OrderID
	if state.PendingConfirm != nil && state.PendingConfirm.OrderID > 0 {
		orderID = state.PendingConfirm.OrderID
	}
	if orderID > 0 && (pendingConfirmValid(state, time.Now()) || state.PendingCancel.TokenStatus == "pending") {
		remaining := state.PendingCancel.RemainingSeconds
		if remaining <= 0 && state.PendingConfirm != nil {
			remaining = state.PendingConfirm.ExpiresAt - time.Now().Unix()
		}
		pending = fmt.Sprintf("order=%d,status=pending,remaining=%ds", orderID, remaining)
	}
	content := fmt.Sprintf("[当前会话状态锚点] OwnedOrders: %s | Cancellable: %s | Terminal: %s | FocusedProducts: %s | PendingConfirm: %s", formatIDs(preferIDs(state.OwnedOrderIDs, state.ActiveOrderIDs)), formatIDs(state.CancellableOrderIDs), formatIDs(state.TerminalOrderIDs), formatIDs(preferIDs(state.FocusedProductIDs, state.ActiveProductIDs)), pending)
	return Message{Role: "system", Name: "session_state", Content: content}
}

func preferIDs(primary, fallback []int64) []int64 {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func formatIDs(values []int64) string {
	if len(values) == 0 {
		return "[]"
	}
	if len(values) > 8 {
		values = values[len(values)-8:]
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func (s SessionState) hasFacts() bool {
	return len(s.ActiveOrderIDs) > 0 || len(s.ActiveProductIDs) > 0 || len(s.OwnedOrderIDs) > 0 || len(s.FocusedProductIDs) > 0 || s.PendingConfirm != nil || s.TotalOrderCount >= 0 || s.PendingCancel.OrderID > 0 || s.PendingCancel.TokenStatus != "" ||
		s.TargetOrderID > 0 || len(s.ProtectedOrderIDs) > 0 || s.AddressLocked || s.CancelPaused || s.PendingOrderID > 0 || s.LastVerifiedOrderID > 0 || len(s.ConfirmedActions) > 0
}
