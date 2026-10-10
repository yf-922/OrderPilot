package agent

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type retrievalV2Case struct {
	ID       string   `json:"id"`
	Query    string   `json:"query"`
	Expected []string `json:"expected"`
	Split    string   `json:"split"`
	Kind     string   `json:"kind"`
}
type retrievalMetrics struct {
	Recall        float64 `json:"recallAt3"`
	MRR           float64 `json:"mrr"`
	FalsePositive float64 `json:"unknownFalsePositiveRate"`
	Known         int     `json:"known"`
	Unknown       int     `json:"unknown"`
}

func measureRetrieval(cases []retrievalV2Case, rankings [][]KnowledgeChunk, threshold float64, split, kind string) retrievalMetrics {
	m := retrievalMetrics{}
	for i, c := range cases {
		if c.Split != split || (kind != "" && c.Kind != kind) {
			continue
		}
		hits := rankings[i]
		if len(hits) > 0 && hits[0].Score < threshold {
			hits = nil
		}
		if len(hits) > 3 {
			hits = hits[:3]
		}
		if len(c.Expected) == 0 {
			m.Unknown++
			if len(hits) > 0 {
				m.FalsePositive++
			}
			continue
		}
		m.Known++
		seen := map[string]bool{}
		rr := 0.0
		for rank, h := range hits {
			for _, id := range c.Expected {
				if strings.Split(h.DocumentID, "#")[0] == id {
					seen[id] = true
					if rr == 0 {
						rr = 1 / float64(rank+1)
					}
				}
			}
		}
		m.Recall += float64(len(seen)) / float64(len(c.Expected))
		m.MRR += rr
	}
	if m.Known > 0 {
		m.Recall /= float64(m.Known)
		m.MRR /= float64(m.Known)
	}
	if m.Unknown > 0 {
		m.FalsePositive /= float64(m.Unknown)
	}
	return m
}

func TestRetrievalV2Ablation(t *testing.T) {
	data, err := os.ReadFile("testdata/retrieval_v2_vectors.json")
	if os.IsNotExist(err) {
		t.Skip("prepare_rag_ablation.py --v2 required")
	}
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Model     string              `json:"model"`
		Revision  string              `json:"revision"`
		Documents []KnowledgeDocument `json:"documents"`
		Cases     []retrievalV2Case   `json:"cases"`
		Docs      [][]float64         `json:"documentVectors"`
		Queries   [][]float64         `json:"queryVectors"`
	}
	if err = json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("testdata/retrieval_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var frozen []retrievalV2Case
	if err = json.Unmarshal(data, &frozen); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Cases, frozen) || !reflect.DeepEqual(v.Documents, policyDocuments()) {
		t.Fatal("stale vectors: corpus or cases changed")
	}
	chunks := policyChunks(v.Documents)
	if len(v.Docs) != len(chunks) || len(v.Queries) != len(v.Cases) {
		t.Fatal("vector count mismatch")
	}
	rankings := map[string][][]KnowledgeChunk{"bm25": {}, "vector": {}, "rrf": {}}
	details := []map[string]any{}
	for i, c := range v.Cases {
		lexical := bm25Rank(c.Query, chunks, 9)
		dense := []KnowledgeChunk{}
		for j, chunk := range chunks {
			score := 0.0
			if len(v.Queries[i]) != len(v.Docs[j]) {
				t.Fatal("dimension mismatch")
			}
			for k, value := range v.Docs[j] {
				score += value * v.Queries[i][k]
			}
			chunk.Score = score
			dense = append(dense, chunk)
		}
		sort.Slice(dense, func(a, b int) bool {
			if dense[a].Score == dense[b].Score {
				return dense[a].DocumentID < dense[b].DocumentID
			}
			return dense[a].Score > dense[b].Score
		})
		if len(dense) > 9 {
			dense = dense[:9]
		}
		fused := fuseRanks(3, lexical, dense)
		rankings["bm25"] = append(rankings["bm25"], lexical)
		rankings["vector"] = append(rankings["vector"], dense)
		rankings["rrf"] = append(rankings["rrf"], fused)
		details = append(details, map[string]any{"case": c, "bm25": lexical, "vector": dense, "rrf": fused})
	}
	summary := map[string]any{}
	for name, ranks := range rankings {
		// Fixed objective: equal weight for calibration recall and unknown rejection.
		candidates := []float64{0}
		for i, c := range v.Cases {
			if c.Split == "calibration" && len(ranks[i]) > 0 {
				candidates = append(candidates, ranks[i][0].Score+1e-9)
			}
		}
		sort.Float64s(candidates)
		bestThreshold, bestScore := 0.0, -1.0
		for _, threshold := range candidates {
			m := measureRetrieval(v.Cases, ranks, threshold, "calibration", "")
			score := (m.Recall + 1 - m.FalsePositive) / 2
			if score > bestScore {
				bestScore = score
				bestThreshold = threshold
			}
		}
		groups := map[string]retrievalMetrics{}
		for _, kind := range []string{"paraphrase", "identifier", "multi_condition", "unknown"} {
			groups[kind] = measureRetrieval(v.Cases, ranks, bestThreshold, "test", kind)
		}
		summary[name] = map[string]any{"threshold": bestThreshold, "calibration": measureRetrieval(v.Cases, ranks, bestThreshold, "calibration", ""), "rawTest": measureRetrieval(v.Cases, ranks, 0, "test", ""), "gatedTest": measureRetrieval(v.Cases, ranks, bestThreshold, "test", ""), "byKind": groups}
	}
	report := map[string]any{"model": v.Model, "revision": v.Revision, "documents": len(v.Documents), "chunks": len(chunks), "cases": len(v.Cases), "topK": 3, "candidateK": 9, "rrfK": 60, "bm25K1": 1.2, "bm25B": 0.75, "calibrationObjective": "(Recall@3 + unknown specificity)/2; ties prefer lower threshold", "summary": summary, "results": details}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("RAG_V2_REPORT"); path != "" {
		if err = os.WriteFile(path, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := json.Marshal(summary)
	t.Logf("RETRIEVAL_V2=%s", s)
}
