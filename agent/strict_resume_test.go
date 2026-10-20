package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Continuation changes only the ledger. The frozen v3 fixture, provider,
// compressor and scoring code must still pass strictRun's manifest check.
func TestStrictCore4ResumeRemaining(t *testing.T) {
	if os.Getenv("AGENT_STRICT_RESUME") != "1" {
		t.Skip("explicit paid continuation only")
	}
	source := "../docs/strict-core4-v3/checkpoint.json"
	target := "../docs/strict-core4-v3-resumed/checkpoint.json"
	if _, err := os.Stat(target); os.IsNotExist(err) {
		b, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		var cp strictCheckpoint
		if err = json.Unmarshal(b, &cp); err != nil {
			t.Fatal(err)
		}
		if cp.Protocol != "strict-2x2-v3" || len(cp.Results) != 159 {
			t.Fatal("unexpected predecessor; inspect before continuation")
		}
		if err = strictSave(target, &cp); err != nil {
			t.Fatal(err)
		}
		audit := map[string]any{"predecessor": source, "predecessorHash": strictHash(string(b)), "resumeSourceHash": strictHashFile("strict_resume_test.go"), "createdUTC": time.Now().UTC(), "policy": "retain all attempts; no retry of failed calls; unexecuted suffix after infrastructure failure excluded from latency/efficiency; matched complete cases reported separately; original source manifest unchanged"}
		ab, _ := json.MarshalIndent(audit, "", "  ")
		if err = os.WriteFile(filepath.Join(filepath.Dir(target), "continuation-audit.json"), ab, 0600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var cp strictCheckpoint
	if err = json.Unmarshal(b, &cp); err != nil {
		t.Fatal(err)
	}
	if strictTerminalize(&cp) > 0 {
		if err = strictSave(target, &cp); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AGENT_STRICT_CHECKPOINT", target)
	t.Run("FrozenV3Continuation", func(t *testing.T) { strictRun(t, true) })
	if err := strictResumeReport(target); err != nil {
		t.Fatal(err)
	}
}

func strictHashFile(path string) string { b, _ := os.ReadFile(path); return strictHash(string(b)) }

func strictTerminalize(cp *strictCheckpoint) int {
	done := map[string]bool{}
	failed := map[string]strictResult{}
	for _, r := range cp.Results {
		done[r.Key] = true
		if r.Stage == "free_running_e2e" && r.APIError != "" && r.Status != "not_executed" {
			failed[r.Stage+"/"+r.Case+"/"+r.Group] = r
		}
	}
	added := 0
	for scope, r := range failed {
		for turn := r.Turn + 1; turn <= 8; turn++ {
			key := fmt.Sprintf("%s/%d", scope, turn)
			if done[key] {
				continue
			}
			cp.Results = append(cp.Results, strictResult{Key: key, Stage: r.Stage, Group: r.Group, Case: r.Case, Turn: turn, Status: "not_executed", Message: "前轮Provider失败后终止本条连续轨迹，保留失败；未请求模型、未延长确认令牌有效期。", APIError: "not_executed_after_prior_provider_failure"})
			added++
			done[key] = true
		}
	}
	return added
}

func TestStrictResumeTerminalizationOffline(t *testing.T) {
	cp := strictCheckpoint{Results: []strictResult{{Key: "free_running_e2e/S1/E/7", Stage: "free_running_e2e", Case: "S1", Group: "E", Turn: 7, APIError: "provider_unavailable"}}}
	if strictTerminalize(&cp) != 1 || strictTerminalize(&cp) != 0 || len(cp.Results) != 2 || cp.Results[1].ProviderCalls != 0 || cp.Results[1].TaskPassed {
		t.Fatal("terminalization must preserve failure without paid retry")
	}
}

func TestStrictResumeReportOffline(t *testing.T) {
	if os.Getenv("AGENT_STRICT_REBUILD_RESUME") != "1" {
		t.Skip("explicit local report only")
	}
	if err := strictResumeReport("../docs/strict-core4-v3-resumed/checkpoint.json"); err != nil {
		t.Fatal(err)
	}
}

func TestStrictResumeEvidenceOffline(t *testing.T) {
	if os.Getenv("AGENT_STRICT_REBUILD_RESUME") != "1" {
		t.Skip("local evidence verification only")
	}
	load := func(path string) strictCheckpoint {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var cp strictCheckpoint
		if err = json.Unmarshal(b, &cp); err != nil {
			t.Fatal(err)
		}
		return cp
	}
	old := load("../docs/strict-core4-v3/checkpoint.json")
	cp := load("../docs/strict-core4-v3-resumed/checkpoint.json")
	if !cp.Complete || len(cp.Results) != 256 || strictCharged(&cp) > 4000000 {
		t.Fatal("incomplete or over budget")
	}
	if strictHash(cp.Manifest) != strictHash(old.Manifest) || cp.ManifestHash != old.ManifestHash {
		t.Fatal("manifest changed")
	}
	if strictHash(cp.Attempts[:len(old.Attempts)]) != strictHash(old.Attempts) || strictHash(cp.Results[:len(old.Results)]) != strictHash(old.Results) {
		t.Fatal("previous evidence mutated")
	}
	keys := map[string]bool{}
	skipped := 0
	for _, r := range cp.Results {
		if keys[r.Key] {
			t.Fatal("duplicate unit")
		}
		keys[r.Key] = true
		if r.Status == "not_executed" {
			skipped++
		}
		if r.OtherLatencyMS < 0 || r.TotalLatencyMS != r.ContextLatencyMS+r.LLMNetworkLatencyMS+r.JevLatencyMS+r.OtherLatencyMS {
			t.Fatal("latency does not close")
		}
	}
	if skipped != 1 {
		t.Fatal("unexpected skipped units")
	}
	for _, a := range cp.Attempts[len(old.Attempts):] {
		if a.Error != "" || a.Usage.TotalTokens <= 0 {
			t.Fatal("new API failure or missing usage")
		}
		if (a.Group == "D" || a.Group == "E") && a.Usage.InputTokens > 12000 {
			t.Fatal("provider prompt cap exceeded")
		}
		if !strictToolPairs(a.Request.Messages) {
			t.Fatal("unpaired tool request")
		}
	}
	for _, c := range strictCases() {
		for _, g := range strictGroups {
			scope := "free_running_e2e/" + c.ID + "/" + g.ID
			snap := cp.Snapshots[scope]
			if c.ID == "S1" && g.ID == "E" {
				if len(snap.Cancelled) != 0 {
					t.Fatal("failed conversation unexpectedly cancelled")
				}
				continue
			}
			if len(snap.Cancelled) != 1 || snap.Cancelled[0] != c.Target {
				t.Fatalf("wrong final business state: %s", scope)
			}
			if c.Protected > 0 && containsID(snap.Cancelled, c.Protected) {
				t.Fatal("protected order cancelled")
			}
		}
	}
	for _, g := range strictGroups {
		found := false
		for _, r := range cp.Results {
			if r.Stage == "free_running_e2e" && r.Case == "S4" && r.Group == g.ID && r.Turn == 4 {
				found = true
				if !r.FaultRecovered || len(r.Tools) != 1 || r.Tools[0].Attempts != 2 {
					t.Fatal("fault fixture not recovered")
				}
			}
		}
		if !found {
			t.Fatal("missing fault fixture")
		}
	}
}

func strictResumeReport(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cp strictCheckpoint
	if err = json.Unmarshal(b, &cp); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	var md strings.Builder
	fmt.Fprintf(&md, "# 四条八轮线上严格消融：续跑结果\n\n完整采集：%v。模型 `%s`，固定v3 manifest `%s`，串行、温度0、每次输出上限512、网关自动重试0。Jev为确定性Go路由，不包含外部小模型评测。\n\n", cp.Complete, cp.Model, cp.ManifestHash)
	md.WriteString("A=完整历史/Jev关；B=完整历史/Jev开；D=三级压缩/Jev关；E=相同三级压缩/Jev开。所有组后端权限和确认条件相同。128轮Gold回放与已完成连续轨迹原样继承，原checkpoint保持不变。超时后的未执行轮不计作API调用，也不以0ms混入延迟。\n\n")
	known, unknown := 0, 0
	for _, a := range cp.Attempts {
		known += a.Usage.TotalTokens
		if a.Error != "" || a.Usage.TotalTokens == 0 {
			unknown += a.ReservedTokens
		}
	}
	fmt.Fprintf(&md, "## 消耗与完整性\n\n正式实验Provider已返回用量：%d Token；未知计费预留：%d；此前诊断/校准预算：%d；总预算记账：**%d / 4,000,000 Token**。预留不是实际账单。\n\n", known, unknown, cp.PreludeTokens, strictCharged(&cp))
	paired := []string{}
	for _, c := range strictCases() {
		ok := true
		for _, g := range strictGroups {
			n := 0
			for _, r := range cp.Results {
				if r.Stage == "free_running_e2e" && r.Case == c.ID && r.Group == g.ID {
					if r.APIError != "" || r.Status == "not_executed" {
						ok = false
					}
					n++
				}
			}
			if n != 8 {
				ok = false
			}
		}
		if ok {
			paired = append(paired, c.ID)
		}
	}
	fmt.Fprintf(&md, "完整且无API中断的四组同源连续样本：%s。质量指标使用全部4条，效率配对只使用这些完成样本，避免失败组因少发请求被误认为更省。此口径也必须与含故障的全量结果一起阅读。\n\n", strings.Join(paired, ","))
	type row struct {
		Scope, Group                                                                                                                           string
		Turns, Observed, Calls, Input, Output, Peak, TaskSuccess, TaskTotal, ToolCorrect, ArgCorrect, CorefCorrect, CorefN, APIErrors, Skipped int
		P50, Avg, P95, Context, Network, Jev, Other                                                                                            float64
		BusinessSuccess, ToolsUsed, Bypassed, CachedTokens, CacheSamples, CacheEligibleTokens, FaultN, FaultSuccess, RedundantCalls            int
	}
	var rows []row
	for _, scope := range []string{"Gold回放/全4条", "连续E2E/全4条", "连续E2E/完整配对"} {
		md.WriteString("## " + scope + "\n\n")
		md.WriteString("|组|执行轮/计划轮|API请求|输入Token|输出Token|Turn6+峰值|工具名/参数正确|指代正确|最终通过|API错误/未执行轮|\n|---|---:|---:|---:|---:|---:|---|---|---|---|\n")
		stage := "free_running_e2e"
		if strings.HasPrefix(scope, "Gold") {
			stage = "teacher_forced_replay"
		}
		include := func(id string) bool {
			if scope != "连续E2E/完整配对" {
				return true
			}
			for _, p := range paired {
				if p == id {
					return true
				}
			}
			return false
		}
		for _, g := range strictGroups {
			rw := row{Scope: scope, Group: g.ID}
			var lats []int64
			for _, r := range cp.Results {
				if r.Stage != stage || r.Group != g.ID || !include(r.Case) {
					continue
				}
				rw.Turns++
				if r.Status == "not_executed" {
					rw.Skipped++
				} else {
					rw.Observed++
					lats = append(lats, r.TotalLatencyMS)
					rw.Avg += float64(r.TotalLatencyMS)
					rw.Context += float64(r.ContextLatencyMS)
					rw.Network += float64(r.LLMNetworkLatencyMS)
					rw.Jev += float64(r.JevLatencyMS)
					rw.Other += float64(r.OtherLatencyMS)
				}
				rw.Calls += r.ProviderCalls
				if len(r.Tools) > 0 {
					rw.ToolsUsed++
				}
				if r.ProviderCalls == 0 && r.APIError == "" && r.Status != "not_executed" {
					rw.Bypassed++
				}
				if r.Case == "S4" && r.Turn == 4 {
					rw.FaultN++
					if r.FaultRecovered {
						rw.FaultSuccess++
					}
				}
				if len(r.Tools) > 1 {
					rw.RedundantCalls += len(r.Tools) - 1
				}
				if r.ToolNameCorrect {
					rw.ToolCorrect++
				}
				if r.ArgumentsCorrect {
					rw.ArgCorrect++
				}
				if r.Turn == 6 || r.Turn == 8 {
					rw.CorefN++
					if r.CorefCorrect {
						rw.CorefCorrect++
					}
				}
				if r.Turn == 8 {
					rw.TaskTotal++
					business := r.TaskPassed
					if stage == "free_running_e2e" {
						for _, pr := range cp.Results {
							if pr.Stage == stage && pr.Group == g.ID && pr.Case == r.Case && (pr.EarlyCancel || pr.APIError != "" || !pr.FaultRecovered) {
								business = false
							}
						}
					}
					if business {
						rw.BusinessSuccess++
					}
					pass := r.TaskPassed
					if stage == "free_running_e2e" {
						for _, pr := range cp.Results {
							if pr.Stage == stage && pr.Group == g.ID && pr.Case == r.Case && (!pr.ArgumentsCorrect || pr.EarlyCancel || pr.APIError != "" || !pr.FaultRecovered) {
								pass = false
							}
						}
					}
					if pass {
						rw.TaskSuccess++
					}
				}
			}
			for _, a := range cp.Attempts {
				if a.Stage != stage || a.Group != g.ID || !include(a.Case) {
					continue
				}
				rw.Input += a.Usage.InputTokens
				rw.Output += a.Usage.OutputTokens
				if a.Usage.CacheUsageReported {
					rw.CacheSamples++
					rw.CachedTokens += a.Usage.CachedTokens
					rw.CacheEligibleTokens += a.Usage.InputTokens
				}
				if a.Turn >= 6 && a.Usage.InputTokens > rw.Peak {
					rw.Peak = a.Usage.InputTokens
				}
				if a.Error != "" {
					rw.APIErrors++
				}
			}
			if rw.Observed > 0 {
				n := float64(rw.Observed)
				rw.Avg /= n
				rw.Context /= n
				rw.Network /= n
				rw.Jev /= n
				rw.Other /= n
			}
			rw.P50 = strictPercentile(append([]int64(nil), lats...), .5)
			rw.P95 = strictPercentile(lats, .95)
			fmt.Fprintf(&md, "|%s|%d/%d|%d|%d|%d|%d|%d/%d；%d/%d|%d/%d|%d/%d|%d/%d|\n", rw.Group, rw.Observed, rw.Turns, rw.Calls, rw.Input, rw.Output, rw.Peak, rw.ToolCorrect, rw.Turns, rw.ArgCorrect, rw.Turns, rw.CorefCorrect, rw.CorefN, rw.TaskSuccess, rw.TaskTotal, rw.APIErrors, rw.Skipped)
			rows = append(rows, rw)
		}
		md.WriteString("\n|组|P50(ms)|Avg(ms)|P95(ms)|Context均值|LLM网络均值|Jev均值|其他均值|\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, r := range rows {
			if r.Scope == scope {
				fmt.Fprintf(&md, "|%s|%.1f|%.1f|%.1f|%.1f|%.1f|%.1f|%.1f|\n", r.Group, r.P50, r.Avg, r.P95, r.Context, r.Network, r.Jev, r.Other)
			}
		}
		md.WriteString("\n|组|业务最终完成|严格轨迹通过|每API平均输入|每计划轮平均输入|工具使用轮|主模型绕过轮|API错误率|故障恢复|额外工具调用|缓存Token/已返回字段样本|\n|---|---|---|---:|---:|---|---|---|---|---:|---|\n")
		for _, r := range rows {
			if r.Scope != scope {
				continue
			}
			cache := "N/A"
			if r.CacheSamples > 0 {
				cache = fmt.Sprintf("%d；%.2f%%；%d/%d次返回", r.CachedTokens, percentage(r.CachedTokens, r.CacheEligibleTokens), r.CacheSamples, r.Calls)
			}
			perCall, perTurn := 0.0, 0.0
			if r.Calls > 0 {
				perCall = float64(r.Input) / float64(r.Calls)
			}
			if r.Turns > 0 {
				perTurn = float64(r.Input) / float64(r.Turns)
			}
			fmt.Fprintf(&md, "|%s|%d/%d|%d/%d|%.1f|%.1f|%d/%d|%d/%d|%d/%d (%.2f%%)|%d/%d|%d|%s|\n", r.Group, r.BusinessSuccess, r.TaskTotal, r.TaskSuccess, r.TaskTotal, perCall, perTurn, r.ToolsUsed, r.Turns, r.Bypassed, r.Turns, r.APIErrors, r.Calls, percentage(r.APIErrors, r.Calls), r.FaultSuccess, r.FaultN, r.RedundantCalls, cache)
		}
		md.WriteString("\n|组/Turn（平均输入Token / 总延迟ms）|1|2|3|4|5|6|7|8|\n|---|---|---|---|---|---|---|---|---|\n")
		for _, g := range strictGroups {
			fmt.Fprintf(&md, "|%s", g.ID)
			for turn := 1; turn <= 8; turn++ {
				n, tokens, ms := 0, 0, int64(0)
				for _, r := range cp.Results {
					if r.Stage == stage && r.Group == g.ID && r.Turn == turn && include(r.Case) && r.Status != "not_executed" {
						n++
						ms += r.TotalLatencyMS
					}
				}
				for _, a := range cp.Attempts {
					if a.Stage == stage && a.Group == g.ID && a.Turn == turn && include(a.Case) {
						tokens += a.Usage.InputTokens
					}
				}
				if n == 0 {
					md.WriteString("|N/A")
				} else {
					fmt.Fprintf(&md, "|%.0f / %.0f", float64(tokens)/float64(n), float64(ms)/float64(n))
				}
			}
			md.WriteString("|\n")
		}
	}
	md.WriteString("\n## 独立贡献与解释边界\n\n")
	for _, scope := range []string{"Gold回放/全4条", "连续E2E/完整配对"} {
		m := map[string]row{}
		for _, r := range rows {
			if r.Scope == scope {
				m[r.Group] = r
			}
		}
		for _, p := range [][3]string{{"A", "D", "压缩（Jev关闭）"}, {"B", "E", "压缩（Jev开启）"}, {"A", "B", "Jev（完整历史）"}, {"D", "E", "Jev（三级压缩）"}} {
			a, b := m[p[0]], m[p[1]]
			if a.Input > 0 {
				fmt.Fprintf(&md, "- %s，%s：累计输入降低 %.2f%%；平均总延迟 %.1f→%.1f ms。\n", scope, p[2], 100*(1-float64(b.Input)/float64(a.Input)), a.Avg, b.Avg)
			}
		}
	}
	md.WriteString("\n只有4条样本，不代表生产总体准确率或P95 SLA。回放最后一轮通过不是端到端成功。自然语言参数按核心关键词包含、订单ID和枚举精确匹配；不把主模型绕过率当准确率。本次不覆盖越权/无单/投诉全分类，Guardrail Macro-F1不可计算。LLM网络含真实网关等待，其他耗时含工具、计数与证据刷盘；子延迟和等于总延迟。实际输入按Provider usage，失败无usage按完整预留记账，无法仅凭本地计数保证网关额外前缀长度。没有实际费率不计算美元成本。完整配对剔除基础设施中断是条件统计，含失败的全4条表必须同时保留。\n\n")
	md.WriteString("## 原始故障与未执行记录\n\n")
	md.WriteString("业务完成仅验证正确目标最终取消、没有提前取消及工具瞬时故障恢复；严格轨迹还要求八轮全部工具与参数匹配。额外工具调用按每轮超出一个的数量统计，其中取消前一次同订单状态校验由原评分协议允许，故不可全部解释为无效调用。缓存占比仅在返回cached_tokens字段的请求中计算，缺失字段不补零。Jev毫秒打点显示0只代表小于1ms分辨率，不代表没有成本。\n\n")
	for _, r := range cp.Results {
		if r.APIError != "" {
			fmt.Fprintf(&md, "- `%s`：%s；Provider请求数%d；状态%s。\n", r.Key, r.APIError, r.ProviderCalls, r.Status)
		}
	}
	md.WriteString("\n## 可用结论与简历措辞\n\n")
	m := map[string]row{}
	for _, r := range rows {
		if r.Scope == "连续E2E/完整配对" {
			m[r.Group] = r
		}
	}
	if a, d, e := m["A"], m["D"], m["E"]; a.Input > 0 && d.Input > 0 {
		fmt.Fprintf(&md, "在3条四组完整运行的八轮同源样本中，单独启用三级压缩使累计输入Token降低%.2f%%；在相同压缩器上启用Jev再降低%.2f%%，两者组合较基线降低%.2f%%，Turn6+单请求峰值降低%.2f%%。压缩构建均值%.1fms，网关网络均值%.1fms。\n\n", 100*(1-float64(d.Input)/float64(a.Input)), 100*(1-float64(e.Input)/float64(d.Input)), 100*(1-float64(e.Input)/float64(a.Input)), 100*(1-float64(e.Peak)/float64(a.Peak)), e.Context, e.Network)
		md.WriteString("不能据此声称Jev普遍提高准确率或主流程P95低于2秒；两条E轨迹出现额外查询，另有一次历史网关超时，必须同时披露。\n\n")
		fmt.Fprintf(&md, "简历保守示例：针对电商客服长对话中工具载荷膨胀及取消确认开销，设计状态槽锚定、历史载荷脱水与Token预算压缩，结合确定性Jev工具路由和确认直通；在四组消融中，3条完整配对八轮场景的累计输入降低%.2f%%、Turn6+单请求峰值降低%.2f%%。此结果为小样本线上验证，不作为生产总体SLA或准确率结论。\n", 100*(1-float64(e.Input)/float64(a.Input)), 100*(1-float64(e.Peak)/float64(a.Peak)))
	}
	obj := map[string]any{"complete": cp.Complete, "manifestHash": cp.ManifestHash, "pairedCompleteCases": paired, "aggregates": rows, "chargedBudgetTokens": strictCharged(&cp), "returnedUsageTokens": known, "unknownReserveTokens": unknown, "preludeTokens": cp.PreludeTokens}
	obj["reportSourceHash"] = strictHashFile("strict_resume_test.go")
	j, _ := json.MarshalIndent(obj, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "final-resumed-summary.json"), j, 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "final-resumed-report.md"), []byte(md.String()), 0600)
}
