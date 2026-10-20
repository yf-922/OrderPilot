package agent

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTypeSafeEfficiencyEvidenceOffline(t *testing.T) {
	data, err := os.ReadFile("../docs/typesafe-efficiency-v1/checkpoint.json")
	if os.IsNotExist(err) {
		t.Skip("尚未生成线上评测证据")
	}
	if err != nil {
		t.Fatal(err)
	}
	var cp efficiencyCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatal(err)
	}
	// 恢复原始结构体的 JSON 字段顺序，再验证现有冻结哈希。
	cp.Manifest["cases"] = efficiencyCases()
	cp.Manifest["arms"] = efficiencyArms
	if !cp.Complete || len(cp.Results) != 48 || strictHash(cp.Manifest) != cp.ManifestHash {
		t.Fatal("完成标记、结果数量或冻结配置不一致")
	}
	seen, counts := map[string]bool{}, map[string]int{}
	for _, r := range cp.Results {
		if seen[r.Key] || r.APIError != "" || r.EarlyCancel {
			t.Fatalf("重复、失败或提前执行: %s", r.Key)
		}
		seen[r.Key] = true
		counts[r.Group]++
		if r.OtherLatencyMS < 0 || r.TotalLatencyMS != r.ContextLatencyMS+r.LLMNetworkLatencyMS+r.JevLatencyMS+r.OtherLatencyMS {
			t.Fatalf("延迟未闭合: %s", r.Key)
		}
	}
	used := 0
	for _, attempt := range cp.Attempts {
		if !seen[attempt.Key] || attempt.Error != "" || attempt.Usage.TotalTokens <= 0 || attempt.Usage.TotalTokens != attempt.Usage.InputTokens+attempt.Usage.OutputTokens {
			t.Fatalf("请求或实际用量不完整: %s", attempt.Key)
		}
		used += attempt.Usage.TotalTokens
		if attempt.Kind == "jev" && attempt.Arm != "J_compressed_typesafe" {
			t.Fatal("外部模型污染对照组")
		}
	}
	if used > efficiencyBudget || used+efficiencyPriorReserve > 10000000 {
		t.Fatal("预算越界")
	}
	for _, arm := range efficiencyArms {
		if counts[arm.ID] != 16 {
			t.Fatal("对照样本不齐")
		}
		for _, c := range efficiencyCases() {
			snapshot, ok := cp.Snapshots[c.ID+"/"+arm.ID]
			if !ok || len(snapshot.Cancelled) != 1 || snapshot.Cancelled[0] != c.Target {
				t.Fatalf("后端最终取消证据不正确: %s/%s", c.ID, arm.ID)
			}
		}
	}
	t.Logf("证据核验通过：48 个用户轮次，%d 次真实请求，%d Token", len(cp.Attempts), used)
}
