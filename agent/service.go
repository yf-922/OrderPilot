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
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"product/datamodels"
	"product/repositories"
	"product/services"

	"github.com/mediocregopher/radix/v3"
)

const (
	maxAgentTurns             = 5
	maxConversationSize       = 32
	maxContextBytes           = 96 * 1024
	maxContextRounds          = 6
	legacyMaxMessages         = 20
	defaultContextTokenBudget = 12000
	confirmationTTL           = 5 * time.Minute
)

// ContextStrategy selects one arm of the context ablation. It is deliberately
// a small string enum so it can be wired to an environment/config file.
type ContextStrategy string

const (
	StrategyFullHistory     ContextStrategy = "full_history"
	StrategyNaiveTruncation ContextStrategy = "naive_truncation"
	StrategyDehydrationOnly ContextStrategy = "dehydration_only"
	StrategyOursFull        ContextStrategy = "ours_full"
)

var (
	ErrInvalidMessage      = errors.New("message must be between 1 and 2000 characters")
	ErrInvalidConversation = errors.New("conversation not found or does not belong to this user")
	ErrInvalidConfirmation = errors.New("confirmation is invalid or expired")
)

var summaryNumberPattern = regexp.MustCompile(`[0-9]{1,12}`)

type conversation struct {
	UserID   int64
	Messages []Message
	State    SessionState
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
	// Internal evaluation switch; production defaults to bounded context.
	disableContextCompression bool
	contextStrategy           ContextStrategy
	jev                       *JevDecisionEngine
	jevLatencies              []float64
	lastJevLatency            time.Duration
	lastJevRoute              JevRouteAction
	lastJevDecision           JevDecision
	lastContextLatency        time.Duration
	lastLLMNetworkLatency     time.Duration
	lastRetryCount            int
}

// LastJevDiagnostics returns the most recent preflight timing and route.
// Evaluation runners call Chat serially, while the mutex keeps this accessor
// safe for production observability reads.
func (s *Service) LastJevDiagnostics() (time.Duration, JevRouteAction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastJevLatency, s.lastJevRoute
}

// 与现有 Last* 诊断一致，用于串行评测；不用于并发请求归因。
func (s *Service) LastJevDecision() JevDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	decision := s.lastJevDecision
	decision.AllowedTools = append([]string(nil), decision.AllowedTools...)
	return decision
}

// LastRequestDiagnostics returns phase timings accumulated during the most
// recent Chat call. A Chat call may perform more than one provider round when
// tools are used, so the phase values are sums across those rounds.
func (s *Service) LastRequestDiagnostics() (contextLatency, llmNetworkLatency time.Duration, retryCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastContextLatency, s.lastLLMNetworkLatency, s.lastRetryCount
}

func (s *Service) resetRequestDiagnostics() {
	s.mu.Lock()
	s.lastContextLatency = 0
	s.lastLLMNetworkLatency = 0
	s.lastRetryCount = 0
	s.lastJevLatency = 0
	s.lastJevRoute = RouteProceedMainLLM
	s.lastJevDecision = JevDecision{}
	s.mu.Unlock()
}

func (s *Service) addRequestDiagnostics(contextLatency, llmNetworkLatency time.Duration, retries int) {
	s.mu.Lock()
	s.lastContextLatency += contextLatency
	s.lastLLMNetworkLatency += llmNetworkLatency
	s.lastRetryCount += retries
	s.mu.Unlock()
}

type ChatService interface {
	Status() Status
	Chat(context.Context, int64, ChatRequest) (ChatResponse, error)
	Confirm(context.Context, int64, ConfirmRequest) (ChatResponse, error)
	Reset(int64, string)
}

