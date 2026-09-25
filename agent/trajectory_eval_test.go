package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"product/datamodels"
)

type trajectoryStep struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Success   bool           `json:"success"`
}

type trajectoryCase struct {
	ID             string           `json:"id"`
	Kind           string           `json:"kind"`
	Input          string           `json:"input"`
	Steps          []trajectoryStep `json:"steps"`
	Answer         string           `json:"answer"`
	Confirmation   bool             `json:"confirmation"`
	ProductFailure bool             `json:"productFailure"`
}

type scriptedEvalProvider struct{ scenario trajectoryCase }

func (p *scriptedEvalProvider) Name() string  { return "scripted" }
func (p *scriptedEvalProvider) Model() string { return "trajectory-fixture-v1" }
func (p *scriptedEvalProvider) Complete(_ context.Context, request CompletionRequest) (Completion, error) {
	completed := 0
	for _, message := range request.Messages {
		if message.Role == "tool" {
			completed++
		}
	}
	if completed >= len(p.scenario.Steps) {
		return Completion{Message: Message{Role: "assistant", Content: p.scenario.Answer}}, nil
	}
	step := p.scenario.Steps[completed]
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: fmt.Sprintf("step-%d", completed), Name: step.Name, Arguments: mustJSON(step.Arguments)}}}}, nil
}

type failingProductService struct{ productServiceMock }

func (m *failingProductService) ListProducts(int, int, string) ([]*datamodels.Product, int64, error) {
	return nil, 0, context.DeadlineExceeded
}

func TestAgentTrajectoryEvaluation(t *testing.T) {
	data, err := os.ReadFile("testdata/trajectory_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []trajectoryCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 10 {
		t.Fatalf("expected 10 fixed trajectory cases, got %d", len(cases))
	}
	seen := make(map[string]bool)
	results := make([]evalResult, 0, len(cases)*5)
	passed := 0
	for _, tc := range cases {
		if tc.ID == "" || tc.Input == "" || tc.Answer == "" || len(tc.Steps) == 0 || seen[tc.ID] {
			t.Fatalf("invalid trajectory case: %+v", tc)
		}
		seen[tc.ID] = true
		for run := 1; run <= 5; run++ {
			orders := &orderServiceMock{order: &datamodels.Order{ID: 18, UserID: 42, OrderStatus: datamodels.OrderWait}}
			products := &productServiceMock{items: []*datamodels.Product{{ID: 1, ProductName: "键盘", ProductNum: 3}}}
			svc := NewService(&scriptedEvalProvider{scenario: tc}, orders, products)
			if tc.ProductFailure {
				svc.products = &failingProductService{}
			}
			started := time.Now()
			response, chatErr := svc.Chat(context.Background(), 42, ChatRequest{Message: tc.Input})
			result := evalResult{ID: tc.ID, Tier: tc.Kind, Run: run, Steps: len(response.ToolCalls), LatencyMS: time.Since(started).Milliseconds()}
			switch {
			case chatErr != nil:
				result.Reason = chatErr.Error()
			case len(response.ToolCalls) != len(tc.Steps):
				result.Reason = fmt.Sprintf("steps=%d want=%d", len(response.ToolCalls), len(tc.Steps))
			case response.Message != tc.Answer:
				result.Reason = "final response mismatch"
			case (response.Confirmation != nil) != tc.Confirmation:
				result.Reason = "confirmation mismatch"
			case orders.cancelCallCount != 0:
				result.Reason = "cancellation occurred before confirmation"
			default:
				for i, step := range tc.Steps {
					trace := response.ToolCalls[i]
					var args map[string]any
					if err := json.Unmarshal(trace.Arguments, &args); err != nil || trace.Name != step.Name || trace.Success != step.Success || !reflect.DeepEqual(args, step.Arguments) {
						result.Reason = fmt.Sprintf("step %d trace mismatch", i+1)
						break
					}
					var toolResult struct {
						OK bool `json:"ok"`
					}
					if err := json.Unmarshal(trace.Result, &toolResult); err != nil || toolResult.OK != step.Success {
						result.Reason = fmt.Sprintf("step %d tool result mismatch", i+1)
						break
					}
				}
			}
			result.Passed = result.Reason == ""
			if result.Passed {
				passed++
			} else {
				t.Errorf("%s run %d: %s", tc.ID, run, result.Reason)
			}
			results = append(results, result)
		}
	}
	report := map[string]any{"schemaVersion": 1, "dataset": "trajectory-v1", "mode": "scripted", "cases": len(cases), "repetitions": 5, "passed": passed, "total": len(results), "results": results}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AGENT_TRAJECTORY_REPORT=%s", encoded)
	if path := os.Getenv("AGENT_TRAJECTORY_REPORT_PATH"); path != "" {
		if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
