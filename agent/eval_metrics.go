package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type longContextTurn struct {
	User            string         `json:"user"`
	ExpectedTool    string         `json:"expectedTool"`
	ExpectedOrderID int64          `json:"expectedOrderID,omitempty"`
	GoldRoute       JevRouteAction `json:"goldRoute,omitempty"`
}

type longContextCase struct {
	ID       string            `json:"id"`
	Scenario string            `json:"scenario"`
	Turns    []longContextTurn `json:"turns"`
}

// EvalRecord is the common per-turn evidence schema used by offline replay
// and live Provider runs. Latencies are measured independently so their sum
// can be compared with TotalLatencyMS instead of hiding Jev work in LLM time.
type EvalRecord struct {
	Group                  string         `json:"group"`
	Dataset                string         `json:"dataset,omitempty"`
	Case                   string         `json:"case"`
	Turn                   int            `json:"turn"`
	InputTokens            int            `json:"inputTokens,omitempty"`
	OutputTokens           int            `json:"outputTokens,omitempty"`
	TotalTokens            int            `json:"totalTokens,omitempty"`
	CachedTokens           int            `json:"cachedTokens,omitempty"`
	TotalLatencyMS         int64          `json:"totalLatencyMs"`
	ContextLatencyMS       int64          `json:"contextLatencyMs,omitempty"`
	LLMNetworkLatencyMS    int64          `json:"llmNetworkLatencyMs,omitempty"`
	JevLatencyMS           int64          `json:"jevLatencyMs,omitempty"`
	RetryCount             int            `json:"retryCount,omitempty"`
	LatencyInstrumented    bool           `json:"latencyInstrumented,omitempty"`
	OtherLatencyMS         int64          `json:"otherLatencyMs,omitempty"`
	CacheUsageReported     bool           `json:"cacheUsageReported,omitempty"`
	ProviderCalls          int            `json:"providerCalls,omitempty"`
	CancelledBeforeConfirm bool           `json:"cancelledBeforeConfirm,omitempty"`
	RouteCorrect           bool           `json:"routeCorrect"`
	ToolNameCorrect        bool           `json:"toolNameCorrect"`
	ArgumentsCorrect       bool           `json:"argumentsCorrect"`
	CorefCorrect           bool           `json:"corefCorrect"`
	TaskPassed             bool           `json:"taskPassed"`
	ConstraintRecalled     bool           `json:"constraintRecalled"`
	APIError               bool           `json:"apiError"`
	Route                  JevRouteAction `json:"route,omitempty"`
	ExpectedRoute          JevRouteAction `json:"expectedRoute,omitempty"`
	Status                 string         `json:"status,omitempty"`
	Tools                  []ToolTrace    `json:"tools,omitempty"`
	Error                  string         `json:"error,omitempty"`
}

// Core8StratifiedSample selects the requested 3/2/1/1/1 composition from a
// 25-case corpus. The classifier accepts explicit Scenario labels and falls
// back to stable keywords so generated fixtures remain usable.
func Core8StratifiedSample(cases []longContextCase) []longContextCase {
	want := map[string]int{"coref": 3, "json": 2, "constraint": 1, "retry": 1, "越权": 1}
	got := make(map[string]int)
	out := make([]longContextCase, 0, 8)
	for _, c := range cases {
		kind := classifyLongCase(c)
		if got[kind] >= want[kind] || want[kind] == 0 {
			continue
		}
		out = append(out, c)
		got[kind]++
	}
	if len(out) != 8 {
		return nil
	}
	return out
}

func classifyLongCase(c longContextCase) string {
	text := strings.ToLower(c.Scenario)
	if strings.Contains(text, "越权") || strings.Contains(text, "安全") {
		return "越权"
	}
	if strings.Contains(text, "重试") || strings.Contains(text, "错误") {
		return "retry"
	}
	if strings.Contains(text, "约束") || strings.Contains(text, "记忆") {
		return "constraint"
	}
	if strings.Contains(text, "json") || strings.Contains(text, "载荷") || strings.Contains(text, "巨型") {
		return "json"
	}
	return "coref"
}

// TeacherForcedHistory makes every strategy see the same gold prefix. Model
// errors therefore cannot cascade into later turns during replay.
func TeacherForcedHistory(system string, turns []longContextTurn, upto int, payload func(longContextTurn, int) string) []Message {
	if upto < 0 {
		upto = 0
	}
	if upto > len(turns) {
		upto = len(turns)
	}
	messages := []Message{{Role: "system", Content: system}}
	for i := 0; i < upto; i++ {
		turn := turns[i]
		callID := fmt.Sprintf("teacher-%d", i)
		args := mustJSON(map[string]any{})
		if turn.ExpectedOrderID > 0 {
			args = mustJSON(map[string]any{"order_id": turn.ExpectedOrderID})
		}
		messages = append(messages,
			Message{Role: "user", Content: turn.User},
			Message{Role: "assistant", ToolCalls: []ToolCall{{ID: callID, Name: turn.ExpectedTool, Arguments: args}}},
			Message{Role: "tool", Name: turn.ExpectedTool, ToolCallID: callID, Content: payload(turn, i)},
			Message{Role: "assistant", Content: "已根据业务结果完成本轮答复。"},
		)
	}
	return messages
}

// MatchToolArguments uses exact matching for IDs/enums and keyword matching
// for natural-language query fields, as required by the replay protocol.
func MatchToolArguments(expected, observed map[string]any) bool {
	for key, want := range expected {
		got, ok := observed[key]
		if !ok {
			return false
		}
		if key == "query" || key == "keyword" || key == "reason" {
			wantText, _ := want.(string)
			gotText, _ := got.(string)
			for _, token := range strings.Fields(strings.ToLower(wantText)) {
				if len([]rune(token)) >= 2 && !strings.Contains(strings.ToLower(gotText), token) {
					return false
				}
			}
			continue
		}
		wb, _ := json.Marshal(want)
		gb, _ := json.Marshal(got)
		if string(wb) != string(gb) {
			return false
		}
	}
	return true
}

func latencyParts(total time.Duration, context, jev, llm time.Duration) (int64, int64, int64, int64) {
	return total.Milliseconds(), context.Milliseconds(), jev.Milliseconds(), llm.Milliseconds()
}
