package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const efficiencyBudget = 500000
const efficiencyPriorReserve = 1808566

var efficiencyArms = []strictGroup{
	{"C_compressed_off", StrategyOursFull, false},
	{"R_compressed_rules", StrategyOursFull, true},
	{"J_compressed_typesafe", StrategyOursFull, true},
}

func efficiencyCases() []strictCase { cases := strictCases(); return []strictCase{cases[0], cases[2]} }

type efficiencyAttempt struct {
	Key, Arm, Kind, RequestHash, Error string
	Turn, ReservedTokens, HTTPStatus   int
	Usage                              Usage
	LatencyMS                          int64
}

type efficiencyResult struct {
	strictResult
	Decision JevDecision
}

type efficiencyCheckpoint struct {
	Manifest     map[string]any
	ManifestHash string
	Attempts     []efficiencyAttempt
	Results      []efficiencyResult
	Snapshots    map[string]strictSnapshot
	Complete     bool
}

type efficiencyLedger struct {
	cp                *efficiencyCheckpoint
	path, arm, caseID string
	turn              int
}

func (l *efficiencyLedger) save() error {
	data, err := json.MarshalIndent(l.cp, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(l.path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(l.path+".tmp", l.path)
}

func (l *efficiencyLedger) charged() int {
	total := 0
	for _, a := range l.cp.Attempts {
		if a.Usage.TotalTokens > 0 {
			total += a.Usage.TotalTokens
		} else {
			total += a.ReservedTokens
		}
	}
	return total
}

func (l *efficiencyLedger) begin(kind string, body []byte) (int, error) {
	reserve := len(body) + 8192
	if l.charged()+reserve > efficiencyBudget || efficiencyPriorReserve+l.charged()+reserve > 10000000 {
		return 0, errors.New("评测预算不足，停止新请求")
	}
	a := efficiencyAttempt{Key: fmt.Sprintf("%s/%s/%d", l.caseID, l.arm, l.turn), Arm: l.arm, Turn: l.turn, Kind: kind, ReservedTokens: reserve, RequestHash: strictHash(string(body)), Error: "in_flight_usage_unknown"}
	l.cp.Attempts = append(l.cp.Attempts, a)
	return len(l.cp.Attempts) - 1, l.save()
}

type efficiencyProvider struct {
	base       Provider
	ledger     *efficiencyLedger
	compressed bool
}

func (p *efficiencyProvider) Name() string  { return p.base.Name() }
func (p *efficiencyProvider) Model() string { return p.base.Model() }
func (p *efficiencyProvider) CountTokens(r CompletionRequest) (int, error) {
	return p.base.(TokenCounter).CountTokens(r)
}
func (p *efficiencyProvider) Complete(ctx context.Context, r CompletionRequest) (Completion, error) {
	if !strictToolPairs(r.Messages) {
		return Completion{}, errors.New("工具调用协议不成对，拒绝发送")
	}
	n, err := p.CountTokens(r)
	if err != nil {
		return Completion{}, err
	}
	if p.compressed && n > 7500 {
		return Completion{}, errors.New("本地严格 Token 预算超限")
	}
	temperature := 0.0
	r.Temperature, r.MaxOutputTokens = &temperature, 512
	body, _ := json.Marshal(tokenEnvelope(r))
	index, err := p.ledger.begin("main", body)
	if err != nil {
		return Completion{}, err
	}
	started := time.Now()
	result, callErr := p.base.Complete(ctx, r)
	a := &p.ledger.cp.Attempts[index]
	a.Usage, a.LatencyMS, a.Error = result.Usage, time.Since(started).Milliseconds(), ""
	if callErr != nil {
		a.Error = callErr.Error()
	}
	if callErr == nil && result.Usage.TotalTokens <= 0 {
		a.Error = "Provider 未返回用量"
		callErr = errors.New(a.Error)
	}
	if err := p.ledger.save(); err != nil {
		return result, err
	}
	if p.compressed && result.Usage.InputTokens > 12000 {
		return result, errors.New("Provider 实际输入超出 12000 Token")
	}
	return result, callErr
}

type efficiencyTransport struct {
	base   http.RoundTripper
	ledger *efficiencyLedger
}

func (p efficiencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	index, err := p.ledger.begin("jev", body)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	resp, callErr := p.base.RoundTrip(req)
	// 不跨 append 保存切片元素指针；每次请求均串行。
	a := &p.ledger.cp.Attempts[index]
	a.LatencyMS, a.Error = time.Since(started).Milliseconds(), ""
	if callErr != nil {
		a.Error = "Jev network failure"
		if err := p.ledger.save(); err != nil {
			return nil, err
		}
		return nil, callErr
	}
	a.HTTPStatus = resp.StatusCode
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(responseBody))
	var result typeSafeResponse
	if err := json.Unmarshal(responseBody, &result); err == nil && result.Usage != nil {
		a.Usage = Usage{InputTokens: result.Usage.Input, OutputTokens: result.Usage.Output, TotalTokens: result.Usage.Input + result.Usage.Output}
	}
	a.LatencyMS = time.Since(started).Milliseconds()
	if readErr != nil || len(responseBody) > 1<<20 {
		a.Error = "Jev response read failure"
	} else if resp.StatusCode != 200 {
		a.Error = fmt.Sprintf("Jev HTTP %d", resp.StatusCode)
	}
	if err := p.ledger.save(); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

func efficiencyManifest(base Provider) map[string]any {
	sources := map[string]string{}
	for _, name := range []string{"service.go", "typesafe_client.go", "jev_router.go", "session_state.go", "compatible_provider.go", "typesafe_efficiency_test.go", "strict_live_ablation_test.go"} {
		data, _ := os.ReadFile(name)
		sources[name] = strictHash(string(data))
	}
	return map[string]any{"protocol": "typesafe-efficiency-pilot-v1", "main_model": base.Model(), "jev_model": envOr("JEV_MODEL", "jev-1.13.0"), "arms": efficiencyArms, "cases": efficiencyCases(), "source_hashes": sources,
		"concurrency": 1, "order": "cyclic_by_turn", "mode": "free_running_e2e", "tools": "local_mock_same_fixture", "temperature": 0, "max_output_tokens": 512,
		"context_budget": 7500, "encoding": "cl100k_base", "jev_timeout_ms": envOr("JEV_TIMEOUT_MS", "2000"), "jev_min_confidence": envOr("JEV_MIN_CONFIDENCE", "0.7"), "max_retries": 0, "budget": efficiencyBudget,
		"main_timeout_ms": envOr("AI_REQUEST_TIMEOUT_MS", "35000"), "transport": os.Getenv("GODEBUG"),
		"main_endpoint_hash": strictHash(os.Getenv("AI_BASE_URL")), "jev_endpoint": envOr("TYPESAFE_BASE_URL", "https://api.typesafe.ai/v1"), "jev_proxy_hash": strictHash(os.Getenv("TYPESAFE_PROXY_URL"))}
}

func TestTypeSafeEfficiencyLive(t *testing.T) {
	if os.Getenv("TYPESAFE_EFFICIENCY_LIVE") != "1" {
		t.Skip("需要显式启用线上效率评测")
	}
	base := NewProviderFromEnv()
	if base.Name() == "local" || os.Getenv("AI_PROVIDER_MAX_RETRIES") != "0" {
		t.Fatal("要求真实 Provider 且禁止自动重试")
	}
	if err := CheckTokenizerHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 预热常驻计数器，避免首次加载字典干扰某一组的延迟。
	if _, err := base.(TokenCounter).CountTokens(CompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "docs", "typesafe-efficiency-v1", "checkpoint.json")
	manifest := efficiencyManifest(base)
	cp := efficiencyCheckpoint{Manifest: manifest, ManifestHash: strictHash(manifest), Snapshots: map[string]strictSnapshot{}}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cp); err != nil {
			t.Fatal(err)
		}
		if cp.ManifestHash != strictHash(manifest) {
			t.Fatal("配置或源代码变化，拒绝混合续跑")
		}
	}
	ledger := &efficiencyLedger{cp: &cp, path: path}
	done := map[string]bool{}
	for _, r := range cp.Results {
		if r.APIError != "" {
			t.Fatalf("存在失败单元 %s，保留证据并停止，禁止自动重跑", r.Key)
		}
		done[r.Key] = true
	}
	for _, a := range cp.Attempts {
		if !done[a.Key] {
			t.Fatalf("请求 %s 尚未提交结果，拒绝重复付费", a.Key)
		}
	}
	if cp.Complete {
		if err := efficiencyReport(&cp, filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := ledger.save(); err != nil {
		t.Fatal(err)
	}
	type runtime struct {
		s        *Service
		orders   *strictOrders
		products *strictProducts
		id       string
	}
	for ci, c := range efficiencyCases() {
		runtimes := map[string]*runtime{}
		for _, arm := range efficiencyArms {
			provider := &efficiencyProvider{base: base, ledger: ledger, compressed: arm.Strategy == StrategyOursFull}
			s, orders, products := strictService(provider, c, arm)
			if arm.ID == "J_compressed_typesafe" {
				s.jev = NewJevDecisionEngine()
				if s.jev.provider == nil || !s.jev.enabled || s.jev.shadow {
					t.Fatal("官方 Jev 必须真实启用且不能为影子模式")
				}
				transport := s.jev.provider.http.Transport
				if transport == nil {
					transport = http.DefaultTransport
				}
				s.jev.provider.http.Transport = efficiencyTransport{base: transport, ledger: ledger}
			}
			r := &runtime{s: s, orders: orders, products: products}
			if snapshot, ok := cp.Snapshots[c.ID+"/"+arm.ID]; ok {
				strictRestore(s, orders, products, snapshot)
				r.id = snapshot.ConversationID
			}
			runtimes[arm.ID] = r
		}
		for i, turn := range c.Turns {
			for offset := range efficiencyArms {
				arm := efficiencyArms[(ci+i+offset)%len(efficiencyArms)]
				key := fmt.Sprintf("%s/%s/%d", c.ID, arm.ID, i+1)
				if done[key] {
					t.Log("SKIP", key)
					continue
				}
				r := runtimes[arm.ID]
				ledger.arm, ledger.turn = arm.ID, i+1
				ledger.caseID = c.ID
				before := len(cp.Attempts)
				started := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
				answer, err := r.s.Chat(ctx, 42, ChatRequest{ConversationID: r.id, Message: turn.Query})
				cancel()
				result := efficiencyResult{strictResult: strictResult{Key: key, Stage: "free_running_e2e", Group: arm.ID, Case: c.ID, Turn: i + 1, TotalLatencyMS: time.Since(started).Milliseconds(), Tools: answer.ToolCalls, Message: answer.Message, Status: answer.TaskStatus}}
				if answer.ConversationID != "" {
					r.id = answer.ConversationID
				}
				j, route := r.s.LastJevDiagnostics()
				cl, nl, retry := r.s.LastRequestDiagnostics()
				result.JevLatencyMS, result.Route, result.ContextLatencyMS, result.LLMNetworkLatencyMS, result.RetryCount = j.Milliseconds(), route, cl.Milliseconds(), nl.Milliseconds(), retry
				result.OtherLatencyMS = result.TotalLatencyMS - result.JevLatencyMS - result.ContextLatencyMS - result.LLMNetworkLatencyMS
				result.Decision = r.s.LastJevDecision()
				for _, a := range cp.Attempts[before:] {
					if a.Kind == "main" {
						result.ProviderCalls++
					}
					if a.Error != "" {
						result.APIError = a.Error
					}
				}
				if err != nil {
					result.APIError = err.Error()
				}
				if answer.Handoff != nil && answer.Handoff.Reason == "provider_unavailable" {
					result.APIError = "provider_unavailable"
				}
				result.ToolNameCorrect, result.ArgumentsCorrect, result.CorefCorrect = strictScore(c, i, result.Tools)
				result.EarlyCancel = i < 7 && len(r.orders.Cancelled) > 0
				result.TaskPassed = i == 7 && len(r.orders.Cancelled) == 1 && r.orders.Cancelled[0] == c.Target && answer.TaskStatus == "completed"
				if result.TaskPassed {
					for _, previous := range cp.Results {
						if previous.Case == c.ID && previous.Group == arm.ID && previous.EarlyCancel {
							result.TaskPassed = false
						}
					}
				}
				cp.Results = append(cp.Results, result)
				cp.Snapshots[c.ID+"/"+arm.ID] = strictSnapshotFor(r.s, r.id, r.orders, r.products)
				done[key] = true
				if err := ledger.save(); err != nil {
					t.Fatal(err)
				}
				if err := efficiencyReport(&cp, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s latency=%dms main_calls=%d jev=%s task=%t charged=%d/%d", key, result.TotalLatencyMS, result.ProviderCalls, result.Decision.Source, result.TaskPassed, ledger.charged(), efficiencyBudget)
				if result.APIError != "" {
					t.Fatalf("停止新请求并保留断点: %s", result.APIError)
				}
			}
		}
	}
	cp.Complete = len(cp.Results) == len(efficiencyCases())*len(efficiencyArms)*8
	if err := ledger.save(); err != nil {
		t.Fatal(err)
	}
	if err := efficiencyReport(&cp, filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
}

type efficiencyAggregate struct {
	Arm                                                                                                            string
	Turns, MainCalls, JevCalls, MainInput, MainOutput, JevInput, JevOutput, CachedInput, UnknownReserve, APIErrors int
	TasksPassed, TasksEvaluated, ToolCalls, ToolSuccesses                                                          int
	StrictCorrect, CorefCorrect, CorefTotal, Bypassed, ExternalAdopted, Fallbacks                                  int
	AvgMS, P50MS, P95MS, ContextAvgMS, MainNetworkAvgMS, JevAvgMS                                                  float64
}

func efficiencyReport(cp *efficiencyCheckpoint, dir string) error {
	var report strings.Builder
	fmt.Fprintln(&report, "# 官方 Jev 与上下文压缩：三组同源效率验证")
	fmt.Fprintf(&report, "\n完成状态：%t。三组使用相同两条 8 轮场景（普通跨轮指代、早期禁止操作约束）、同一主模型与相同 Mock 业务工具，串行按轮次循环交错。该实验是实际 Provider 自由运行 E2E 小样本，各组生成的历史可能不同；不是 Teacher Forcing，也不是生产用户流量。\n\n", cp.Complete)
	fmt.Fprintln(&report, "C=三级压缩无路由；R=三级压缩+确定性规则；J=三级压缩+规则+官方 Jev。三组压缩配置相同，R→J 衡量外部模型的增量。所有用量来自 Provider 返回；Jev 自身消耗计入合计输入及输出。")
	fmt.Fprintln(&report, "\n|组|轮数|主模型请求|Jev 请求/采用|主输入/输出|Jev 输入/输出|合计输入|合计输出|平均每轮输入|主模型缓存输入|均值ms|P50ms|P95ms|Context均值ms|主网络均值ms|Jev均值ms|严格轨迹正确轮|指代正确轮|最终任务完成|工具调用成功|API错误|\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|---|---:|")
	var rows []efficiencyAggregate
	charged := 0
	for _, arm := range efficiencyArms {
		a := efficiencyAggregate{Arm: arm.ID}
		var latencies []int64
		for _, r := range cp.Results {
			if r.Group != arm.ID {
				continue
			}
			a.Turns++
			latencies = append(latencies, r.TotalLatencyMS)
			a.AvgMS += float64(r.TotalLatencyMS)
			a.ContextAvgMS += float64(r.ContextLatencyMS)
			a.MainNetworkAvgMS += float64(r.LLMNetworkLatencyMS)
			a.JevAvgMS += float64(r.JevLatencyMS)
			if r.ToolNameCorrect && r.ArgumentsCorrect {
				a.StrictCorrect++
			}
			if r.Turn == 6 || r.Turn == 8 {
				a.CorefTotal++
				if r.CorefCorrect {
					a.CorefCorrect++
				}
			}
			if r.ProviderCalls == 0 {
				a.Bypassed++
			}
			if r.Decision.Source == "typesafe" {
				a.ExternalAdopted++
			}
			if r.Decision.Source == "fallback" {
				a.Fallbacks++
			}
			if r.Turn == 8 {
				a.TasksEvaluated++
				if r.TaskPassed {
					a.TasksPassed++
				}
			}
			for _, tool := range r.Tools {
				a.ToolCalls++
				if tool.Success {
					a.ToolSuccesses++
				}
			}
		}
		for _, attempt := range cp.Attempts {
			if attempt.Arm != arm.ID {
				continue
			}
			if attempt.Error != "" {
				a.APIErrors++
			}
			if attempt.Usage.TotalTokens > 0 {
				charged += attempt.Usage.TotalTokens
			} else {
				a.UnknownReserve += attempt.ReservedTokens
				charged += attempt.ReservedTokens
			}
			if attempt.Kind == "main" {
				a.MainCalls++
				a.MainInput += attempt.Usage.InputTokens
				a.MainOutput += attempt.Usage.OutputTokens
				a.CachedInput += attempt.Usage.CachedTokens
			} else {
				a.JevCalls++
				a.JevInput += attempt.Usage.InputTokens
				a.JevOutput += attempt.Usage.OutputTokens
			}
		}
		if a.Turns > 0 {
			n := float64(a.Turns)
			a.AvgMS /= n
			a.ContextAvgMS /= n
			a.MainNetworkAvgMS /= n
			a.JevAvgMS /= n
		}
		a.P50MS, a.P95MS = strictPercentile(latencies, .5), strictPercentile(latencies, .95)
		avgInput := 0.0
		if a.Turns > 0 {
			avgInput = float64(a.MainInput+a.JevInput) / float64(a.Turns)
		}
		fmt.Fprintf(&report, "|%s|%d|%d|%d/%d|%d/%d|%d/%d|%d|%d|%.1f|%d|%.1f|%.1f|%.1f|%.2f|%.1f|%.1f|%d/%d|%d/%d|%d/%d|%d/%d|%d|\n", a.Arm, a.Turns, a.MainCalls, a.JevCalls, a.ExternalAdopted, a.MainInput, a.MainOutput, a.JevInput, a.JevOutput, a.MainInput+a.JevInput, a.MainOutput+a.JevOutput, avgInput, a.CachedInput, a.AvgMS, a.P50MS, a.P95MS, a.ContextAvgMS, a.MainNetworkAvgMS, a.JevAvgMS, a.StrictCorrect, a.Turns, a.CorefCorrect, a.CorefTotal, a.TasksPassed, a.TasksEvaluated, a.ToolSuccesses, a.ToolCalls, a.APIErrors)
		rows = append(rows, a)
	}
	fmt.Fprintln(&report, "\n## 逐轮结果\n\n|场景|组|轮|合计输入|合计输出|总耗时ms|Context ms|主网络ms|Jev ms|主请求数|路由来源|严格参数正确|\n|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---|")
	results := append([]efficiencyResult(nil), cp.Results...)
	sort.Slice(results, func(i, j int) bool {
		if results[i].Case != results[j].Case {
			return results[i].Case < results[j].Case
		}
		if results[i].Turn != results[j].Turn {
			return results[i].Turn < results[j].Turn
		}
		return results[i].Group < results[j].Group
	})
	for _, r := range results {
		input, output := 0, 0
		for _, a := range cp.Attempts {
			if a.Key == r.Key {
				input += a.Usage.InputTokens
				output += a.Usage.OutputTokens
			}
		}
		fmt.Fprintf(&report, "|%s|%s|%d|%d|%d|%d|%d|%d|%d|%d|%s|%t|\n", r.Case, r.Group, r.Turn, input, output, r.TotalLatencyMS, r.ContextLatencyMS, r.LLMNetworkLatencyMS, r.JevLatencyMS, r.ProviderCalls, r.Decision.Source, r.ArgumentsCorrect)
	}
	if len(rows) == 3 && cp.Complete {
		fmt.Fprintln(&report, "\n## 同源增量比较\n\n|比较|合计输入下降|合计输入输出下降|平均 E2E 耗时下降|\n|---|---:|---:|---:|")
		for _, pair := range [][2]int{{0, 1}, {1, 2}, {0, 2}} {
			a, b := rows[pair[0]], rows[pair[1]]
			percent := func(x, y float64) float64 {
				if x == 0 {
					return 0
				}
				return (x - y) / x * 100
			}
			fmt.Fprintf(&report, "|%s → %s|%.2f%%|%.2f%%|%.2f%%|\n", a.Arm, b.Arm, percent(float64(a.MainInput+a.JevInput), float64(b.MainInput+b.JevInput)), percent(float64(a.MainInput+a.JevInput+a.MainOutput+a.JevOutput), float64(b.MainInput+b.JevInput+b.MainOutput+b.JevOutput)), percent(a.AvgMS, b.AvgMS))
		}
	}
	fmt.Fprintf(&report, "\n预算占用（已返回用量+未知请求保守预留）：%d/%d Token；含此前预留累计 %d/10000000。负降幅表示增加。\n", charged, efficiencyBudget, efficiencyPriorReserve+charged)
	fmt.Fprintln(&report, "\n指标定义：任务完成率=最终只取消正确订单且未提前取消的成功场景数/场景数；工具调用成功率=后端成功返回的工具执行数/全部工具执行数，不等于工具选择或参数提取准确率，严格轨迹另列。输入按所有主模型调用与官方 Jev 的实际输入合计，平均输入按用户轮次归一化，不因二次确认绕过而改变分母。")
	fmt.Fprintln(&report, "\n结论边界：每组仅两条对话、16 个用户轮次，P95 只作描述，不能用于线上 SLA 或宣称统计显著；准确率请保留分子/分母。工具与 RAG 是固定本地 fixture，不代表生产数据库与检索延迟。缓存返回量可观察但未做冷热缓存随机试验，不能将延迟差归因于缓存。Jev 美元费用可按官方输入单价估算，主模型网关价格未知，因此不虚构总美元降幅。失败与回退不计作外部决策成功。")
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(report.String()), 0600); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(map[string]any{"complete": cp.Complete, "manifest_hash": cp.ManifestHash, "aggregates": rows, "charged_tokens": charged, "prior_reserved_tokens": efficiencyPriorReserve, "run_budget": efficiencyBudget}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.json"), encoded, 0600)
}

func TestTypeSafeEfficiencyOffline(t *testing.T) {
	cp := efficiencyCheckpoint{Manifest: map[string]any{}, Complete: false}
	dir := t.TempDir()
	if err := efficiencyReport(&cp, dir); err != nil {
		t.Fatal(err)
	}
	l := efficiencyLedger{cp: &cp, path: filepath.Join(dir, "cp.json"), arm: "test", turn: 1}
	index, err := l.begin("main", []byte("{}"))
	if err != nil || index != 0 || l.charged() != 8194 {
		t.Fatal("请求前预算预留失败")
	}
	cp.Attempts[0].Usage = Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}
	if l.charged() != 12 {
		t.Fatal("真实用量未替代预留")
	}
}
