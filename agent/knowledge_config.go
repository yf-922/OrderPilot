package agent

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// Configured vector retrieval fails startup rather than silently claiming an
// unavailable vector backend. With no DSN the local hybrid retriever is used.
func NewKnowledgeRetrieverFromEnv(ctx context.Context) (KnowledgeRetriever, func(), error) {
	dsn := os.Getenv("PGVECTOR_DSN")
	if dsn == "" {
		return DefaultKnowledgeRetriever(), func() {}, nil
	}
	if os.Getenv("EMBEDDING_BASE_URL") == "" || os.Getenv("EMBEDDING_MODEL") == "" {
		return nil, nil, fmt.Errorf("embedding URL and model are required with PGVECTOR_DSN")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, nil, err
	}
	closeDB := func() { _ = db.Close() }
	if err = db.PingContext(ctx); err != nil {
		closeDB()
		return nil, nil, fmt.Errorf("pgvector connection failed")
	}
	if _, err = db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE IF NOT EXISTS knowledge_chunks (
id BIGSERIAL PRIMARY KEY, document_id VARCHAR(128) NOT NULL,
title TEXT NOT NULL, category VARCHAR(64) NOT NULL, content TEXT NOT NULL,
embedding vector(1536) NOT NULL, metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE INDEX IF NOT EXISTS knowledge_chunks_embedding_hnsw ON knowledge_chunks USING hnsw (embedding vector_cosine_ops);`); err != nil {
		closeDB()
		return nil, nil, fmt.Errorf("knowledge schema initialization failed: %w", err)
	}
	e := &OpenAICompatibleEmbedder{BaseURL: os.Getenv("EMBEDDING_BASE_URL"), APIKey: os.Getenv("EMBEDDING_API_KEY"), Model: os.Getenv("EMBEDDING_MODEL")}
	indexer := &PgvectorIndexer{DB: db, Embedder: e}
	for _, document := range defaultKnowledgeDocuments() {
		if err = indexer.UpsertDocument(ctx, document); err != nil {
			closeDB()
			return nil, nil, fmt.Errorf("knowledge indexing failed: %w", err)
		}
	}
	return &FallbackRetriever{Primary: &PgvectorRetriever{DB: db, Embedder: e}, Fallback: DefaultKnowledgeRetriever()}, closeDB, nil
}

type FallbackRetriever struct{ Primary, Fallback KnowledgeRetriever }

func (r *FallbackRetriever) Retrieve(ctx context.Context, query string, topK int) ([]KnowledgeChunk, error) {
	chunks, err := r.Primary.Retrieve(ctx, query, topK)
	if err == nil {
		return chunks, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return r.Fallback.Retrieve(ctx, query, topK)
}