func NewService(provider Provider, orders services.IOrderService, products services.IProductService) *Service {
	strategy := ContextStrategy(strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_CONTEXT_STRATEGY"))))
	if strategy == "" {
		strategy = StrategyOursFull
	}
	return &Service{provider: provider, orders: orders, products: products, now: time.Now, chats: make(map[string]*conversation), pending: make(map[string]pendingAction), contextStrategy: strategy, jev: NewJevDecisionEngine()}
}

// SetContextStrategy is used by controlled evaluations and can also be called
// by the application config layer during startup.
func (s *Service) SetContextStrategy(strategy ContextStrategy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contextStrategy = normalizeContextStrategy(strategy)
}

func normalizeContextStrategy(strategy ContextStrategy) ContextStrategy {
	switch strategy {
	case StrategyFullHistory, StrategyNaiveTruncation, StrategyDehydrationOnly, StrategyOursFull:
		return strategy
	default:
		return StrategyOursFull
	}
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
	s.resetRequestDiagnostics()
	request.Message = strings.TrimSpace(request.Message)
	if len(request.Message) == 0 || len(request.Message) > 2000 {
		s.recordRequestEnd(false, 0, time.Since(startedAt))
		return ChatResponse{}, ErrInvalidMessage
	}
	conversationID, history, state, err := s.loadConversation(userID, request.ConversationID)
	if err != nil {
		s.recordRequestEnd(false, 0, time.Since(startedAt))
		return ChatResponse{}, err
	}
	state.expirePending(s.now())
	state.UserID = userID
	messages := append([]Message{{Role: "system", Content: systemPrompt}}, history...)
	messages = append(messages, Message{Role: "user", Content: request.Message})
	state.observeUser(request.Message)
	traces := make([]ToolTrace, 0)
	usage := Usage{}
	var confirmation *Confirmation
	seenCalls := make(map[string]bool)
	unresolved := make(map[string]ToolTrace)
	var handoff *Handoff
	decision := JevDecision{RouteAction: RouteProceedMainLLM, AllowedTools: allToolNames()}
	if s.jev != nil && s.jev.enabled {
		jevStarted := time.Now()
		decision = s.jev.Evaluate(ctx, JevInput{UserQuery: request.Message, RecentTurnsBrief: recentTurnsBrief(history), State: state})
		jevLatency := time.Since(jevStarted)
		s.mu.Lock()
		s.lastJevLatency = jevLatency
		s.lastJevRoute = decision.RouteAction
		s.lastJevDecision = decision
		s.mu.Unlock()
		s.recordJev(decision, jevLatency)
		if s.jev.shadow {
			decision.RouteAction = RouteProceedMainLLM
			decision.AllowedTools = allToolNames()
		}
	}
	if decision.Source == "typesafe" && !s.jev.shadow && decision.TargetOrderID > 0 && containsID(state.OwnedOrderIDs, decision.TargetOrderID) {
		state.TargetOrderID = decision.TargetOrderID
		// 指代候选不是操作授权，后续仍检查所有权、状态和二次确认。
		hint := Message{Role: "system", Content: fmt.Sprintf("前置指代候选：当前用户可能指订单 %d；这不是取消授权，必须继续校验状态与二次确认。", decision.TargetOrderID)}
		messages = append(messages[:1:1], append([]Message{hint}, messages[1:]...)...)
	}
	if shouldEscalateToHuman(request.Message) {
		handoff = newHandoff("explicit_human_request", request.Message, traces, "用户明确要求人工或表达投诉/强烈不满")
		return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt, state), nil
	}
	switch decision.RouteAction {
	case RouteShortCircuitReject:
		answer := decision.RejectOrEscalateReason
		if answer == "" {
			answer = "当前请求不满足可操作条件，请核对订单状态后重试。"
		}
		messages = append(messages, Message{Role: "assistant", Content: answer})
		s.saveConversation(userID, conversationID, messages[1:], state)
		s.recordRequestEnd(true, 0, time.Since(startedAt))
		return ChatResponse{TaskStatus: "rejected", ConversationID: conversationID, Message: answer, Provider: s.provider.Name(), Model: s.provider.Model()}, nil
	case RouteFastPathConfirm:
		if decision.ConfirmDecision == "CONFIRM" && pendingToken(state) != "" {
			return s.Confirm(ctx, userID, ConfirmRequest{ConversationID: conversationID, Token: pendingToken(state)})
		}
		if decision.ConfirmDecision == "ABORT" {
			if pendingToken(state) != "" {
				s.deleteConfirmation(pendingToken(state))
			}
			state.PendingCancel = ConfirmTokenInfo{OrderID: state.PendingCancel.OrderID, TokenStatus: "aborted"}
			state.PendingConfirm = nil
			state.PendingOrderID = 0
			answer := "好的，已放弃取消操作。"
			messages = append(messages, Message{Role: "assistant", Content: answer})
			s.saveConversation(userID, conversationID, messages[1:], state)
			s.recordRequestEnd(true, 0, time.Since(startedAt))
			return ChatResponse{TaskStatus: "answered", ConversationID: conversationID, Message: answer, Provider: s.provider.Name(), Model: s.provider.Model()}, nil
		}
	case RouteEscalateHuman:
		handoff := newHandoff("jev_escalation", request.Message, traces, decision.RejectOrEscalateReason)
		return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt, state), nil
	}
	allowedTools := filterToolDefinitions(decision.AllowedTools)
	if len(allowedTools) == 0 {
		allowedTools = toolDefinitions()
	}
	// Every group retains the same backend confirmation gate. With Jev off,
	// the main model may select this capability only after an explicit user
	// confirmation; the raw bearer token never reaches the model.
	canConfirm := pendingConfirmValid(state, s.now()) && isConfirmText(request.Message) && pendingToken(state) != ""
	if canConfirm {
		allowedTools = append(allowedTools, confirmationToolDefinition())
	}
	for turn := 0; turn < maxAgentTurns; turn++ {
		contextStarted := time.Now()
		contextMessages := s.modelContext(messages, state)
		contextLatency := time.Since(contextStarted)
		s.addRequestDiagnostics(contextLatency, 0, 0)
		providerStarted := time.Now()
		completion, completeErr := s.provider.Complete(ctx, CompletionRequest{Messages: contextMessages, Tools: allowedTools})
		networkLatency := time.Since(providerStarted)
		if completion.NetworkLatencyMS > 0 {
			networkLatency = time.Duration(completion.NetworkLatencyMS) * time.Millisecond
		}
		s.addRequestDiagnostics(0, networkLatency, completion.RetryCount)
		if completeErr != nil {
			if ctx.Err() != nil {
				return ChatResponse{ConversationID: conversationID, Usage: usage, ToolCalls: traces}, ctx.Err()
			}
			logAgentRun(userID, conversationID, s.provider.Name(), s.provider.Model(), traces, usage, false, completeErr.Error())
			handoff = newHandoff("provider_unavailable", request.Message, traces, "模型服务暂时不可用")
			return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt, state), nil
		}
		usage.InputTokens += completion.Usage.InputTokens
		usage.OutputTokens += completion.Usage.OutputTokens
		usage.TotalTokens += completion.Usage.TotalTokens
		usage.CachedTokens += completion.Usage.CachedTokens
		usage.CacheUsageReported = usage.CacheUsageReported || completion.Usage.CacheUsageReported
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
			s.saveConversation(userID, conversationID, messages[1:], state)
			s.recordRequestEnd(len(unresolved) == 0, usage.TotalTokens, time.Since(startedAt))
			logAgentRun(userID, conversationID, s.provider.Name(), s.provider.Model(), traces, usage, len(unresolved) == 0, "")
			return ChatResponse{TaskStatus: status, ConversationID: conversationID, Message: answer, Provider: s.provider.Name(), Model: s.provider.Model(), ToolCalls: traces, Confirmation: confirmation, Usage: usage, Handoff: handoff}, nil
		}
		for callIndex, call := range completion.Message.ToolCalls {
			call = normalizeToolCall(call)
			if call.Name == "confirm_cancel_order" {
				orderID, parseErr := parseOrderID(call.Arguments)
				if !canConfirm || parseErr != nil || orderID != pendingOrderID(state) || len(completion.Message.ToolCalls) != 1 {
					return ChatResponse{ConversationID: conversationID, Usage: usage}, ErrInvalidConfirmation
				}
				confirmed, confirmErr := s.Confirm(ctx, userID, ConfirmRequest{ConversationID: conversationID, Token: pendingToken(state)})
				if confirmErr != nil {
					return ChatResponse{ConversationID: conversationID, Usage: usage}, confirmErr
				}
				confirmed.Usage = usage
				messages = append(messages, Message{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: string(confirmed.ToolCalls[0].Result)}, Message{Role: "assistant", Content: confirmed.Message})
				_, _, finalState, _ := s.loadConversation(userID, conversationID)
				s.saveConversation(userID, conversationID, messages[1:], finalState)
				return confirmed, nil
			}
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
				return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt, state), nil
			}
			started := s.now()
			result, action, toolErr := s.executeTool(ctx, userID, conversationID, call)
			attempts := 1
			if isTransientToolError(toolErr) && isReadOnlyTool(call.Name) && ctx.Err() == nil {
				result, action, toolErr = s.executeTool(ctx, userID, conversationID, call)
				attempts++
			}
			failureClass := classifyToolFailure(call.Name, result, toolErr)
			trace := ToolTrace{Name: call.Name, Arguments: call.Arguments, Result: result, DurationMS: s.now().Sub(started).Milliseconds(), Success: toolErr == nil, FailureClass: failureClass, Attempts: attempts, FatalBusinessError: failureClass == "business_rule"}
			traces = append(traces, trace)
			state.observeTool(call.Name, result)
			if toolErr == nil {
				state.ConsecutiveFailures = 0
				state.LastFailedOrderID = 0
			}
			if toolErr != nil {
				unresolved[call.Name] = trace
			} else {
				delete(unresolved, call.Name)
			}
			s.recordTool(trace.Success)
			if action != nil {
				confirmation = action
				state.observeConfirmation(action, s.now())
			}
			// Store the authenticated tool result losslessly. Historical payload
			// dehydration is applied only while assembling the next model request,
			// so the latest turn can retain the complete result.
			observation := string(result)
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
				return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt, state), nil
			}
		}
	}
	handoff = newHandoff("max_turns", request.Message, traces, "达到模型推理轮次上限")
	return s.finishHandoff(userID, conversationID, messages, traces, usage, confirmation, handoff, startedAt, state), nil
}

