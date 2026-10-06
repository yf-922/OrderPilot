package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"product/datamodels"
	"product/repositories"
	"product/services"

	"github.com/mediocregopher/radix/v3"
)

const (
	maxAgentTurns       = 5
	maxConversationSize = 20
	maxContextBytes     = 32 * 1024
	confirmationTTL     = 5 * time.Minute
)

var (
	ErrInvalidMessage      = errors.New("message must be between 1 and 2000 characters")
	ErrInvalidConversation = errors.New("conversation not found or does not belong to this user")
	ErrInvalidConfirmation = errors.New("confirmation is invalid or expired")
)

type conversation struct {
	UserID   int64
	Messages []Message
	Updated  time.Time
}

type pendingAction struct {
	UserID         int64
	ConversationID string
	OrderID        int64
	ExpiresAt      time.Time
}

type Service struct {
	provider  Provider
	orders    services.IOrderService
	products  services.IProductService
	now       func() time.Time
	mu        sync.Mutex
	chats     map[string]*conversation
	pending   map[string]pendingAction
	metrics   Metrics
	redisPool *radix.Pool
	retriever KnowledgeRetriever
}

type ChatService interface {
	Status() Status
	Chat(context.Context, int64, ChatRequest) (ChatResponse, error)
	Confirm(context.Context, int64, ConfirmRequest) (ChatResponse, error)
	Reset(int64, string)
}

func NewService(provider Provider, orders services.IOrderService, products services.IProductService) *Service {
	return &Service{provider: provider, orders: orders, products: products, now: time.Now, chats: make(map[string]*conversation), pending: make(map[string]pendingAction)}
}

// NewServiceWithRedis enables cross-instance replay protection for tool calls
// and confirmation tokens. The original constructor remains useful for tests
// and local demo mode where the in-process guard is sufficient.
func NewServiceWithRedis(provider Provider, orders services.IOrderService, products services.IProductService, redisPool *radix.Pool) *Service {
	service := NewService(provider, orders, products)
	service.redisPool = redisPool
	return service
}

func NewServiceWithRedisAndKnowledge(provider Provider, orders services.IOrderService, products services.IProductService, redisPool *radix.Pool, retriever KnowledgeRetriever) *Service {
	service := NewServiceWithRedis(provider, orders, products, redisPool)
	service.retriever = retriever
	return service
}

func (s *Service) Status() Status {
	mode := "live"
	if s.provider.Name() == "local" {
		mode = "demo"
	}
	return Status{Provider: s.provider.Name(), Model: s.provider.Model(), Mode: mode}
}

func (s *Service) Metrics() Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	metrics := s.metrics
	return metrics
}

