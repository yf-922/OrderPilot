package controllers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"product/agent"
	"product/common"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/mvc"
)

// This opt-in test uses the real controller/auth/orchestrator and a local
// deterministic Provider. It never constructs an external model client.
func TestAgentLocalHTTPLoad(t *testing.T) {
	if os.Getenv("AGENT_LOCAL_LOAD") != "1" {
		t.Skip("set AGENT_LOCAL_LOAD=1; no paid API calls")
	}
	t.Setenv("JEV_EXTERNAL_ENABLED", "false")
	t.Setenv("AGENT_JEV_ENABLED", "true")
	t.Setenv("AGENT_JEV_SHADOW", "false")
	t.Setenv("AGENT_CONTEXT_STRATEGY", "ours_full")
	t.Setenv("AGENT_CONTEXT_TOKEN_BUDGET", "12000")
	t.Setenv("AUTH_SECRET", "local-load-fixture-not-a-production-secret")
	type row struct {
		Scenario    string  `json:"scenario"`
		Concurrency int     `json:"concurrency"`
		Requests    int     `json:"requests"`
		Failures    int     `json:"failures"`
		Tools       int     `json:"successful_tools"`
		Seconds     float64 `json:"seconds"`
		RPS         float64 `json:"requests_per_second"`
		Avg         float64 `json:"avg_ms"`
		P50         float64 `json:"p50_ms"`
		P95         float64 `json:"p95_ms"`
		P99         float64 `json:"p99_ms"`
	}
	rows := []row{}
	for _, scenario := range []struct {
		name, message, status string
		tools                 int
	}{
		{"policy_rag", "配送时效规则是什么", "answered", 1},
		{"human_short_circuit", "我要找人工客服投诉", "handoff", 0},
	} {
		for _, concurrency := range []int{1, 8, 32} {
			svc := agent.NewServiceWithRedisAndKnowledge(agent.NewLocalProvider(), nil, nil, nil, agent.DefaultKnowledgeRetriever())
			app := iris.New()
			app.Logger().SetLevel("disable")
			party := app.Party("/agent")
			party.Use(common.RequireAuth)
			mvc.New(party).Register(svc).Handle(new(AgentController))
			if err := app.Build(); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(app)
			transport := &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64}
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			token := common.IssueToken(42)
			body, _ := json.Marshal(agent.ChatRequest{Message: scenario.message})
			invoke := func() (bool, int, float64) {
				req, _ := http.NewRequest(http.MethodPost, server.URL+"/agent/chat", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				start := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					return false, 0, float64(time.Since(start).Microseconds()) / 1000
				}
				raw, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				elapsed := float64(time.Since(start).Microseconds()) / 1000
				var decoded struct {
					Data agent.ChatResponse `json:"data"`
				}
				parseErr := json.Unmarshal(raw, &decoded)
				ok := resp.StatusCode == http.StatusOK && readErr == nil && parseErr == nil && decoded.Data.TaskStatus == scenario.status && decoded.Data.Message != "" && len(decoded.Data.ToolCalls) == scenario.tools
				tools := 0
				for _, call := range decoded.Data.ToolCalls {
					if call.Success {
						tools++
					} else {
						ok = false
					}
				}
				// Bound session growth. Cleanup is outside request latency, but is
				// included in wall-clock throughput, making RPS conservative.
				if decoded.Data.ConversationID != "" {
					svc.Reset(42, decoded.Data.ConversationID)
				}
				return ok, tools, elapsed
			}
			for i := 0; i < 20; i++ {
				if ok, _, _ := invoke(); !ok {
					t.Fatal("warmup failed")
				}
			}
			const requests = 10000
			jobs := make(chan int)
			latencies := make([]float64, requests)
			outcomes := make([]bool, requests)
			toolCounts := make([]int, requests)
			var wg sync.WaitGroup
			started := time.Now()
			for worker := 0; worker < concurrency; worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for index := range jobs {
						outcomes[index], toolCounts[index], latencies[index] = invoke()
					}
				}()
			}
			for i := 0; i < requests; i++ {
				jobs <- i
			}
			close(jobs)
			wg.Wait()
			r := row{Scenario: scenario.name, Concurrency: concurrency, Requests: requests, Seconds: time.Since(started).Seconds()}
			for i, ms := range latencies {
				r.Avg += ms
				r.Tools += toolCounts[i]
				if !outcomes[i] {
					r.Failures++
				}
			}
			r.Avg /= requests
			r.RPS = requests / r.Seconds
			sort.Float64s(latencies)
			percentile := func(p float64) float64 { return latencies[int(math.Ceil(p*requests))-1] }
			r.P50, r.P95, r.P99 = percentile(.50), percentile(.95), percentile(.99)
			rows = append(rows, r)
			transport.CloseIdleConnections()
			server.Close()
			if r.Failures > 0 {
				t.Errorf("%s concurrency=%d failures=%d", r.Scenario, concurrency, r.Failures)
			}
		}
	}
	report := struct {
		GeneratedAt string `json:"generated_at"`
		Go          string `json:"go"`
		OS          string `json:"os"`
		Arch        string `json:"arch"`
		CPUs        int    `json:"logical_cpus"`
		GOMAXPROCS  int    `json:"gomaxprocs"`
		Scope       string `json:"scope"`
		Rows        []row  `json:"rows"`
	}{time.Now().Format(time.RFC3339), runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0), "loopback HTTP + actual Iris controller/auth + shared Agent + local Provider; no Redis/MySQL/remote LLM/tokenizer; single-turn fresh sessions; closed-loop; 20 warmup requests per row excluded", rows}
	path := os.Getenv("AGENT_LOCAL_LOAD_REPORT")
	if path == "" {
		path = "../../../docs/agent-local-load.json"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(path, encoded, 0644); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		t.Logf("%s c=%d n=%d errors=%d RPS=%.1f avg=%.3f p50=%.3f p95=%.3f p99=%.3f", r.Scenario, r.Concurrency, r.Requests, r.Failures, r.RPS, r.Avg, r.P50, r.P95, r.P99)
	}
	t.Log(fmt.Sprintf("JSON report: %s", path))
}
