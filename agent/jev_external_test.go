package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJevExternalGateway(t *testing.T) {
	for _, scenario := range []string{"success", "low_confidence", "unknown_order", "bad_probability", "missing_answer", "http_error", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("JEV_TIMEOUT_MS", "1000")
			if scenario == "timeout" {
				t.Setenv("JEV_TIMEOUT_MS", "5")
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer typesafe-test-key" {
					t.Error("TypeSafe 路径或独立凭据错误")
				}
				var payload struct {
					Model     string                      `json:"model"`
					State     json.RawMessage             `json:"state"`
					Questions map[string]typeSafeQuestion `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if strings.Contains(string(payload.State), "private-confirm-secret") || strings.Contains(string(payload.State), "legacy-secret") {
					t.Error("确认令牌泄露")
				}
				if payload.Model != "jev-1.13.0" || len(payload.Questions) != 8 {
					t.Error("非原生 TypeSafe 请求")
				}
				if scenario == "http_error" {
					w.WriteHeader(451)
					return
				}
				if scenario == "timeout" {
					<-r.Context().Done()
					return
				}
				answers := map[string]any{}
				for key, q := range payload.Questions {
					if q.Type == "noul" {
						p := 0.01
						if key == "tool_search_products" || key == "tool_search_knowledge" {
							p = 0.99
						}
						answers[key] = map[string]any{"type": "noul", "noul": p}
						continue
					}
					choice := map[string]string{"route": "CONTINUE", "intent": "PRODUCT_QUERY", "target_order": "10086"}[key]
					confidence := 0.99
					if scenario == "low_confidence" {
						confidence = 0.1
					}
					probabilities := map[string]float64{}
					for option := range q.Criteria {
						probabilities[option] = 0
					}
					probabilities[choice] = 1
					if scenario == "unknown_order" && key == "target_order" {
						choice = "99999"
					}
					if scenario == "bad_probability" {
						probabilities[choice] = -1
					}
					answers[key] = map[string]any{"type": "choice", "choice": choice, "confidence": confidence, "probabilities": probabilities}
				}
				if scenario == "missing_answer" {
					delete(answers, "tool_search_knowledge")
				}
				json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0", "answers": answers, "usage": map[string]int{"input_tokens": 500, "output_tokens": 80}})
			}))
			defer server.Close()
			engine := &JevDecisionEngine{enabled: true, model: "jev-1.13.0", provider: newTypeSafeClient(server.URL+"/v1", "typesafe-test-key", "jev-1.13.0")}
			input := JevInput{UserQuery: "查商品，顺便讲下配送规则", RecentTurnsBrief: "private-confirm-secret legacy-secret", State: SessionState{
				OwnedOrderIDs: []int64{10086}, PendingConfirm: &ConfirmTokenSnapshot{Token: "private-confirm-secret", OrderID: 10086, ExpiresAt: time.Now().Add(time.Minute).Unix()}, PendingCancel: ConfirmTokenInfo{Token: "legacy-secret"},
			}}
			decision := engine.Evaluate(context.Background(), input)
			if calls != 1 || decision.RouteAction != RouteProceedMainLLM {
				t.Fatalf("calls=%d decision=%+v", calls, decision)
			}
			if scenario == "success" {
				if decision.Source != "typesafe" || decision.TargetOrderID != 10086 || len(decision.AllowedTools) != 2 || decision.Usage.TotalTokens != 580 {
					t.Fatalf("未使用外部决策: %+v", decision)
				}
			} else if decision.Source != "fallback" {
				t.Fatalf("应安全回退: %+v", decision)
			}
			engine.Evaluate(context.Background(), JevInput{UserQuery: "查询我的订单列表"})
			engine.Evaluate(context.Background(), JevInput{UserQuery: "确认取消", State: input.State})
			if calls != 1 {
				t.Fatal("普通请求和确定性确认不应调用外部 Jev")
			}
		})
	}
}

func TestJevCredentialsIsolation(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("AI_API_KEY", "main-key-must-not-be-used")
	t.Setenv("JEV_EXTERNAL_ENABLED", "true")
	if NewJevDecisionEngine().provider != nil {
		t.Fatal("不能将主模型 Key 用于 TypeSafe")
	}
}

func TestJevDedicatedProxy(t *testing.T) {
	t.Setenv("TYPESAFE_PROXY_URL", "http://127.0.0.1:7897")
	client := newTypeSafeClient("https://api.typesafe.ai/v1", "test", "jev-1.13.0")
	req, _ := http.NewRequest(http.MethodPost, "https://api.typesafe.ai/v1/systemone", nil)
	address, err := client.http.Transport.(*http.Transport).Proxy(req)
	if err != nil || address.String() != "http://127.0.0.1:7897" {
		t.Fatal("专用代理未应用")
	}
	t.Setenv("TYPESAFE_PROXY_URL", ":invalid")
	client = newTypeSafeClient("https://api.typesafe.ai/v1", "test", "jev-1.13.0")
	if _, err := client.evaluate(context.Background(), JevInput{}); err == nil {
		t.Fatal("代理配置错误时不能静默直连")
	}
}

// 显式启用才收费；只运行一次原生 Jev 决策，不请求主模型。
func TestJevExternalLiveSmoke(t *testing.T) {
	if os.Getenv("JEV_LIVE_SMOKE") != "1" {
		t.Skip("未启用真实 Jev 冒烟测试")
	}
	engine := NewJevDecisionEngine()
	if engine.provider == nil {
		t.Fatal("缺少 TYPESAFE_API_KEY 或 JEV_EXTERNAL_ENABLED 未启用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := time.Now()
	decision := engine.Evaluate(ctx, JevInput{UserQuery: "帮我看看库存快没了的商品，顺便讲一下配送超时规则", State: SessionState{TotalOrderCount: -1}})
	latency := time.Since(started).Milliseconds()
	record := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "latency_ms": latency, "decision": decision, "requests": 1, "main_model_requests": 0, "usage_reported_in_decision": decision.Source == "typesafe"}
	encoded, _ := json.MarshalIndent(record, "", "  ")
	if err := os.WriteFile(filepath.Join("..", "docs", "typesafe-live-smoke.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if decision.Source != "typesafe" {
		t.Fatalf("真实模型未成功采用（详见回退日志）: %+v", decision)
	}
	t.Logf("官方模型=%s 来源=%s 延迟=%dms 输入=%d 输出=%d 决策=%+v", decision.Model, decision.Source, latency, decision.Usage.InputTokens, decision.Usage.OutputTokens, decision)
	if decision.RouteAction != RouteProceedMainLLM || !containsTool(decision.AllowedTools, "search_knowledge") || !containsTool(decision.AllowedTools, "search_products") {
		t.Fatal("多意图路由未通过")
	}
}

func containsTool(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}