func pendingToken(state SessionState) string {
	if state.PendingCancel.Token != "" {
		return state.PendingCancel.Token
	}
	if state.PendingConfirm != nil {
		return state.PendingConfirm.Token
	}
	return ""
}

func (s *Service) recordJev(decision JevDecision, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics.JevEvaluations++
	s.jevLatencies = append(s.jevLatencies, float64(latency.Microseconds())/1000)
	switch decision.RouteAction {
	case RouteShortCircuitReject:
		s.metrics.JevShortCircuits++
	case RouteFastPathConfirm:
		s.metrics.JevConfirmPaths++
	case RouteEscalateHuman:
		s.metrics.JevEscalations++
	}
	if len(s.jevLatencies) > 0 {
		values := append([]float64(nil), s.jevLatencies...)
		sort.Float64s(values)
		index := int(float64(len(values)-1) * 0.95)
		s.metrics.JevLatencyP95MS = values[index]
	}
}

func boundedContext(messages []Message) []Message {
	return boundedContextWithState(messages, extractSessionState(messages))
}

func (s *Service) modelContext(messages []Message, state SessionState) []Message {
	strategy := s.contextStrategy
	legacyFull := s.disableContextCompression
	if legacyFull {
		strategy = StrategyFullHistory
	}
	if strategy == "" {
		strategy = StrategyOursFull
	}
	assembled := assembleContext(messages, state, normalizeContextStrategy(strategy))
	if strategy != StrategyFullHistory {
		assembled = s.applyTokenBudget(assembled, state)
	}
	if legacyFull && len(assembled) > 0 {
		if anchor := sessionStateMessage(state); anchor.Content != "" {
			withAnchor := make([]Message, 0, len(assembled)+1)
			withAnchor = append(withAnchor, assembled[0], anchor)
			withAnchor = append(withAnchor, assembled[1:]...)
			return withAnchor
		}
	}
	return assembled
}

