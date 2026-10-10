package agent

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// RRF combines ranks, not incomparable lexical and cosine scores.
type RRFRetriever struct{ Lexical, Vector KnowledgeRetriever }

func (r *RRFRetriever) Retrieve(ctx context.Context, query string, topK int) ([]KnowledgeChunk, error) {
	if topK <= 0 {
		topK = 3
	}
	if topK > 100 {
		topK = 100
	}
	type result struct {
		chunks []KnowledgeChunk
		err    error
	}
	a, b := make(chan result, 1), make(chan result, 1)
	fetch := func(source KnowledgeRetriever, ch chan result) {
		v, e := source.Retrieve(ctx, query, topK*3)
		ch <- result{v, e}
	}
	go fetch(r.Lexical, a)
	go fetch(r.Vector, b)
	var x, y result
	select {
	case x = <-a:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case y = <-b:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if x.err != nil && y.err != nil {
		return nil, fmt.Errorf("both retrieval branches failed")
	}
	if x.err != nil {
		x.chunks = nil
	}
	if y.err != nil {
		y.chunks = nil
	}
	return fuseRanks(topK, x.chunks, y.chunks), nil
}

func fuseRanks(topK int, lists ...[]KnowledgeChunk) []KnowledgeChunk {
	items := map[string]KnowledgeChunk{}
	for _, list := range lists {
		seen := map[string]bool{}
		for index, item := range list {
			if seen[item.DocumentID] {
				continue
			}
			seen[item.DocumentID] = true
			previous, exists := items[item.DocumentID]
			if !exists {
				previous = item
				previous.Score = 0
			}
			previous.Score += 1 / float64(60+index+1)
			items[item.DocumentID] = previous
		}
	}
	out := make([]KnowledgeChunk, 0, len(items))
	for _, v := range items {
		out = append(out, v)
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

// Keyword retrieval uses the same persisted chunks and IDs as vector search.
// This scan is deliberately bounded to a small policy knowledge base.
type PgKeywordRetriever struct{ DB *sql.DB }

func (r *PgKeywordRetriever) Retrieve(ctx context.Context, query string, topK int) ([]KnowledgeChunk, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT document_id,title,category,content FROM knowledge_chunks ORDER BY document_id LIMIT 10001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnowledgeChunk{}
	count := 0
	for rows.Next() {
		var c KnowledgeChunk
		if err = rows.Scan(&c.DocumentID, &c.Title, &c.Category, &c.Content); err != nil {
			return nil, err
		}
		count++
		if count > 10000 {
			return nil, fmt.Errorf("keyword corpus exceeds 10000 chunks")
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return bm25Rank(query, out, topK), nil
}
