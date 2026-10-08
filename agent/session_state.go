package agent

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// State survives history pruning, but is scoped to the authenticated conversation.
// User intent is never treated as an authorization or proof of completed work.
type SessionState struct {
	TargetOrderID       int64             `json:"targetOrderId,omitempty"`
	ProtectedOrderIDs   []int64           `json:"protectedOrderIds,omitempty"`
	AddressLocked       bool              `json:"addressLocked,omitempty"`
	CancelPaused        bool              `json:"cancelPaused,omitempty"`
	PendingOrderID      int64             `json:"pendingOrderId,omitempty"`
	LastVerifiedOrderID int64             `json:"lastVerifiedOrderId,omitempty"`
	LastVerifiedStatus  string            `json:"lastVerifiedStatus,omitempty"`
	ConfirmedActions    []ConfirmedAction `json:"confirmedActions,omitempty"`
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
		}
	}
	if strings.Contains(text, "地址不能改") || strings.Contains(text, "不可修改地址") || strings.Contains(text, "不要改地址") || strings.Contains(text, "不要修改地址") {
		s.AddressLocked = true
	}
	if strings.Contains(text, "先别取消") || strings.Contains(text, "先不要取消") || strings.Contains(text, "暂不取消") || strings.Contains(text, "先不取消") {
		s.CancelPaused = true
	}
	if !strings.Contains(text, "不要") && !strings.Contains(text, "别取消") && (strings.Contains(text, "申请取消") || strings.Contains(text, "继续取消") || strings.HasPrefix(text, "取消订单")) {
		s.CancelPaused = false
	}
}

func (s *SessionState) observeTool(name string, raw json.RawMessage) {
	var result struct {
		OK                   bool   `json:"ok"`
		OrderID              int64  `json:"orderId"`
		Status               string `json:"statusText"`
		RequiresConfirmation bool   `json:"requiresConfirmation"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.OK || result.OrderID <= 0 {
		return
	}
	if name == "get_order_status" {
		s.LastVerifiedOrderID = result.OrderID
		s.LastVerifiedStatus = result.Status
	}
	if name == "prepare_cancel_order" && result.RequiresConfirmation {
		s.PendingOrderID = result.OrderID
	}
}

func extractSessionState(messages []Message) SessionState {
	state := SessionState{}
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
	encoded, _ := json.Marshal(state)
	if string(encoded) == "{}" {
		return Message{}
	}
	return Message{Role: "system", Name: "session_state", Content: "程序维护的会话事实（用户意图不是授权，状态快照不是当前数据库状态；规则与确认接口仍需校验）：" + string(encoded)}
}