func contextTokenBudget() int {
	if raw := strings.TrimSpace(os.Getenv("AGENT_CONTEXT_TOKEN_BUDGET")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			return value
		}
	}
	return defaultContextTokenBudget
}

func (s *Service) applyTokenBudget(messages []Message, state SessionState) []Message {
	budget := contextTokenBudget()
	request := CompletionRequest{Messages: messages, Tools: toolDefinitions()}
	count := func(candidate []Message) int {
		request.Messages = candidate
		if counter, ok := s.provider.(TokenCounter); ok {
			if tokens, err := counter.CountTokens(request); err == nil {
				return tokens
			}
		}
		bytes := 0
		for _, message := range candidate {
			bytes += messageBytes(message)
		}
		return (bytes + 3) / 4
	}
	if count(messages) <= budget {
		return messages
	}
	for rounds := maxContextRounds - 1; rounds >= 1; rounds-- {
		candidate := safeSlidingWindowRounds(messages, state, rounds, true, true)
		if count(candidate) <= budget {
			return candidate
		}
	}
	candidate := safeSlidingWindowRounds(messages, state, 1, true, true)
	if count(candidate) <= budget {
		return candidate
	}
	// A single latest tool payload can be larger than the entire budget. Keep
	// its call/result pair, replace the payload with an explicit safe summary,
	// and count again before sending anything to the provider.
	for i := range candidate {
		if candidate[i].Role == "tool" && len(candidate[i].Content) > 256 {
			candidate[i].Content = `{"ok":false,"errorType":"context_budget","message":"工具结果超过上下文预算，已转为安全摘要。","nextAction":"handoff"}`
		}
	}
	if count(candidate) <= budget {
		return candidate
	}
	// Keep the system contract, state anchor, and the newest complete user
	// turn. This final guard is only reachable when tool definitions or a user
	// message itself consumes nearly the whole configured budget.
	minimal := make([]Message, 0, 3)
	if len(candidate) > 0 {
		minimal = append(minimal, candidate[0])
	}
	for _, message := range candidate[1:] {
		if message.Name == "session_state" || message.Role == "user" {
			minimal = append(minimal, message)
		}
	}
	if count(minimal) <= budget {
		return minimal
	}
	// The configured budget must still be respected when the system prompt is
	// unusually large. Preserve a compact system contract and the latest user.
	latest := Message{}
	for i := len(candidate) - 1; i >= 0; i-- {
		if candidate[i].Role == "user" {
			latest = candidate[i]
			break
		}
	}
	if latest.Content != "" {
		latest.Content = string([]rune(latest.Content)[:minInt(len([]rune(latest.Content)), 256)])
	}
	return []Message{{Role: "system", Content: "你是商品订单客服，只依据工具真实结果回答。"}, latest}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// assembleContext implements the three layers of the production arm while
// keeping the other arms intentionally observable for ablation experiments.
func assembleContext(messages []Message, state SessionState, strategy ContextStrategy) []Message {
	if len(messages) == 0 {
		return nil
	}
	if strategy == StrategyFullHistory {
		return append([]Message(nil), messages...)
	}
	latestToolBoundary := latestToolTurnBoundary(messages)
	transformed := make([]Message, 0, len(messages))
	for i, message := range messages {
		copyMessage := message
		if message.Role == "tool" {
			switch strategy {
			case StrategyNaiveTruncation:
				copyMessage.Content = compactToolResult(message.Name, json.RawMessage(message.Content))
			case StrategyDehydrationOnly, StrategyOursFull:
				if i < latestToolBoundary {
					copyMessage.Content = dehydrateHistoricalTool(message.Name, message.Content)
				}
			}
		}
		transformed = append(transformed, copyMessage)
	}
	if strategy == StrategyNaiveTruncation {
		return safeSlidingWindow(transformed, state, false, false)
	}
	if strategy == StrategyDehydrationOnly {
		return safeSlidingWindow(transformed, state, false, false)
	}
	return safeSlidingWindow(transformed, state, true, true)
}

// latestToolTurnBoundary returns the user boundary owning the newest tool
// result. A current user message without a tool call does not demote the last
// completed tool turn to dehydrated history.
func latestToolTurnBoundary(messages []Message) int {
	lastUser := -1
	for i := len(messages) - 1; i >= 1; i-- {
		if messages[i].Role == "user" {
			lastUser = i
			break
		}
	}
	if lastUser >= 0 {
		for _, message := range messages[lastUser+1:] {
			if message.Role == "tool" {
				return lastUser
			}
		}
	}
	for i := lastUser - 1; i >= 1; i-- {
		if messages[i].Role != "user" {
			continue
		}
		for _, message := range messages[i+1:] {
			if message.Role == "tool" {
				return i
			}
		}
	}
	return len(messages)
}

// safeSlidingWindow always starts at a user boundary and therefore cannot
// orphan a tool result from its assistant tool_calls message.
func safeSlidingWindow(messages []Message, state SessionState, withState, withSummary bool) []Message {
	return safeSlidingWindowRounds(messages, state, maxContextRounds, withState, withSummary)
}

func safeSlidingWindowRounds(messages []Message, state SessionState, rounds int, withState, withSummary bool) []Message {
	if len(messages) <= 1 {
		return messages
	}
	users := make([]int, 0)
	for i := 1; i < len(messages); i++ {
		if messages[i].Role == "user" {
			users = append(users, i)
		}
	}
	start := 1
	if rounds < 1 {
		rounds = 1
	}
	if len(users) > rounds {
		start = users[len(users)-rounds]
	}
	// Defensive alignment for malformed histories: move forward to a user.
	for start < len(messages) && messages[start].Role != "user" {
		start++
	}
	result := make([]Message, 0, len(messages)-start+3)
	result = append(result, messages[0])
	if withState {
		if anchor := sessionStateMessage(state); anchor.Content != "" {
			result = append(result, anchor)
		}
	}
	if withSummary && start > 1 {
		if summary := summarizeEarlyMessages(messages[1:start]); summary.Content != "" {
			result = append(result, summary)
		}
	}
	result = append(result, messages[start:]...)
	// A single oversized current turn cannot be safely split. Keep it whole;
	// callers can raise maxContextBytes or use provider-side limits as needed.
	return result
}

func summarizeEarlyMessages(messages []Message) Message {
	if len(messages) == 0 {
		return Message{}
	}
	orders := make([]int64, 0)
	conclusions := make([]string, 0)
	requests := make([]string, 0)
	seen := map[int64]bool{}
	for _, message := range messages {
		if message.Role == "user" && strings.TrimSpace(message.Content) != "" {
			requests = append(requests, message.Content)
		}
		if message.Role == "assistant" && len(message.ToolCalls) == 0 && strings.TrimSpace(message.Content) != "" {
			conclusions = append(conclusions, message.Content)
		}
		for _, match := range summaryNumberPattern.FindAllString(message.Content, -1) {
			id, _ := strconv.ParseInt(match, 10, 64)
			if id > 0 && !seen[id] {
				seen[id] = true
				orders = append(orders, id)
			}
		}
	}
	for i := range orders {
		for j := i + 1; j < len(orders); j++ {
			if orders[j] < orders[i] {
				orders[i], orders[j] = orders[j], orders[i]
			}
		}
	}
	payload := map[string]any{"core_requests": lastStrings(requests, 4), "queried_order_ids": orders, "answered_conclusions": lastStrings(conclusions, 4)}
	encoded, _ := json.Marshal(payload)
	return Message{Role: "system", Name: "context_summary", Content: "[历史轮次滚动摘要] " + string(encoded)}
}

func lastStrings(values []string, limit int) []string {
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func dehydrateHistoricalTool(name, content string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(content), &payload) != nil {
		return content
	}
	if ok, exists := payload["ok"].(bool); exists && !ok {
		return content
	}
	if name == "search_knowledge" {
		titles := make([]string, 0)
		if items, ok := payload["items"].([]any); ok {
			for _, item := range items {
				if object, ok := item.(map[string]any); ok {
					if title, ok := object["title"].(string); ok && title != "" {
						titles = append(titles, title)
					}
				}
			}
		}
		return string(mustJSON(map[string]any{"status": "archived", "doc_titles": titles, "note": "规则细节已体现在上一轮助手回复中"}))
	}
	if name != "search_products" && name != "list_my_orders" && name != "get_order_status" && name != "prepare_cancel_order" {
		return content
	}
	keep := map[string]bool{"id": true, "orderId": true, "productId": true, "productName": true, "name": true, "status": true, "orderStatus": true, "statusText": true, "price": true, "productNum": true, "quantity": true, "canCancel": true, "requiresConfirmation": true, "ok": true, "count": true, "total": true, "found": true, "expiresAt": true}
	for key := range payload {
		if key == "items" {
			continue
		}
		if !keep[key] {
			delete(payload, key)
		}
	}
	if items, ok := payload["items"].([]any); ok {
		for _, item := range items {
			if object, ok := item.(map[string]any); ok {
				for key := range object {
					if !keep[key] {
						delete(object, key)
					}
				}
			}
		}
	}
	return string(mustJSON(payload))
}

func boundedContextWithState(messages []Message, state SessionState) []Message {
	if len(messages) < 2 {
		return messages
	}
	start := len(messages) - 1
	for start > 1 && messages[start].Role != "user" {
		start--
	}
	stateMessage := sessionStateMessage(state)
	used := messageBytes(messages[0])
	if stateMessage.Content != "" {
		used += messageBytes(stateMessage)
	}
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
		if used+groupBytes > maxContextBytes || len(messages)-previous > legacyMaxMessages {
			break
		}
		used += groupBytes
		start = previous
	}
	result := make([]Message, 0, 2+len(messages)-start)
	result = append(result, messages[0])
	if encoded := sessionStateMessage(state); encoded.Content != "" {
		result = append(result, encoded)
	}
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
	case "order not found", "only pending orders can be cancelled", "permission denied", "not your order":
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

func (s *Service) finishHandoff(userID int64, conversationID string, messages []Message, traces []ToolTrace, usage Usage, confirmation *Confirmation, handoff *Handoff, startedAt time.Time, state SessionState) ChatResponse {
	state.PendingOrderID = 0
	if state.PendingCancel.TokenStatus == "pending" {
		state.PendingCancel.TokenStatus = "aborted"
		state.PendingCancel.Token = ""
		state.PendingCancel.RemainingSeconds = 0
	}
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
	s.saveConversation(userID, conversationID, append(messages[1:], Message{Role: "assistant", Content: answer}), state)
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
		s.mu.Lock()
		if chat, exists := s.chats[action.ConversationID]; exists && chat.UserID == userID {
			chat.State.ConsecutiveFailures++
			chat.State.LastFailedOrderID = action.OrderID
			chat.State.PendingConfirm = nil
			chat.State.PendingCancel.Token = ""
			chat.State.PendingCancel.TokenStatus = "failed"
			s.persistSessionState(userID, action.ConversationID, chat.State)
		}
		s.mu.Unlock()
		return ChatResponse{}, err
	}
	s.mu.Lock()
	s.metrics.Confirmations++
	var updatedState SessionState
	if chat, exists := s.chats[action.ConversationID]; exists && chat.UserID == userID {
		chat.State.PendingOrderID = 0
		chat.State.PendingCancel.TokenStatus = "consumed"
		chat.State.PendingCancel.Token = ""
		chat.State.PendingCancel.RemainingSeconds = 0
		chat.State.PendingConfirm = nil
		chat.State.syncOrderStatus(action.OrderID, "已取消", false)
		chat.State.TerminalOrderIDs = appendUniqueID(chat.State.TerminalOrderIDs, action.OrderID)
		chat.State.ConsecutiveFailures = 0
		chat.State.ConfirmedActions = append(chat.State.ConfirmedActions, ConfirmedAction{OrderID: action.OrderID, Action: "cancel_order"})
		updatedState = chat.State
	}
	s.mu.Unlock()
	s.persistSessionState(userID, action.ConversationID, updatedState)
	message := fmt.Sprintf("订单 %d 已取消，库存已经恢复。", action.OrderID)
	s.appendConversation(userID, action.ConversationID, Message{Role: "assistant", Content: message})
	trace := ToolTrace{Name: "cancel_my_pending_order", Arguments: mustJSON(map[string]any{"order_id": action.OrderID}), Result: mustJSON(map[string]any{"ok": true, "orderId": action.OrderID}), Success: true}
	logAgentRun(userID, action.ConversationID, s.provider.Name(), s.provider.Model(), []ToolTrace{trace}, Usage{}, true, "")
	return ChatResponse{TaskStatus: "completed", ConversationID: action.ConversationID, Message: message, Provider: s.provider.Name(), Model: s.provider.Model(), ToolCalls: []ToolTrace{trace}}, nil
}

