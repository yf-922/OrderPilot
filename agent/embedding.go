package agent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type Embedder interface {
	Embed(context.Context, string) ([]float32, error)
}

type OpenAICompatibleEmbedder struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
}

func (e *OpenAICompatibleEmbedder) Embed(ctx context.Context, input string) ([]float32, error) {
	body, err := json.Marshal(map[string]any{"model": e.Model, "input": input})
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(e.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/embeddings") {
		endpoint += "/embeddings"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding provider returned HTTP %d", resp.StatusCode)
	}
	var output struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&output); err != nil {
		return nil, err
	}
	if len(output.Data) == 0 || len(output.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding provider returned no vector")
	}
	for _, value := range output.Data[0].Embedding {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, fmt.Errorf("embedding contains non-finite value")
		}
	}
	return output.Data[0].Embedding, nil
}

type PgvectorRetriever struct {
	DB       *sql.DB
	Embedder Embedder
}

type PgvectorIndexer struct {
	DB       *sql.DB
	Embedder Embedder
}

func (i *PgvectorIndexer) UpsertDocument(ctx context.Context, document KnowledgeDocument) error {
	if i.DB == nil || i.Embedder == nil {
		return fmt.Errorf("pgvector indexer is not configured")
	}
	chunks := splitKnowledgeDocument(document.Content, 360)
	if len(chunks) == 0 {
		return fmt.Errorf("document is empty")
	}
	vectors := make([][]float32, len(chunks))
	for index, content := range chunks {
		vector, err := i.Embedder.Embed(ctx, document.Title+" "+content)
		if err != nil {
			return err
		}
		vectors[index] = vector
	}
	tx, err := i.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, document.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE left(document_id,length($1)+1)=$1 || '#'`, document.ID); err != nil {
		return err
	}
	for index, content := range chunks {
		_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_chunks(document_id,title,category,content,embedding,metadata) VALUES($1,$2,$3,$4,$5::vector,$6::jsonb)`, document.ID+fmt.Sprintf("#%d", index+1), document.Title, document.Category, content, vectorLiteral(vectors[index]), `{}`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func splitKnowledgeDocument(content string, size int) []string {
	if size <= 0 {
		size = 360
	}
	runes := []rune(content)
	chunks := make([]string, 0, (len(runes)+size-1)/size)
	for len(runes) > 0 {
		end := size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[:end]))
		runes = runes[end:]
	}
	return chunks
}

func (r *PgvectorRetriever) Retrieve(ctx context.Context, query string, topK int) ([]KnowledgeChunk, error) {
	if r.DB == nil || r.Embedder == nil {
		return nil, fmt.Errorf("pgvector retriever is not configured")
	}
	if topK <= 0 {
		topK = 3
	}
	vector, err := r.Embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT document_id,title,category,content,1-(embedding <=> $1::vector) AS score FROM knowledge_chunks ORDER BY embedding <=> $1::vector LIMIT $2`, vectorLiteral(vector), topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]KnowledgeChunk, 0, topK)
	for rows.Next() {
		var chunk KnowledgeChunk
		if err := rows.Scan(&chunk.DocumentID, &chunk.Title, &chunk.Category, &chunk.Content, &chunk.Score); err != nil {
			return nil, err
		}
		results = append(results, chunk)
	}
	return results, rows.Err()
}

func vectorLiteral(vector []float32) string {
	parts := make([]string, len(vector))
	for i, value := range vector {
		parts[i] = fmt.Sprintf("%g", value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
