package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type ragEvalCase struct {
	ID       string   `json:"id"`
	Query    string   `json:"query"`
	Expected []string `json:"expected"`
}

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
	t.Logf("RAG_CONTEXT_COMPACTION rawBytes=%d compactBytes=%d reduction=%.1f%%", len(raw), len(compact), float64(len(raw)-len(compact))*100/float64(len(raw)))
	if len(compact) >= len(raw) || len(compact) > 5000 {
		t.Fatalf("knowledge result was not compacted: raw=%d compact=%d", len(raw), len(compact))
	}
}

func TestHybridRetrieverRegressionSet(t *testing.T) {
	data, err := os.ReadFile("testdata/rag_eval.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []ragEvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	var hits, unknowns, unknownFalsePositives int
	var reciprocalRank float64
	for _, tc := range cases {
		chunks, err := DefaultKnowledgeRetriever().Retrieve(context.Background(), tc.Query, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(tc.Expected) == 0 {
			unknowns++
			if len(chunks) > 0 {
				unknownFalsePositives++
				t.Logf("FALSE_POSITIVE id=%s query=%q chunks=%+v", tc.ID, tc.Query, chunks)
			}
			continue
		}
		for rank, chunk := range chunks {
			for _, expected := range tc.Expected {
				if chunk.DocumentID == expected {
					hits++
					reciprocalRank += 1 / float64(rank+1)
					goto nextCase
				}
			}
		}
	nextCase:
		if len(tc.Expected) > 0 && (len(chunks) == 0 || !containsExpected(chunks, tc.Expected)) {
			t.Logf("MISS id=%s query=%q chunks=%+v", tc.ID, tc.Query, chunks)
		}
	}
	known := len(cases) - unknowns
	if hits != known || unknownFalsePositives != 0 {
		t.Fatalf("hybrid eval failed: hits=%d/%d unknownFalsePositives=%d", hits, known, unknownFalsePositives)
	}
	t.Logf("RAG_HYBRID_EVAL cases=%d recallAt3=%.1f%% mrr=%.3f unknownFalsePositiveRate=%.1f%%", len(cases), float64(hits)*100/float64(known), reciprocalRank/float64(known), float64(unknownFalsePositives)*100/float64(unknowns))
	lexicalHits, lexicalUnknownFP := evaluateRetriever(t, &LexicalRetriever{documents: defaultKnowledgeDocuments()}, cases)
	t.Logf("RAG_RETRIEVAL_COMPARISON lexicalRecallAt3=%.1f%% hybridRecallAt3=%.1f%% lexicalUnknownFalsePositiveRate=%.1f%%", float64(lexicalHits)*100/float64(known), float64(hits)*100/float64(known), float64(lexicalUnknownFP)*100/float64(unknowns))
}

func evaluateRetriever(t *testing.T, retriever KnowledgeRetriever, cases []ragEvalCase) (int, int) {
	hits, unknownFP := 0, 0
	for _, tc := range cases {
		chunks, err := retriever.Retrieve(context.Background(), tc.Query, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(tc.Expected) == 0 {
			if len(chunks) > 0 {
				unknownFP++
			}
			continue
		}
		if containsExpected(chunks, tc.Expected) {
			hits++
		}
	}
	return hits, unknownFP
}

func containsExpected(chunks []KnowledgeChunk, expected []string) bool {
	for _, chunk := range chunks {
		for _, id := range expected {
			if chunk.DocumentID == id {
				return true
			}
		}
	}
	return false
}