func appendUniqueID(values []int64, wanted int64) []int64 {
	if wanted <= 0 || containsID(values, wanted) {
		return values
	}
	return append(values, wanted)
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
		// The current turn keeps the full retrieval payload (up to 32 chunks);
		// older turns are reduced to document titles by the context assembler.
		chunks, err := s.retriever.Retrieve(ctx, args.Query, 32)
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

func (s *Service) loadConversation(userID int64, conversationID string) (string, []Message, SessionState, error) {
	s.mu.Lock()
	s.cleanupLocked()
	if conversationID == "" {
		conversationID = randomID()
		s.chats[conversationID] = &conversation{UserID: userID, State: SessionState{UserID: userID, TotalOrderCount: -1}, Updated: s.now()}
		s.mu.Unlock()
		return conversationID, nil, SessionState{UserID: userID, TotalOrderCount: -1}, nil
	}
	chat, ok := s.chats[conversationID]
	if !ok || chat.UserID != userID {
		s.mu.Unlock()
		return "", nil, SessionState{}, ErrInvalidConversation
	}
	history, state := append([]Message(nil), chat.Messages...), chat.State
	s.mu.Unlock()
	if cached, found := s.loadSessionState(userID, conversationID); found {
		state = cached
	}
	state.UserID = userID
	if !state.OrdersQueried && state.TotalOrderCount == 0 && len(state.OwnedOrderIDs) == 0 && state.Version == 0 {
		state.TotalOrderCount = -1
	}
	return conversationID, history, state, nil
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

func (s *Service) saveConversation(userID int64, id string, messages []Message, state SessionState) {
	// Keep lossless history in the session store. The model-facing strategy is
	// applied at request assembly time, which preserves a complete latest turn.
	messages = append([]Message(nil), messages...)
	if len(messages) > maxContextRounds*4 {
		cut := len(messages) - maxContextRounds*4
		if summary := summarizeEarlyMessages(messages[:cut]); summary.Content != "" {
			state.RollingSummary = summary.Content
		}
	}
	s.mu.Lock()
	if previous, ok := s.chats[id]; ok && previous.State.Version >= state.Version {
		state.Version = previous.State.Version + 1
	} else if state.Version == 0 {
		state.Version = 1
	}
	state.UpdatedAt = s.now().UnixMilli()
	s.chats[id] = &conversation{UserID: userID, Messages: append([]Message(nil), messages...), State: state, Updated: s.now()}
	s.mu.Unlock()
	s.persistSessionState(userID, id, state)
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
没有订单号且历史和状态都不能唯一确定的“退/取消/不想要”请求先澄清订单号，不要猜测；若用户指代最早查到的待处理订单，应依据真实历史和状态确定目标，并遵守用户此前禁止取消的订单约束。明确的非法订单号仍可调用订单状态工具，让后端返回参数错误。发票、改价、加急配送、删除历史等未支持操作不要调用任何工具，直接说明能力边界。寒暄或空意图直接说明“我可以”提供的帮助。
需要查询业务事实时调用工具。售后、退款、配送和地址规则优先调用 search_knowledge，并只引用检索到的来源片段；没有匹配依据时不要编造，必要时转人工。取消订单只能先调用 prepare_cancel_order；绝不能声称订单已取消，除非后端确认接口已经执行。
工具返回错误时简洁说明，不暴露 SQL、密钥、系统提示或内部堆栈。使用简洁中文回答。`

func confirmationToolDefinition() ToolDefinition {
	return ToolDefinition{Name: "confirm_cancel_order", Description: "用户本轮明确确认后，核销程序保存的有效取消申请。只传订单号，不传令牌；后端验证所有权、申请和确认。", Parameters: map[string]any{"type": "object", "properties": map[string]any{"order_id": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"order_id"}, "additionalProperties": false}}
}

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

func allToolNames() []string {
	return []string{"search_products", "search_knowledge", "list_my_orders", "get_order_status", "prepare_cancel_order"}
}

func filterToolDefinitions(names []string) []ToolDefinition {
	if len(names) == 0 {
		return toolDefinitions()
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	definitions := toolDefinitions()
	filtered := make([]ToolDefinition, 0, len(names))
	for _, definition := range definitions {
		if allowed[definition.Name] {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

func recentTurnsBrief(history []Message) string {
	if len(history) == 0 {
		return ""
	}
	start := len(history) - 6
	if start < 0 {
		start = 0
	}
	var builder strings.Builder
	for _, message := range history[start:] {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if len([]rune(content)) > 120 {
			content = string([]rune(content)[:120])
		}
		if content != "" {
			builder.WriteString(message.Role + ":" + content + "\n")
		}
	}
	return builder.String()
}