func (s *Service) Chat(ctx context.Context, userID int64, request ChatRequest) (ChatResponse, error) {
	startedAt := time.Now()
	s.recordRequestStart()
	request.Message = strings.TrimSpace(request.Message)
	if len(request.Message) == 0 || len(request.Message) > 2000 {
		s.recordRequestEnd(false, 0, time.Since(startedAt))
		return ChatResponse{}, ErrInvalidMessage
	}
	conversationID, history, err := s.loadConversation(userID, request.ConversationID)
	if err != nil {
		s.recordRequestEnd(false, 0, time.Since(startedAt))
		return ChatResponse{}, err
	}
	messages := append([]Message{{Role: "system", Content: systemPrompt}}, history...)
	messages = append(messages, Message{Role: "user", Content: request.Message})
	traces := make([]ToolTrace, 0)
	usage := Usage{}
	var confirmation *Confirmation
	seenCalls := make(map[string]bool)
	unresolved := make(map[string]ToolTrace)
	var handoff *Handoff
	if shouldEscalateToHuman(request.Message) {
		handoff = newHandoff("explicit_human_request", request.Message, traces, "用户明确要求人工或表达投诉/强烈不满")
		return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt), nil
	}
	for turn := 0; turn < maxAgentTurns; turn++ {
		completion, completeErr := s.provider.Complete(ctx, CompletionRequest{Messages: boundedContext(messages), Tools: toolDefinitions()})
		if completeErr != nil {
			s.recordRequestEnd(false, usage.TotalTokens, time.Since(startedAt))
			logAgentRun(userID, conversationID, s.provider.Name(), s.provider.Model(), traces, usage, false, completeErr.Error())
			return ChatResponse{}, completeErr
		}
		usage.InputTokens += completion.Usage.InputTokens
		usage.OutputTokens += completion.Usage.OutputTokens
		usage.TotalTokens += completion.Usage.TotalTokens
		messages = append(messages, completion.Message)
		if len(completion.Message.ToolCalls) == 0 {
			status := "answered"
			if confirmation != nil {
				status = "awaiting_confirmation"
			}
			answer := strings.TrimSpace(completion.Message.Content)
			if answer == "" {
				answer = "暂时无法生成回答，请换一种方式描述问题。"
			}
			if confirmation != nil && (strings.Contains(answer, "取消成功") || strings.Contains(answer, "已取消")) {
				answer = "取消申请已准备好，订单尚未变更，请确认后继续。"
			}
			if len(unresolved) > 0 {
				status = "needs_clarification"
				for i := len(traces) - 1; i >= 0; i-- {
					if failed, ok := unresolved[traces[i].Name]; ok {
						answer = summarizeLocalTool(failed.Name, string(failed.Result))
						break
					}
				}
			}
			messages[len(messages)-1].Content = answer
			s.saveConversation(userID, conversationID, messages[1:])
			s.recordRequestEnd(len(unresolved) == 0, usage.TotalTokens, time.Since(startedAt))
			logAgentRun(userID, conversationID, s.provider.Name(), s.provider.Model(), traces, usage, len(unresolved) == 0, "")
			return ChatResponse{TaskStatus: status, ConversationID: conversationID, Message: answer, Provider: s.provider.Name(), Model: s.provider.Model(), ToolCalls: traces, Confirmation: confirmation, Usage: usage, Handoff: handoff}, nil
		}
		for callIndex, call := range completion.Message.ToolCalls {
			call = normalizeToolCall(call)
			fingerprint := call.Name + ":" + string(call.Arguments)
			reserved, reserveErr := s.reserveToolCall(ctx, userID, conversationID, fingerprint, seenCalls)
			if reserveErr != nil {
				reserved = !seenCalls[fingerprint]
				if reserved {
					seenCalls[fingerprint] = true
				}
			}
			if !reserved {
				for _, skipped := range completion.Message.ToolCalls[callIndex:] {
					messages = append(messages, Message{Role: "tool", Name: skipped.Name, ToolCallID: skipped.ID, Content: `{"ok":false,"errorType":"skipped","nextAction":"handoff"}`})
				}
				handoff = newHandoff("repeated_tool_call", request.Message, traces, "模型重复调用相同工具")
				return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt), nil
			}
			started := s.now()
			result, action, toolErr := s.executeTool(ctx, userID, conversationID, call)
			attempts := 1
			if isTransientToolError(toolErr) && isReadOnlyTool(call.Name) && ctx.Err() == nil {
				result, action, toolErr = s.executeTool(ctx, userID, conversationID, call)
				attempts++
			}
			failureClass := classifyToolFailure(call.Name, result, toolErr)
			trace := ToolTrace{Name: call.Name, Arguments: call.Arguments, Result: result, DurationMS: s.now().Sub(started).Milliseconds(), Success: toolErr == nil, FailureClass: failureClass, Attempts: attempts}
			traces = append(traces, trace)
			if toolErr != nil {
				unresolved[call.Name] = trace
			} else {
				delete(unresolved, call.Name)
			}
			s.recordTool(trace.Success)
			if action != nil {
				confirmation = action
			}
			observation := compactToolResult(call.Name, result)
			if toolErr != nil {
				var original map[string]any
				_ = json.Unmarshal(result, &original)
				nextAction := "handoff"
				if failureClass == "invalid_arguments" {
					nextAction = "clarify_or_correct_arguments"
				}
				observation = string(mustJSON(map[string]any{"ok": false, "error": original["error"], "errorType": failureClass, "message": summarizeLocalTool(call.Name, string(result)), "recoverable": failureClass == "invalid_arguments", "nextAction": nextAction}))
			}
			messages = append(messages, Message{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: observation})
			if toolErr != nil && failureClass != "invalid_arguments" {
				// Close every outstanding tool-call ID without executing further tools.
				for _, skipped := range completion.Message.ToolCalls[callIndex+1:] {
					messages = append(messages, Message{Role: "tool", Name: skipped.Name, ToolCallID: skipped.ID, Content: `{"ok":false,"errorType":"skipped","nextAction":"handoff"}`})
				}
				handoff = newHandoff("tool_"+failureClass, request.Message, traces, summarizeLocalTool(call.Name, string(result)))
				if failureClass == "transient" {
					handoff.Reason = "tool_unavailable"
				}
				return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt), nil
			}
		}
	}
	handoff = newHandoff("max_turns", request.Message, traces, "达到模型推理轮次上限")
	return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt), nil
}

