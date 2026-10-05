package agent

import (
	"context"
	"strings"
	"testing"
)

func TestKnowledgeRetrieverReturnsRelevantSource(t *testing.T) {
	retriever := DefaultKnowledgeRetriever()
	chunks, err := retriever.Retrieve(context.Background(), "订单地址修改", 2)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("expected source, chunks=%+v err=%v", chunks, err)
	}
	if chunks[0].DocumentID != "address-policy-v1" {
		t.Fatalf("unexpected source: %+v", chunks[0])
	}
}

func TestKnowledgeRetrieverDoesNotInventForUnknownQuery(t *testing.T) {
	chunks, err := DefaultKnowledgeRetriever().Retrieve(context.Background(), "积分兑换规则", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected no source, got %+v", chunks)
	}
}

func TestAgenticRAGUsesKnowledgeToolForPolicyQuestion(t *testing.T) {
	svc := NewServiceWithRedisAndKnowledge(NewLocalProvider(), &orderServiceMock{}, &productServiceMock{}, nil, DefaultKnowledgeRetriever())
	response, err := svc.Chat(context.Background(), 42, ChatRequest{Message: "订单地址可以修改吗？"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "search_knowledge" {
		t.Fatalf("expected knowledge tool path, got %+v", response.ToolCalls)
	}
	if !response.ToolCalls[0].Success || !strings.Contains(string(response.ToolCalls[0].Result), "address-policy-v1") {
		t.Fatalf("expected cited knowledge source, got %+v", response.ToolCalls[0])
	}
}

func TestKnowledgeResultCompactionReducesModelContext(t *testing.T) {
	raw := mustJSON(map[string]any{"found": true, "items": []KnowledgeChunk{{DocumentID: "policy", Content: strings.Repeat("地址修改规则 ", 200)}}})
	compact := compactToolResult("search_knowledge", raw)
	if len(compact) >= len(raw) || len(compact) > 5000 {
		t.Fatalf("knowledge result was not compacted: raw=%d compact=%d", len(raw), len(compact))
	}
}
