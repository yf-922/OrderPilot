package agent

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"
)

func TestExportAblationDocuments(t *testing.T) {
	if os.Getenv("RAG_ABLATION_EXPORT") != "1" {
		t.Skip("explicit export only")
	}
	data, err := json.Marshal(defaultKnowledgeDocuments())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile("testdata/rag_ablation_documents.json", data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRAGAblation(t *testing.T) {
	data, err := os.ReadFile("testdata/rag_ablation_vectors.json")
	if os.IsNotExist(err) {
		t.Skip("run prepare_rag_ablation.py first")
	}
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Model   string      `json:"model"`
		Docs    [][]float64 `json:"documentVectors"`
		Queries [][]float64 `json:"queryVectors"`
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("testdata/rag_eval.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []ragEvalCase
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	docs := defaultKnowledgeDocuments()
	if len(vectors.Docs) != len(docs) || len(vectors.Queries) != len(cases) {
		t.Fatal("vector corpus mismatch")
	}
	type metric struct {
		Recall        float64 `json:"recallAt3"`
		MRR           float64 `json:"mrr"`
		FalsePositive float64 `json:"unknownFalsePositiveRate"`
		Known         int     `json:"known"`
		Unknown       int     `json:"unknown"`
	}
	metrics := map[string]*metric{"keyword": {}, "vector": {}, "rrf": {}}
	details := []map[string]any{}
	for i, c := range cases {
		lexical, err := NewLexicalRetriever(docs).Retrieve(context.Background(), c.Query, 9)
		if err != nil {
			t.Fatal(err)
		}
		dense := []KnowledgeChunk{}
		for j, d := range docs {
			score := 0.0
			if len(vectors.Queries[i]) != len(vectors.Docs[j]) {
				t.Fatal("dimension mismatch")
			}
			for k, v := range vectors.Docs[j] {
				score += v * vectors.Queries[i][k]
			}
			dense = append(dense, KnowledgeChunk{DocumentID: d.ID, Score: score})
		}
		sort.Slice(dense, func(a, b int) bool {
			if dense[a].Score == dense[b].Score {
				return dense[a].DocumentID < dense[b].DocumentID
			}
			return dense[a].Score > dense[b].Score
		})
		rankings := map[string][]KnowledgeChunk{"keyword": lexical, "vector": dense, "rrf": fuseRanks(3, lexical, dense)}
		row := map[string]any{"id": c.ID, "query": c.Query, "expected": c.Expected}
		for name, hits := range rankings {
			if len(hits) > 3 {
				hits = hits[:3]
			}
			row[name] = hits
			m := metrics[name]
			if len(c.Expected) == 0 {
				m.Unknown++
				if len(hits) > 0 {
					m.FalsePositive++
				}
				continue
			}
			m.Known++
			covered := map[string]bool{}
			rr := 0.0
			for rank, h := range hits {
				for _, expected := range c.Expected {
					if h.DocumentID == expected {
						covered[expected] = true
						if rr == 0 {
							rr = 1 / float64(rank+1)
						}
					}
				}
			}
			m.Recall += float64(len(covered)) / float64(len(c.Expected))
			m.MRR += rr
		}
		details = append(details, row)
	}
	for _, m := range metrics {
		m.Recall /= float64(m.Known)
		m.MRR /= float64(m.Known)
		m.FalsePositive /= float64(m.Unknown)
	}
	report := map[string]any{"model": vectors.Model, "cases": len(cases), "documents": len(docs), "rrfK": 60, "topK": 3, "metrics": metrics, "results": details, "vectorThreshold": "none; matches production vector ranking"}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("RAG_ABLATION_REPORT"); path != "" {
		if err = os.WriteFile(path, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	summary, _ := json.Marshal(metrics)
	t.Logf("RAG_ABLATION model=%s metrics=%s", vectors.Model, summary)
}
