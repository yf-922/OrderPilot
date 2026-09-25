package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"product/datamodels"
)

type contextProbeProvider struct {
	maxBytes    int
	maxMessages int
}

func (p *contextProbeProvider) Name() string  { return "context-probe" }
func (p *contextProbeProvider) Model() string { return "context-probe-v1" }
func (p *contextProbeProvider) Complete(_ context.Context, input CompletionRequest) (Completion, error) {
	bytes := 0
	for _, message := range input.Messages {
		bytes += len(message.Content) + len(message.Role) + len(message.Name)
	}
	if bytes > p.maxBytes {
		p.maxBytes = bytes
	}
	if len(input.Messages) > p.maxMessages {
		p.maxMessages = len(input.Messages)
	}
	last := input.Messages[len(input.Messages)-1]
	if last.Role == "tool" {
		return Completion{Message: Message{Role: "assistant", Content: "已查询商品。"}}, nil
	}
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "probe", Name: "search_products", Arguments: mustJSON(map[string]any{"keyword": "", "low_stock_only": false})}}}}, nil
}

func TestAgentLongConversationContextMeasurement(t *testing.T) {
	products := make([]*datamodels.Product, 100)
	for i := range products {
		products[i] = &datamodels.Product{ID: int64(i + 1), ProductName: fmt.Sprintf("商品-%03d", i), ProductNum: 5, ProductImage: strings.Repeat("x", 300)}
	}
	probe := &contextProbeProvider{}
	svc := NewService(probe, &orderServiceMock{}, &productServiceMock{items: products})
	conversationID := ""
	for i := 0; i < 12; i++ {
		response, err := svc.Chat(context.Background(), 42, ChatRequest{ConversationID: conversationID, Message: fmt.Sprintf("第%d次查询商品", i+1)})
		if err != nil {
			t.Fatal(err)
		}
		conversationID = response.ConversationID
	}
	t.Logf("CONTEXT_MEASUREMENT turns=12 maxInputBytes=%d maxMessages=%d", probe.maxBytes, probe.maxMessages)
	if probe.maxBytes == 0 || probe.maxBytes > maxContextBytes || probe.maxMessages > maxConversationSize {
		t.Fatalf("context budget exceeded: bytes=%d messages=%d", probe.maxBytes, probe.maxMessages)
	}
}
