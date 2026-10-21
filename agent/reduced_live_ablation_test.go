package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"product/datamodels"
)

// TestReducedLiveAblation is the bounded online evaluation profile. It keeps
// the two canonical eight-turn conversations and the four causal groups while
// avoiding the 100-case synthetic stress corpus.
func TestReducedLiveAblation(t *testing.T) {
	if os.Getenv("AGENT_REDUCED_LIVE") != "1" {
		t.Skip("set AGENT_REDUCED_LIVE=1 for the bounded paid evaluation")
	}
	provider := NewProviderFromEnv()
	if provider.Name() == "local" {
		t.Fatal("reduced live evaluation requires AI_PROVIDER/API key")
	}
	data, err := os.ReadFile("testdata/long_context_challenges.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []longContextCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 1 {
		t.Fatalf("reduced live evaluation requires at least one canonical case")
	}
	caseLimit := 2
	if raw := strings.TrimSpace(os.Getenv("AGENT_REDUCED_LIVE_CASES")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed >= 1 && parsed <= len(cases) {
			caseLimit = parsed
		} else {
			t.Fatalf("AGENT_REDUCED_LIVE_CASES must be between 1 and %d", len(cases))
		}
	}
	cases = cases[:caseLimit]
	core8 := Core8StratifiedSample(cases)
	if len(cases) >= 25 && len(core8) != 8 {
		t.Fatalf("25-case evaluation requires a stratified Core-8 subset (3 coref, 2 json, 1 constraint, 1 retry, 1 privilege)")
	}
	if len(cases) == 8 && len(core8) != 8 {
		t.Fatalf("Core-8 fixture must contain 3 coref, 2 json, 1 constraint, 1 retry, and 1 privilege case")
	}

	groups := []struct {
		Name     string
		Strategy ContextStrategy
		Jev      bool
	}{
		{Name: "A Full_History / Jev_Off", Strategy: StrategyFullHistory},
		{Name: "B Full_History / Jev_On", Strategy: StrategyFullHistory, Jev: true},
		{Name: "D Three_Tier / Jev_Off", Strategy: StrategyDehydrationOnly},
		{Name: "E Three_Tier / Jev_On", Strategy: StrategyOursFull, Jev: true},
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_REDUCED_LIVE_GROUPS")); raw != "" {
		allowed := make(map[string]bool)
		for _, name := range strings.Split(raw, ",") {
			allowed[strings.TrimSpace(name)] = true
		}
		filtered := groups[:0]
		for _, group := range groups {
			if allowed[group.Name[:1]] || allowed[group.Name[:1]+" "] {
				filtered = append(filtered, group)
			}
		}
		if len(filtered) == 0 {
			t.Fatalf("AGENT_REDUCED_LIVE_GROUPS did not select a group: %s", raw)
		}
		groups = filtered
	}

	checkpointPath := strings.TrimSpace(os.Getenv("AGENT_REDUCED_LIVE_CHECKPOINT"))
	if checkpointPath == "" {
		checkpointPath = "../docs/reduced-live-ablation.partial.json"
	}
	records := make([]EvalRecord, 0, len(groups)*len(cases)*8)
	// Every recorded turn is immutable evidence, including failed API attempts.
	if existing, readErr := os.ReadFile(checkpointPath); readErr == nil {
		var checkpoint struct {
			Records []EvalRecord `json:"records"`
		}
		if json.Unmarshal(existing, &checkpoint) == nil {
			records = append(records, checkpoint.Records...)
		}
	}
	completed := make(map[string]bool, len(records))
	for _, item := range records {
		completed[evalRecordKey(item.Group, item.Case, item.Turn)] = true
	}
	writeCheckpoint := func() {
		if err := os.MkdirAll(filepath.Dir(checkpointPath), 0755); err != nil {
			return
		}
		payload := map[string]any{"provider": provider.Name(), "model": provider.Model(), "cases": len(cases), "turnsPerCase": 8, "groups": len(groups), "records": records, "complete": false}
		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err == nil {
			_ = os.WriteFile(checkpointPath, append(encoded, '\n'), 0600)
		}
	}
	timeout := 60 * time.Second
	if raw := strings.TrimSpace(os.Getenv("AGENT_REDUCED_LIVE_TURN_TIMEOUT_SEC")); raw != "" {
		if seconds, parseErr := strconv.Atoi(raw); parseErr == nil && seconds > 0 && seconds <= 300 {
			timeout = time.Duration(seconds) * time.Second
		}
	}
	// A retry must fit inside the outer Chat context; otherwise the second
	// attempt is guaranteed to be cancelled and reported as a false timeout.
	providerTimeout := 35 * time.Second
	if raw := strings.TrimSpace(os.Getenv("AI_REQUEST_TIMEOUT_MS")); raw != "" {
		if millis, parseErr := strconv.Atoi(raw); parseErr == nil && millis > 0 {
			providerTimeout = time.Duration(millis) * time.Millisecond
		}
	}
	retries := 0
	if raw := strings.TrimSpace(os.Getenv("AI_PROVIDER_MAX_RETRIES")); raw != "" {
		retries, _ = strconv.Atoi(raw)
		if retries < 0 {
			retries = 0
		}
	}
	minimumTimeout := providerTimeout*time.Duration(retries+1) + 10*time.Second
	if timeout < minimumTimeout {
		timeout = minimumTimeout
	}
	maxEvalTokens := 5_567_765 // 1,567,765 immutable calibration + 4M incremental maximum
	if raw := strings.TrimSpace(os.Getenv("AGENT_REDUCED_LIVE_MAX_TOKENS")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			maxEvalTokens = parsed
		}
	}
	budgetReserve := 100_000
	if raw := strings.TrimSpace(os.Getenv("AGENT_REDUCED_LIVE_BUDGET_RESERVE")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed >= 0 {
			budgetReserve = parsed
		}
	}
	budgetStopped := false
	for _, group := range groups {
		if budgetStopped {
			break
		}
		runCases := cases
		if len(cases) >= 25 && (strings.HasPrefix(group.Name, "A ") || strings.HasPrefix(group.Name, "B ")) {
			runCases = core8
		}
		for _, testCase := range runCases {
			if budgetStopped {
				break
			}
			caseComplete := true
			for turnIndex := range testCase.Turns {
				if !completed[evalRecordKey(group.Name, testCase.ID, turnIndex+1)] {
					caseComplete = false
					break
				}
			}
			if caseComplete {
				t.Logf("CHECKPOINT SKIP %s/%s (8 recorded turns)", group.Name, testCase.ID)
				continue
			}
			// Never replay a partial conversation without its persisted service state.
			for _, item := range records {
				if item.Group == group.Name && item.Case == testCase.ID {
					t.Fatalf("partial case %s/%s requires snapshot recovery; refusing paid replay", group.Name, testCase.ID)
				}
			}
			orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
			products := &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "键盘", ProductNum: 3}}}
			svc := NewServiceWithRedisAndKnowledge(provider, orders, products, nil, bulkAblationRetriever{})
			svc.SetContextStrategy(group.Strategy)
			svc.jev.enabled = group.Jev
			conversationID := ""
			for index, turn := range testCase.Turns {
				usedTokens := 0
				for _, record := range records {
					usedTokens += record.TotalTokens
				}
				if usedTokens >= maxEvalTokens-budgetReserve {
					t.Logf("stopping before token budget redline: used=%d max=%d reserve=%d", usedTokens, maxEvalTokens, budgetReserve)
					budgetStopped = true
					break
				}
				key := evalRecordKey(group.Name, testCase.ID, index+1)
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				started := time.Now()
				response, chatErr := svc.Chat(ctx, 42, ChatRequest{ConversationID: conversationID, Message: turn.User})
				cancel()
				jevLatency, route := svc.LastJevDiagnostics()
				contextLatency, llmNetworkLatency, retryCount := svc.LastRequestDiagnostics()
				expectedRoute := expectedRouteForTurn(turn)
				dataset := "Full-25"
				if len(cases) < 25 || len(runCases) == 8 {
					dataset = "Core-8"
				}
				item := EvalRecord{Group: group.Name, Dataset: dataset, Case: testCase.ID, Turn: index + 1, TotalLatencyMS: time.Since(started).Milliseconds(), ContextLatencyMS: contextLatency.Milliseconds(), LLMNetworkLatencyMS: llmNetworkLatency.Milliseconds(), JevLatencyMS: jevLatency.Milliseconds(), RetryCount: retryCount, Route: route, ExpectedRoute: expectedRoute, RouteCorrect: expectedRoute == "" || route == expectedRoute, Status: response.TaskStatus, Tools: response.ToolCalls, InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, TotalTokens: response.Usage.TotalTokens}
				item.LatencyInstrumented = true
				item.OtherLatencyMS = item.TotalLatencyMS - item.ContextLatencyMS - item.LLMNetworkLatencyMS - item.JevLatencyMS
				item.CachedTokens = response.Usage.CachedTokens
				item.CacheUsageReported = response.Usage.CacheUsageReported
				item.CancelledBeforeConfirm = orders.cancelCallCount > 0 && index < 7
				item.APIError = chatErr != nil || response.TaskStatus == "provider_unavailable" || (response.Handoff != nil && response.Handoff.Reason == "provider_unavailable")
				if chatErr != nil {
					item.Error = chatErr.Error()
				} else if item.APIError {
					item.Error = response.Message
				} else {
					conversationID = response.ConversationID
				}
				item.ToolNameCorrect, item.ArgumentsCorrect, item.CorefCorrect, item.TaskPassed = scoreLiveTurn(item, turn)
				records = append(records, item)
				completed[key] = true
				writeCheckpoint()
				t.Logf("SAVED %s/%s turn=%d tokens=%d retry=%d error=%v", group.Name, testCase.ID, index+1, item.TotalTokens, item.RetryCount, item.APIError)
				if item.APIError {
					t.Fatalf("stopped on API error; checkpoint retained: %s", item.Error)
				}
			}
		}
	}
	if budgetStopped {
		t.Fatalf("live evaluation stopped at token budget guard; inspect the checkpoint before extending the run")
	}

	type aggregate struct {
		Group            string  `json:"group"`
		Dataset          string  `json:"dataset"`
		Requests         int     `json:"requests"`
		Errors           int     `json:"errors"`
		InputTokens      int     `json:"inputTokens"`
		OutputTokens     int     `json:"outputTokens"`
		TotalTokens      int     `json:"totalTokens"`
		AvgInput         float64 `json:"avgInputTokens"`
		PeakInput        int     `json:"peakInputTokens"`
		PeakLongInput    int     `json:"peakInputTokensTurn6Plus"`
		LatencyMS        int64   `json:"-"`
		AvgLatency       float64 `json:"avgLatencyMs"`
		P50Latency       int64   `json:"p50LatencyMs"`
		P95Latency       int64   `json:"p95LatencyMs"`
		AvgContext       float64 `json:"avgContextLatencyMs"`
		AvgLLMNetwork    float64 `json:"avgLlmNetworkLatencyMs"`
		AvgJev           float64 `json:"avgJevLatencyMs"`
		TotalRetries     int     `json:"totalRetries"`
		LatencySamples   int     `json:"latencySamples"`
		ToolUseRate      float64 `json:"toolUseRate"`
		ToolNameAccuracy float64 `json:"toolNameAccuracy"`
		ArgumentAccuracy float64 `json:"argumentAccuracy"`
		CorefAccuracy    float64 `json:"corefAccuracy"`
		TaskCompletion   float64 `json:"taskCompletionRate"`
		RouteAccuracy    float64 `json:"routeAccuracy"`
	}
	aggs := make([]aggregate, 0, len(groups))
	for _, group := range groups {
		datasets := []string{"Core-8"}
		if len(cases) >= 25 {
			datasets = []string{"Core-8", "Full-25"}
		}
		for _, dataset := range datasets {
			a := aggregate{Group: group.Name, Dataset: dataset}
			latencies := make([]int64, 0)
			toolCount, toolNameCount, argsCount, corefCount, corefN, routeCount := 0, 0, 0, 0, 0, 0
			taskCount, taskN := 0, 0
			for _, item := range records {
				if item.Group != group.Name || item.Dataset != dataset {
					continue
				}
				a.Requests++
				a.InputTokens += item.InputTokens
				a.OutputTokens += item.OutputTokens
				a.TotalTokens += item.TotalTokens
				a.LatencyMS += item.TotalLatencyMS
				latencies = append(latencies, item.TotalLatencyMS)
				a.LatencySamples++
				a.AvgContext += float64(item.ContextLatencyMS)
				a.AvgLLMNetwork += float64(item.LLMNetworkLatencyMS)
				a.AvgJev += float64(item.JevLatencyMS)
				a.TotalRetries += item.RetryCount
				if item.InputTokens > a.PeakInput {
					a.PeakInput = item.InputTokens
				}
				if item.Turn >= 6 && item.InputTokens > a.PeakLongInput {
					a.PeakLongInput = item.InputTokens
				}
				if item.Error != "" {
					a.Errors++
				}
				turn, foundTurn := lookupGoldTurn(cases, item.Case, item.Turn)
				toolNameCorrect, argumentsCorrect, corefCorrect, taskPassed := false, false, false, false
				routeCorrect := item.RouteCorrect
				if foundTurn {
					toolNameCorrect, argumentsCorrect, corefCorrect, taskPassed = scoreLiveTurn(item, turn)
					routeCorrect = item.ExpectedRoute == "" || item.Route == item.ExpectedRoute
				}
				if len(item.Tools) > 0 {
					toolCount++
				}
				if toolNameCorrect {
					toolNameCount++
				}
				if argumentsCorrect {
					argsCount++
				}
				if routeCorrect {
					routeCount++
				}
				if item.Turn >= 6 && item.ExpectedRoute == RouteFastPathConfirm {
					corefN++
					if corefCorrect {
						corefCount++
					}
				}
				if item.Turn == 8 {
					taskN++
					if taskPassed {
						taskCount++
					}
				}
			}
			if a.Requests > 0 {
				a.AvgInput = float64(a.InputTokens) / float64(a.Requests)
				a.AvgLatency = float64(a.LatencyMS) / float64(a.Requests)
				a.AvgContext /= float64(a.Requests)
				a.AvgLLMNetwork /= float64(a.Requests)
				a.AvgJev /= float64(a.Requests)
				sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
				a.P50Latency = percentileInt64(latencies, 0.50)
				a.P95Latency = percentileInt64(latencies, 0.95)
			}
			a.ToolUseRate = percentage(toolCount, a.Requests)
			a.ToolNameAccuracy = percentage(toolNameCount, a.Requests)
			a.ArgumentAccuracy = percentage(argsCount, a.Requests)
			a.CorefAccuracy = percentage(corefCount, corefN)
			a.TaskCompletion = percentage(taskCount, taskN)
			a.RouteAccuracy = percentage(routeCount, a.Requests)
			aggs = append(aggs, a)
		}
	}

	path := strings.TrimSpace(os.Getenv("AGENT_REDUCED_LIVE_REPORT"))
	if path == "" {
		path = "../docs/reduced-live-ablation.json"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"provider": provider.Name(), "model": provider.Model(), "cases": len(cases), "turnsPerCase": 8, "groups": len(groups), "records": records, "aggregates": aggs, "complete": true}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	mdPath := strings.TrimSpace(os.Getenv("AGENT_REDUCED_LIVE_REPORT_MD"))
	if mdPath == "" {
		mdPath = "../docs/reduced-live-ablation.md"
	}
	if err := os.MkdirAll(filepath.Dir(mdPath), 0755); err != nil {
		t.Fatal(err)
	}
	var report strings.Builder
	report.WriteString("# 受控线上上下文与 Jev 消融\n\n")
	report.WriteString(fmt.Sprintf("Provider: `%s`, Model: `%s`; 数据集：%d 条八轮长对话；本报告仅统计真实 Provider usage。\n\n", provider.Name(), provider.Model(), len(cases)))
	report.WriteString("| 组别 | 口径 | 请求数 | 输入 Token | 输出 Token | 总 Token | 平均输入 | Turn6+峰值 | API错误率 | P50(ms) | Avg(ms) | P95(ms) | Context均值(ms) | LLM网络均值(ms) | Jev均值(ms) | Retry | 工具使用率 | 工具名准确率 | 参数准确率 | Coref | 任务完成率 | 路由准确率 |\n|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, item := range aggs {
		errorRate := 0.0
		if item.Requests > 0 {
			errorRate = 100 * float64(item.Errors) / float64(item.Requests)
		}
		report.WriteString(fmt.Sprintf("| %s | %s | %d | %d | %d | %d | %.1f | %d | %.2f%% | %d | %.1f | %d | %.1f | %.1f | %.1f | %d | %.2f%% | %.2f%% | %.2f%% | %.2f%% | %.2f%% | %.2f%% |\n", item.Group, item.Dataset, item.Requests, item.InputTokens, item.OutputTokens, item.TotalTokens, item.AvgInput, item.PeakLongInput, errorRate, item.P50Latency, item.AvgLatency, item.P95Latency, item.AvgContext, item.AvgLLMNetwork, item.AvgJev, item.TotalRetries, item.ToolUseRate, item.ToolNameAccuracy, item.ArgumentAccuracy, item.CorefAccuracy, item.TaskCompletion, item.RouteAccuracy))
	}
	report.WriteString("\n## Turn 1-8 趋势\n\n")
	report.WriteString("每个单元格为 `平均输入 Token / 平均总延迟(ms) / Context均值(ms) / LLM网络均值(ms) / Jev均值(ms)`。\n\n")
	report.WriteString("| 组别 | 口径 | T1 | T2 | T3 | T4 | T5 | T6 | T7 | T8 |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, group := range groups {
		for _, dataset := range []string{"Core-8"} {
			report.WriteString(fmt.Sprintf("| %s | %s", group.Name, dataset))
			for turn := 1; turn <= 8; turn++ {
				avgInput, avgTotal, avgContext, avgNetwork, avgJev, count := trendForTurn(records, group.Name, dataset, turn)
				if count == 0 {
					report.WriteString(" | -")
					continue
				}
				report.WriteString(fmt.Sprintf(" | %.0f / %.0f / %.0f / %.0f / %.0f", avgInput, avgTotal, avgContext, avgNetwork, avgJev))
			}
			report.WriteString(" |\n")
		}
	}
	report.WriteString("\n## 典型案例\n\n")
	report.WriteString("- D 组失败样例：LR01/LR02 的第 6 轮依赖早期订单状态；在历史脱水且没有 Jev 状态路由时可能无法生成正确的 `prepare_cancel_order`，第 8 轮无法保持待确认状态。\n")
	report.WriteString("- E 组修复样例：同一类对话在第 6 轮保留状态锚点并生成确认，第 8 轮由 `FAST_PATH_CONFIRM` 直通 `cancel_my_pending_order`，确认前不执行取消。\n")
	report.WriteString(fmt.Sprintf("\n延迟字段覆盖：%d/%d 条记录包含新阶段打点；旧 checkpoint 记录的 Context/LLM/Jev 延迟保持为未采集的 0，不做估算。\n", countLatencySamples(records), len(records)))
	if err := os.WriteFile(mdPath, []byte(report.String()), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(report.String())
}