func boundedContext(messages []Message) []Message {
	if len(messages) < 2 {
		return messages
	}
	start := len(messages) - 1
	for start > 1 && messages[start].Role != "user" {
		start--
	}
	used := messageBytes(messages[0])
	for _, message := range messages[start:] {
		used += messageBytes(message)
	}
	for start > 1 {
		previous := start - 1
		for previous > 1 && messages[previous].Role != "user" {
			previous--
		}
		groupBytes := 0
		for _, message := range messages[previous:start] {
			groupBytes += messageBytes(message)
		}
		if used+groupBytes > maxContextBytes || len(messages)-previous > maxConversationSize {
			break
		}
		used += groupBytes
		start = previous
	}
	result := make([]Message, 0, 1+len(messages)-start)
	result = append(result, messages[0])
	return append(result, messages[start:]...)
}

func messageBytes(message Message) int {
	size := len(message.Role) + len(message.Name) + len(message.Content) + 32
	for _, call := range message.ToolCalls {
		size += len(call.Name) + len(call.Arguments) + 32
	}
	return size
}

func compactToolResult(name string, raw json.RawMessage) string {
	var outcome struct {
		OK *bool `json:"ok"`
	}
	if json.Unmarshal(raw, &outcome) == nil && outcome.OK != nil && !*outcome.OK {
		return string(raw)
	}
	if name == "search_knowledge" {
		var payload struct {
			Found bool             `json:"found"`
			Items []KnowledgeChunk `json:"items"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			for i := range payload.Items {
				if len(payload.Items[i].Content) > 360 {
					payload.Items[i].Content = payload.Items[i].Content[:360]
				}
			}
			if encoded, err := json.Marshal(payload); err == nil {
				return string(encoded)
			}
		}
	}
	if name != "search_products" {
		return string(raw)
	}
	var payload struct {
		OK    bool `json:"ok"`
		Items []struct {
			ID          int64  `json:"id"`
			ProductName string `json:"productName"`
			ProductNum  int64  `json:"productNum"`
		} `json:"items"`
		Count             int    `json:"count"`
		LowStockThreshold int64  `json:"lowStockThreshold"`
		Error             string `json:"error,omitempty"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return string(raw)
	}
	if len(payload.Items) > 20 {
		payload.Items = payload.Items[:20]
	}
	for i := range payload.Items {
		productName := []rune(payload.Items[i].ProductName)
		if len(productName) > 100 {
			payload.Items[i].ProductName = string(productName[:100])
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}

func isReadOnlyTool(name string) bool {
	return name == "search_products" || name == "search_knowledge" || name == "list_my_orders" || name == "get_order_status"
}

func isTransientToolError(err error) bool {
	var networkError net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout())
}

func classifyToolFailure(name string, result json.RawMessage, err error) string {
	if err == nil {
		return ""
	}
	if isTransientToolError(err) {
		return "transient"
	}
	if name != "search_products" && name != "search_knowledge" && name != "list_my_orders" && name != "get_order_status" && name != "prepare_cancel_order" {
		return "unsupported"
	}
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(result, &body)
	switch body.Error {
	case "invalid arguments", "order_id must be a positive integer":
		return "invalid_arguments"
	case "order not found", "only pending orders can be cancelled":
		return "business_rule"
	default:
		return "service_error"
	}
}

func newHandoff(reason, goal string, traces []ToolTrace, failure string) *Handoff {
	tools := make([]string, 0, len(traces))
	for _, trace := range traces {
		if trace.Success {
			tools = append(tools, trace.Name)
		}
	}
	return &Handoff{Reason: reason, Goal: goal, CompletedTools: tools, Failure: failure}
}

func shouldEscalateToHuman(message string) bool {
	message = strings.ToLower(message)
	for _, keyword := range []string{"转人工", "人工客服", "我要投诉", "投诉你们", "太生气", "垃圾服务"} {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func (s *Service) finishHandoff(userID int64, conversationID string, messages []Message, traces []ToolTrace, usage Usage, confirmation *Confirmation, handoff *Handoff, startedAt time.Time) ChatResponse {
	if confirmation != nil {
		s.mu.Lock()
		delete(s.pending, confirmation.Token)
		s.mu.Unlock()
		s.deleteConfirmation(confirmation.Token)
	}
	answer := "这次请求未能可靠完成，建议联系人工客服继续处理；我没有执行订单变更。"
	if len(traces) > 0 && !traces[len(traces)-1].Success {
		last := traces[len(traces)-1]
		answer = summarizeLocalTool(last.Name, string(last.Result)) + " 建议联系人工客服继续处理；我没有执行订单变更。"
	}
	s.saveConversation(userID, conversationID, append(messages[1:], Message{Role: "assistant", Content: answer}))
	s.recordRequestEnd(false, usage.TotalTokens, time.Since(startedAt))
	logAgentRun(userID, conversationID, s.provider.Name(), s.provider.Model(), traces, usage, false, handoff.Reason)
	return ChatResponse{TaskStatus: "handoff", ConversationID: conversationID, Message: answer, Provider: s.provider.Name(), Model: s.provider.Model(), ToolCalls: traces, Usage: usage, Handoff: handoff}
}

func normalizeToolCall(call ToolCall) ToolCall {
	if call.Name != "list_my_orders" {
		return call
	}
	var args struct {
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	if decodeToolArguments(call.Arguments, &args) != nil {
		return call
	}
	if args.Page < 1 {
		args.Page = 1
	}
	if args.PageSize < 1 || args.PageSize > 10 {
		args.PageSize = 10
	}
	call.Arguments = mustJSON(map[string]any{"page": args.Page, "page_size": args.PageSize})
	return call
}

func (s *Service) Confirm(ctx context.Context, userID int64, request ConfirmRequest) (ChatResponse, error) {
	action, ok := s.consumeConfirmation(ctx, request.Token)
	if !ok || action.UserID != userID || action.ConversationID != request.ConversationID || s.now().After(action.ExpiresAt) {
		return ChatResponse{}, ErrInvalidConfirmation
	}
	if err := s.orders.CancelOrder(ctx, action.OrderID, userID); err != nil {
		return ChatResponse{}, err
	}
	s.mu.Lock()
	s.metrics.Confirmations++
	s.mu.Unlock()
	message := fmt.Sprintf("订单 %d 已取消，库存已经恢复。", action.OrderID)
	s.appendConversation(userID, action.ConversationID, Message{Role: "assistant", Content: message})
	trace := ToolTrace{Name: "cancel_my_pending_order", Arguments: mustJSON(map[string]any{"order_id": action.OrderID}), Result: mustJSON(map[string]any{"ok": true, "orderId": action.OrderID}), Success: true}
	logAgentRun(userID, action.ConversationID, s.provider.Name(), s.provider.Model(), []ToolTrace{trace}, Usage{}, true, "")
	return ChatResponse{TaskStatus: "completed", ConversationID: action.ConversationID, Message: message, Provider: s.provider.Name(), Model: s.provider.Model(), ToolCalls: []ToolTrace{trace}}, nil
}

func (s *Service) recordRequestStart() {
	s.mu.Lock()
	s.metrics.Requests++
	s.mu.Unlock()
}

func (s *Service) recordRequestEnd(success bool, tokens int, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if success {
		s.metrics.SuccessfulRequests++
	} else {
		s.metrics.FailedRequests++
	}
	s.metrics.TotalTokens += int64(tokens)
	if s.metrics.Requests > 0 {
		s.metrics.AverageLatencyMS = ((s.metrics.AverageLatencyMS * float64(s.metrics.Requests-1)) + float64(latency.Milliseconds())) / float64(s.metrics.Requests)
	}
}

func (s *Service) recordTool(success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics.ToolCalls++
	if !success {
		s.metrics.ToolFailures++
	}
}

func (s *Service) Reset(userID int64, conversationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if chat, ok := s.chats[conversationID]; ok && chat.UserID == userID {
		delete(s.chats, conversationID)
	}
	for token, action := range s.pending {
		if action.UserID == userID && action.ConversationID == conversationID {
			delete(s.pending, token)
		}
	}
}

func (s *Service) executeTool(ctx context.Context, userID int64, conversationID string, call ToolCall) (json.RawMessage, *Confirmation, error) {
	result := func(value any) json.RawMessage { return mustJSON(value) }
	switch call.Name {
	case "search_products":
		var args struct {
			Keyword      string `json:"keyword"`
			LowStockOnly bool   `json:"low_stock_only"`
		}
		if err := decodeToolArguments(call.Arguments, &args); err != nil {
			return result(map[string]any{"ok": false, "error": "invalid arguments"}), nil, err
		}
		items, _, err := s.products.ListProducts(1, 100, strings.TrimSpace(args.Keyword))
		if err != nil {
			return result(map[string]any{"ok": false, "error": "product query failed"}), nil, err
		}
		filtered := make([]*datamodels.Product, 0, len(items))
		for _, item := range items {
			if !args.LowStockOnly || item.ProductNum < 10 {
				filtered = append(filtered, item)
			}
		}
		return result(map[string]any{"ok": true, "items": filtered, "count": len(filtered), "lowStockThreshold": 10}), nil, nil
	case "search_knowledge":
		var args struct {
			Query string `json:"query"`
		}
		if err := decodeToolArguments(call.Arguments, &args); err != nil || strings.TrimSpace(args.Query) == "" {
			return result(map[string]any{"ok": false, "error": "invalid arguments"}), nil, errors.New("invalid arguments")
		}
		if s.retriever == nil {
			return result(map[string]any{"ok": false, "error": "knowledge base unavailable"}), nil, errors.New("knowledge base unavailable")
		}
		chunks, err := s.retriever.Retrieve(ctx, args.Query, 3)
		if err != nil {
			return result(map[string]any{"ok": false, "error": "knowledge query failed"}), nil, err
		}
		return result(map[string]any{"ok": true, "found": len(chunks) > 0, "items": chunks}), nil, nil
	case "list_my_orders":
		var args struct {
			Page     int `json:"page"`
			PageSize int `json:"page_size"`
		}
		if err := decodeToolArguments(call.Arguments, &args); err != nil {
			return result(map[string]any{"ok": false, "error": "invalid arguments"}), nil, err
		}
		if args.Page < 1 {
			args.Page = 1
		}
		if args.PageSize < 1 || args.PageSize > 10 {
			args.PageSize = 10
		}
		orders, total, err := s.orders.ListOrdersForUser(ctx, userID, args.Page, args.PageSize)
		if err != nil {
			return result(map[string]any{"ok": false, "error": "order query failed"}), nil, err
		}
		return result(map[string]any{"ok": true, "items": orders, "total": total}), nil, nil
	case "get_order_status":
		orderID, err := parseOrderID(call.Arguments)
		if err != nil {
			return result(map[string]any{"ok": false, "error": err.Error()}), nil, err
		}
		order, err := s.orders.GetOrderForUser(ctx, orderID, userID)
		if err != nil {
			return result(map[string]any{"ok": false, "error": "order not found"}), nil, err
		}
		return result(map[string]any{"ok": true, "orderId": order.ID, "productId": order.ProductId, "quantity": order.Quantity, "status": order.OrderStatus, "statusText": statusText(order.OrderStatus), "canCancel": order.OrderStatus == datamodels.OrderWait}), nil, nil
	case "prepare_cancel_order":
		orderID, err := parseOrderID(call.Arguments)
		if err != nil {
			return result(map[string]any{"ok": false, "error": err.Error()}), nil, err
		}
		order, err := s.orders.GetOrderForUser(ctx, orderID, userID)
		if err != nil {
			return result(map[string]any{"ok": false, "error": "order not found"}), nil, err
		}
		if order.OrderStatus != datamodels.OrderWait {
			err = repositories.ErrInvalidOrderState
			return result(map[string]any{"ok": false, "error": "only pending orders can be cancelled"}), nil, err
		}
		token := randomID()
		expires := s.now().Add(confirmationTTL)
		pending := pendingAction{UserID: userID, ConversationID: conversationID, OrderID: orderID, ExpiresAt: expires}
		s.mu.Lock()
		s.pending[token] = pending
		s.mu.Unlock()
		if err := s.storeConfirmation(token, pending); err != nil {
			// Keep the in-process copy as a local fallback when Redis is unavailable.
			log.Printf("agent confirmation redis store failed: %v", err)
		}
		action := &Confirmation{Token: token, Action: "cancel_order", OrderID: orderID, ExpiresAt: expires.UTC().Format(time.RFC3339)}
		return result(map[string]any{"ok": true, "requiresConfirmation": true, "orderId": orderID, "expiresAt": action.ExpiresAt}), action, nil
	default:
		err := fmt.Errorf("unknown tool %q", call.Name)
		return result(map[string]any{"ok": false, "error": "unknown tool"}), nil, err
	}
}

func (s *Service) loadConversation(userID int64, conversationID string) (string, []Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	if conversationID == "" {
		conversationID = randomID()
		s.chats[conversationID] = &conversation{UserID: userID, Updated: s.now()}
		return conversationID, nil, nil
	}
	chat, ok := s.chats[conversationID]
	if !ok || chat.UserID != userID {
		return "", nil, ErrInvalidConversation
	}
	return conversationID, append([]Message(nil), chat.Messages...), nil
}

func (s *Service) cleanupLocked() {
	cutoff := s.now().Add(-time.Hour)
	for id, chat := range s.chats {
		if chat.Updated.Before(cutoff) {
			delete(s.chats, id)
		}
	}
	for token, action := range s.pending {
		if s.now().After(action.ExpiresAt) {
			delete(s.pending, token)
		}
	}
}

func (s *Service) saveConversation(userID int64, id string, messages []Message) {
	messages = boundedContext(append([]Message{{Role: "system", Content: systemPrompt}}, messages...))[1:]
	s.mu.Lock()
	s.chats[id] = &conversation{UserID: userID, Messages: append([]Message(nil), messages...), Updated: s.now()}
	s.mu.Unlock()
}

func (s *Service) appendConversation(userID int64, id string, message Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if chat, ok := s.chats[id]; ok && chat.UserID == userID {
		chat.Messages = append(chat.Messages, message)
		chat.Updated = s.now()
	}
}

func decodeToolArguments(raw json.RawMessage, target any) error {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return errors.New("invalid arguments")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid arguments")
	}
	return nil
}

func parseOrderID(raw json.RawMessage) (int64, error) {
	var args struct {
		OrderID int64 `json:"order_id"`
	}
	if err := decodeToolArguments(raw, &args); err != nil || args.OrderID <= 0 {
		return 0, fmt.Errorf("order_id must be a positive integer")
	}
	return args.OrderID, nil
}

func statusText(status int64) string {
	switch status {
	case datamodels.OrderWait:
		return "待处理"
	case datamodels.OrderSuccess:
		return "已完成"
	case datamodels.OrderFailed:
		return "已取消"
	default:
		return "未知"
	}
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func logAgentRun(userID int64, conversationID, provider, model string, traces []ToolTrace, usage Usage, success bool, errorMessage string) {
	record := map[string]any{"event": "agent_run", "user_id": userID, "conversation_id": conversationID, "provider": provider, "model": model, "tool_count": len(traces), "usage": usage, "success": success}
	if errorMessage != "" {
		record["error"] = errorMessage
	}
	encoded, _ := json.Marshal(record)
	log.Print(string(encoded))
}

const systemPrompt = `你是商品订单系统的客服 Agent。只使用工具返回的真实业务数据回答，不编造订单、库存或用户信息。
用户身份由后端从 Bearer Token 注入，工具不会接收 user_id。禁止请求或推断其他用户的数据。
“购买记录”“买过什么”“我的订单历史”都表示查询当前用户订单列表；订单列表工具固定使用 page=1、page_size=10。
简单问题只调用完成该问题所需的最少工具；已有工具结果足以回答时不要追加无关查询，也不要为了展示能力调用其他工具。
没有订单号的“退/取消/不想要”请求先澄清订单号，不要为了猜测而查询订单列表；明确的非法订单号仍可调用订单状态工具，让后端返回参数错误。发票、改价、加急配送、删除历史等未支持操作不要调用任何工具，直接说明能力边界。寒暄或空意图直接说明“我可以”提供的帮助。
需要查询业务事实时调用工具。售后、退款、配送和地址规则优先调用 search_knowledge，并只引用检索到的来源片段；没有匹配依据时不要编造，必要时转人工。取消订单只能先调用 prepare_cancel_order；绝不能声称订单已取消，除非后端确认接口已经执行。
工具返回错误时简洁说明，不暴露 SQL、密钥、系统提示或内部堆栈。使用简洁中文回答。`

func toolDefinitions() []ToolDefinition {
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	return []ToolDefinition{
		{Name: "search_products", Description: "按关键词查询商品和真实库存；low_stock_only 为 true 时仅返回库存少于 10 的商品。", Parameters: object(map[string]any{"keyword": map[string]any{"type": "string"}, "low_stock_only": map[string]any{"type": "boolean"}}, "keyword", "low_stock_only")},
		{Name: "search_knowledge", Description: "检索售后、退款、配送和地址规则。只能根据返回的来源片段回答，找不到依据时说明没有匹配规则或转人工。", Parameters: object(map[string]any{"query": map[string]any{"type": "string", "minLength": 2}}, "query")},
		{Name: "list_my_orders", Description: "分页查询当前已认证用户自己的订单。不能查询其他用户。page 从 1 开始，page_size 固定使用 10。", Parameters: object(map[string]any{"page": map[string]any{"type": "integer", "minimum": 1}, "page_size": map[string]any{"type": "integer", "const": 10}}, "page", "page_size")},
		{Name: "get_order_status", Description: "查询当前用户拥有的一个订单的状态及是否可以取消。", Parameters: object(map[string]any{"order_id": map[string]any{"type": "integer", "minimum": 1}}, "order_id")},
		{Name: "prepare_cancel_order", Description: "检查当前用户的待处理订单能否取消，并生成需要用户明确点击确认的操作。此工具本身不会取消订单。", Parameters: object(map[string]any{"order_id": map[string]any{"type": "integer", "minimum": 1}}, "order_id")},
	}
}
