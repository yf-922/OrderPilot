package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type savedTool struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Success   bool            `json:"success"`
}

type savedRecord struct {
	Group          string      `json:"group"`
	Case           string      `json:"case"`
	Turn           int         `json:"turn"`
	InputTokens    int         `json:"inputTokens"`
	OutputTokens   int         `json:"outputTokens"`
	TotalTokens    int         `json:"totalTokens"`
	TotalLatencyMS int64       `json:"totalLatencyMs"`
	JevLatencyMS   int64       `json:"jevLatencyMs"`
	Status         string      `json:"status"`
	Route          string      `json:"route"`
	Tools          []savedTool `json:"tools"`
	APIError       bool        `json:"apiError"`
	Error          string      `json:"error"`
	Dataset        string      `json:"dataset"`
}

type savedReport struct {
	Provider   string         `json:"provider"`
	Model      string         `json:"model"`
	Records    []savedRecord  `json:"records"`
	Aggregates []savedAggBase `json:"aggregates"`
}

type savedAggBase struct {
	Group        string  `json:"group"`
	Dataset      string  `json:"dataset"`
	Requests     int     `json:"requests"`
	Errors       int     `json:"errors"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TotalTokens  int     `json:"totalTokens"`
	AvgInput     float64 `json:"avgInputTokens"`
	PeakInput    int     `json:"peakInputTokens"`
	AvgLatencyMS float64 `json:"avgLatencyMs"`
}

type scoredAgg struct {
	Group              string  `json:"group"`
	Dataset            string  `json:"dataset"`
	Requests           int     `json:"requests"`
	Errors             int     `json:"errors"`
	InputTokens        int     `json:"inputTokens"`
	OutputTokens       int     `json:"outputTokens"`
	TotalTokens        int     `json:"totalTokens"`
	AvgInput           float64 `json:"avgInputTokens"`
	PeakInput          int     `json:"peakInputTokens"`
	AvgLatencyMS       float64 `json:"avgLatencyMs"`
	P95LatencyMS       int64   `json:"p95LatencyMs"`
	TokenReduction     float64 `json:"tokenReductionVsA"`
	ToolUseRate        float64 `json:"toolUseRate"`
	ToolNameAccuracy   float64 `json:"toolNameAccuracy"`
	ArgumentAccuracy   float64 `json:"argumentAccuracy"`
	CorefAccuracy      float64 `json:"corefAccuracy"`
	TaskCompletionRate float64 `json:"taskCompletionRate"`
	RouteAccuracy      float64 `json:"routeAccuracy"`
	APIErrorRate       float64 `json:"apiErrorRate"`
	JevAvgLatencyMS    float64 `json:"jevAvgLatencyMs"`
}

func TestRebuildReducedLiveReportOffline(t *testing.T) {
	if os.Getenv("AGENT_REBUILD_REPORT") != "1" {
		t.Skip("set AGENT_REBUILD_REPORT=1")
	}
	sourcePath := envOr("AGENT_REBUILD_SOURCE", "../docs/reduced-live-ablation-calibration-2case.json")
	b, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var src savedReport
	if err := json.Unmarshal(b, &src); err != nil {
		t.Fatal(err)
	}
	goldBytes, err := os.ReadFile("testdata/long_context_challenges.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []longContextCase
	if err := json.Unmarshal(goldBytes, &cases); err != nil {
		t.Fatal(err)
	}
	gold := map[string]longContextTurn{}
	for _, c := range cases {
		for i, turn := range c.Turns {
			gold[fmt.Sprintf("%s|%d", c.ID, i+1)] = turn
		}
	}

	type scored struct {
		savedRecord
		ToolName, Args, Coref, Task, Route bool
	}
	records := make([]scored, 0, len(src.Records))
	for _, r := range src.Records {
		turn := gold[fmt.Sprintf("%s|%d", r.Case, r.Turn)]
		name := matchToolName(turn, r)
		args := matchArgs(turn, r)
		coref := turn.ExpectedOrderID == 0 || matchOrder(turn.ExpectedOrderID, r)
		route := r.Route == string(turn.GoldRoute)
		task := !r.APIError && r.Error == "" && route && name && args && coref && allToolsOK(r) && ((turn.ExpectedTool == "confirm_cancel" && r.Status == "completed") || (turn.ExpectedTool != "confirm_cancel" && (r.Status == "answered" || r.Status == "awaiting_confirmation")))
		records = append(records, scored{savedRecord: r, ToolName: name, Args: args, Coref: coref, Task: task, Route: route})
	}

	aggs := make([]scoredAgg, 0)
	for _, base := range src.Aggregates {
		items := make([]scored, 0)
		for _, r := range records {
			if r.Group == base.Group && r.Dataset == base.Dataset {
				items = append(items, r)
			}
		}
		if len(items) == 0 {
			continue
		}
		a := scoredAgg{Group: base.Group, Dataset: base.Dataset, Requests: base.Requests, Errors: base.Errors, InputTokens: base.InputTokens, OutputTokens: base.OutputTokens, TotalTokens: base.TotalTokens, AvgInput: base.AvgInput, PeakInput: base.PeakInput, AvgLatencyMS: base.AvgLatencyMS, APIErrorRate: pct(base.Errors, base.Requests)}
		lats := []int64{}
		tools, names, args, coref, corefN, tasks, finalN, routes := 0, 0, 0, 0, 0, 0, 0, 0
		jev := int64(0)
		for _, r := range items {
			lats = append(lats, r.TotalLatencyMS)
			jev += r.JevLatencyMS
			if len(r.Tools) > 0 {
				tools++
			}
			if r.ToolName {
				names++
			}
			if r.Args {
				args++
			}
			if r.Route {
				routes++
			}
			if r.Turn == 8 {
				finalN++
				if r.Task {
					tasks++
				}
			}
			if gold[fmt.Sprintf("%s|%d", r.Case, r.Turn)].ExpectedOrderID > 0 {
				corefN++
				if r.Coref {
					coref++
				}
			}
		}
		sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
		if len(lats) > 0 {
			a.P95LatencyMS = lats[int(float64(len(lats)-1)*.95)]
		}
		a.JevAvgLatencyMS = float64(jev) / float64(len(items))
		a.ToolUseRate = pct(tools, len(items))
		a.ToolNameAccuracy = pct(names, len(items))
		a.ArgumentAccuracy = pct(args, len(items))
		a.CorefAccuracy = pct(coref, corefN)
		a.TaskCompletionRate = pct(tasks, finalN)
		a.RouteAccuracy = pct(routes, len(items))
		aggs = append(aggs, a)
	}
	baseAvg := 0.0
	for _, a := range aggs {
		if strings.HasPrefix(a.Group, "A ") {
			baseAvg = a.AvgInput
			break
		}
	}
	for i := range aggs {
		if baseAvg > 0 {
			aggs[i].TokenReduction = 100 * (baseAvg - aggs[i].AvgInput) / baseAvg
		}
	}

	// Keep the original provider records unchanged. The scored booleans live in
	// the aggregate table; embedding them beside `route` would create duplicate
	// case-insensitive JSON keys in PowerShell consumers.
	out := map[string]any{"provider": src.Provider, "model": src.Model, "records": src.Records, "aggregates": aggs, "onlineRequests": 0, "evaluationMode": "offline_scored_saved_provider_records"}
	jsonPath := envOr("AGENT_REBUILD_REPORT_JSON", "../docs/reduced-live-ablation-calibration-2case-scored.json")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0755); err != nil {
		t.Fatal(err)
	}
	enc, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile(jsonPath, append(enc, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	var md strings.Builder
	md.WriteString("# 线上上下文与 Jev 消融评分报告（离线重建）\n\n")
	md.WriteString(fmt.Sprintf("Provider: `%s`，Model: `%s`；读取 %d 条真实 Provider 记录；新增线上请求：`0`。样本口径：两条校准长对话（Calibration-2），不是 Full-25。\n\n", src.Provider, src.Model, len(src.Records)))
	md.WriteString("最终任务完成要求：第 8 轮确认路由正确、订单 ID 正确、状态为 completed；工具名/参数按 Gold 轨迹评分。\n\n")
	md.WriteString("| 组别 | 请求 | 总 Token | 平均输入 | 峰值 | Token 降幅 | 工具使用率 | 工具名 | 参数 | Coref | 任务完成 | 路由 | API 错误 | P95(ms) | Jev均值(ms) |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, a := range aggs {
		md.WriteString(fmt.Sprintf("| %s | %d | %d | %.1f | %d | %.2f%% | %.2f%% | %.2f%% | %.2f%% | %.2f%% | %.2f%% | %.2f%% | %.2f%% | %d | %.2f |\n", a.Group, a.Requests, a.TotalTokens, a.AvgInput, a.PeakInput, a.TokenReduction, a.ToolUseRate, a.ToolNameAccuracy, a.ArgumentAccuracy, a.CorefAccuracy, a.TaskCompletionRate, a.RouteAccuracy, a.APIErrorRate, a.P95LatencyMS, a.JevAvgLatencyMS))
	}
	md.WriteString("\n指标边界：原始记录没有 cached_tokens、Context Processing Latency 和 LLM Network Latency，本报告不估算这些字段；这是零成本离线重算，不代表新增线上样本。\n")
	mdPath := envOr("AGENT_REBUILD_REPORT_MD", "../docs/reduced-live-ablation-calibration-2case-scored.md")
	if err := os.MkdirAll(filepath.Dir(mdPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mdPath, []byte(md.String()), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(md.String())
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}
func matchToolName(t longContextTurn, r savedRecord) bool {
	for _, x := range r.Tools {
		if t.ExpectedTool == "confirm_cancel" && x.Name == "cancel_my_pending_order" {
			return true
		}
		if x.Name == t.ExpectedTool {
			return true
		}
	}
	return false
}
func matchOrder(id int64, r savedRecord) bool {
	for _, x := range r.Tools {
		var a map[string]any
		if json.Unmarshal(x.Arguments, &a) != nil {
			continue
		}
		if n, ok := a["order_id"].(float64); ok && int64(n) == id {
			return true
		}
	}
	return false
}
func matchArgs(t longContextTurn, r savedRecord) bool {
	if !matchToolName(t, r) {
		return false
	}
	if t.ExpectedOrderID > 0 {
		return matchOrder(t.ExpectedOrderID, r)
	}
	for _, x := range r.Tools {
		var a map[string]any
		if json.Unmarshal(x.Arguments, &a) != nil {
			continue
		}
		switch x.Name {
		case "list_my_orders":
			if _, ok := a["page"]; ok {
				if _, ok = a["page_size"]; ok {
					return true
				}
			}
		case "search_products":
			k, _ := a["keyword"].(string)
			if k != "" && strings.Contains(t.User, k) {
				return true
			}
		case "search_knowledge":
			q, _ := a["query"].(string)
			for _, k := range []string{"售后", "退换货", "配送", "超时", "退款", "取消", "库存"} {
				if strings.Contains(t.User, k) && strings.Contains(q, k) {
					return true
				}
			}
		}
	}
	return false
}
func allToolsOK(r savedRecord) bool {
	if len(r.Tools) == 0 {
		return false
	}
	for _, x := range r.Tools {
		if !x.Success {
			return false
		}
	}
	return true
}
