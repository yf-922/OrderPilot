package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

//go:embed knowledge/policies_v2.json
var policyCorpus []byte

func policyDocuments() []KnowledgeDocument {
	var d []KnowledgeDocument
	if err := json.Unmarshal(policyCorpus, &d); err != nil {
		panic(err)
	}
	return d
}

// Chinese unigrams+bigrams plus complete ASCII identifiers, including hyphens.
func bm25Terms(text string) []string {
	out := []string{}
	var ascii strings.Builder
	var chinese []rune
	flush := func() {
		if ascii.Len() > 0 {
			out = append(out, strings.ToLower(ascii.String()))
			ascii.Reset()
		}
		for i, r := range chinese {
			out = append(out, string(r))
			if i+1 < len(chinese) {
				out = append(out, string(chinese[i:i+2]))
			}
		}
		chinese = nil
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			if ascii.Len() > 0 {
				flush()
			}
			chinese = append(chinese, r)
		} else if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
			if len(chinese) > 0 {
				flush()
			}
			ascii.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func bm25Rank(query string, chunks []KnowledgeChunk, topK int) []KnowledgeChunk {
	if topK <= 0 {
		topK = 3
	}
	if len(chunks) == 0 {
		return []KnowledgeChunk{}
	}
	df := map[string]int{}
	freqs := make([]map[string]int, len(chunks))
	lengths := make([]int, len(chunks))
	avg := 0.0
	for i, c := range chunks {
		terms := bm25Terms(c.Title + " " + c.Content)
		lengths[i] = len(terms)
		avg += float64(len(terms))
		freqs[i] = map[string]int{}
		for _, term := range terms {
			freqs[i][term]++
		}
		for term := range freqs[i] {
			df[term]++
		}
	}
	avg /= float64(len(chunks))
	if avg == 0 {
		return []KnowledgeChunk{}
	}
	unique := map[string]bool{}
	for _, term := range bm25Terms(query) {
		unique[term] = true
	}
	out := []KnowledgeChunk{}
	for i, c := range chunks {
		score := 0.0
		for term := range unique {
			tf := float64(freqs[i][term])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(chunks)-df[term])+0.5)/(float64(df[term])+0.5))
			score += idf * tf * 2.2 / (tf + 1.2*(0.25+0.75*float64(lengths[i])/avg))
		}
		if score > 0 {
			c.Score = score
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].DocumentID < out[j].DocumentID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

type BM25Retriever struct{ Chunks []KnowledgeChunk }

func (r *BM25Retriever) Retrieve(ctx context.Context, q string, k int) ([]KnowledgeChunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return bm25Rank(q, r.Chunks, k), nil
}

// A paragraph is a complete rule unit. It is never split inside conditions or exceptions.
func policyChunks(docs []KnowledgeDocument) []KnowledgeChunk {
	out := []KnowledgeChunk{}
	for _, d := range docs {
		for i, content := range splitRuleUnits(d.Content) {
			out = append(out, KnowledgeChunk{DocumentID: d.ID + "#" + strconv.Itoa(i+1), Title: d.Title, Category: d.Category, Content: content})
		}
	}
	return out
}
func splitRuleUnits(content string) []string {
	out := []string{}
	for _, p := range strings.Split(content, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