func percentileInt64(values []int64, quantile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	if quantile < 0 {
		quantile = 0
	}
	if quantile > 1 {
		quantile = 1
	}
	index := int(float64(len(values)-1) * quantile)
	return values[index]
}

func percentage(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

func trendForTurn(records []EvalRecord, group, dataset string, turn int) (avgInput, avgTotal, avgContext, avgNetwork, avgJev float64, count int) {
	for _, record := range records {
		if record.Group != group || record.Dataset != dataset || record.Turn != turn {
			continue
		}
		count++
		avgInput += float64(record.InputTokens)
		avgTotal += float64(record.TotalLatencyMS)
		avgContext += float64(record.ContextLatencyMS)
		avgNetwork += float64(record.LLMNetworkLatencyMS)
		avgJev += float64(record.JevLatencyMS)
	}
	if count > 0 {
		divisor := float64(count)
		avgInput /= divisor
		avgTotal /= divisor
		avgContext /= divisor
		avgNetwork /= divisor
		avgJev /= divisor
	}
	return
}

func countLatencySamples(records []EvalRecord) int {
	count := 0
	for _, record := range records {
		if record.ContextLatencyMS != 0 || record.LLMNetworkLatencyMS != 0 || record.JevLatencyMS != 0 || record.RetryCount != 0 {
			count++
		}
	}
	return count
}

func lookupGoldTurn(cases []longContextCase, caseID string, turn int) (longContextTurn, bool) {
	for _, testCase := range cases {
		if testCase.ID != caseID || turn < 1 || turn > len(testCase.Turns) {
			continue
		}
		return testCase.Turns[turn-1], true
	}
	return longContextTurn{}, false
}

func scoreLiveTurn(record EvalRecord, turn longContextTurn) (toolName, arguments, coref, task bool) {
	toolName = matchLiveToolName(turn, record.Tools)
	arguments = matchLiveArguments(turn, record.Tools)
	coref = turn.ExpectedOrderID == 0 || matchLiveOrder(turn.ExpectedOrderID, record.Tools)
	if turn.GoldRoute != "" && record.Route != turn.GoldRoute {
		return toolName, arguments, coref, false
	}
	if turn.ExpectedTool == "confirm_cancel" {
		task = record.Turn == 8 && record.Status == "completed" && toolName && arguments && coref && !record.APIError && record.Error == ""
	}
	return toolName, arguments, coref, task
}

func matchLiveToolName(turn longContextTurn, tools []ToolTrace) bool {
	for _, tool := range tools {
		if turn.ExpectedTool == "" {
			return false
		}
		if turn.ExpectedTool == "confirm_cancel" && tool.Name == "cancel_my_pending_order" {
			return true
		}
		if tool.Name == turn.ExpectedTool {
			return true
		}
	}
	return false
}

func matchLiveOrder(orderID int64, tools []ToolTrace) bool {
	for _, tool := range tools {
		var args map[string]any
		if json.Unmarshal(tool.Arguments, &args) != nil {
			continue
		}
		if raw, ok := args["order_id"]; ok {
			switch value := raw.(type) {
			case float64:
				if int64(value) == orderID {
					return true
				}
			case int64:
				if value == orderID {
					return true
				}
			}
		}
	}
	return false
}

func matchLiveArguments(turn longContextTurn, tools []ToolTrace) bool {
	if !matchLiveToolName(turn, tools) {
		return false
	}
	if turn.ExpectedOrderID > 0 {
		return matchLiveOrder(turn.ExpectedOrderID, tools)
	}
	for _, tool := range tools {
		var args map[string]any
		if json.Unmarshal(tool.Arguments, &args) != nil {
			continue
		}
		switch tool.Name {
		case "list_my_orders":
			_, pageOK := args["page"]
			_, sizeOK := args["page_size"]
			if pageOK && sizeOK {
				return true
			}
		case "search_products":
			if keyword, ok := args["keyword"].(string); ok && keyword != "" {
				return true
			}
		case "search_knowledge":
			if query, ok := args["query"].(string); ok && strings.TrimSpace(query) != "" {
				return true
			}
		}
	}
	return false
}

func evalRecordKey(group, caseID string, turn int) string {
	return fmt.Sprintf("%s|%s|%d", group, caseID, turn)
}

func expectedRouteForTurn(turn longContextTurn) JevRouteAction {
	if turn.GoldRoute != "" {
		return turn.GoldRoute
	}
	switch turn.ExpectedTool {
	case "confirm_cancel":
		return RouteFastPathConfirm
	default:
		return RouteProceedMainLLM
	}
}
