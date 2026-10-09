package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type vectorFixture struct{}

func (vectorFixture) Embed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, 1536)
	if strings.Contains(text, "退款") {
		v[0] = 1
	} else {
		v[1] = 1
	}
	return v, nil
}

func TestPgvectorDatabaseRoundTrip(t *testing.T) {
	dsn := os.Getenv("PGVECTOR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGVECTOR_TEST_DSN required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schemaName := fmt.Sprintf("agent_eval_%d", time.Now().UnixNano())
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	if _, err = db.ExecContext(ctx, "SET search_path TO "+schemaName+",public"); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../db/rag_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, _ := (vectorFixture{}).Embed(r.Context(), body.Input)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": v}}})
	}))
	defer server.Close()
	embedder := &OpenAICompatibleEmbedder{BaseURL: server.URL, Model: "integration-fixture"}
	i := &PgvectorIndexer{DB: db, Embedder: embedder}
	refund := KnowledgeDocument{ID: "fixture-refund", Title: "退款", Content: strings.Repeat("退款规则", 100)}
	for n := 0; n < 2; n++ {
		if err = i.UpsertDocument(ctx, refund); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_chunks").Scan(&count); err != nil || count != 2 {
		t.Fatalf("duplicate chunks count=%d err=%v", count, err)
	}
	refund.Content = "退款到账以支付渠道为准"
	if err = i.UpsertDocument(ctx, refund); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_chunks").Scan(&count); err != nil || count != 1 {
		t.Fatalf("stale chunks count=%d err=%v", count, err)
	}
	if err = i.UpsertDocument(ctx, KnowledgeDocument{ID: "fixture-delivery", Title: "配送", Content: "配送时效取决于承运商"}); err != nil {
		t.Fatal(err)
	}
	r := &PgvectorRetriever{DB: db, Embedder: embedder}
	for n := 0; n < 30; n++ {
		chunks, err := r.Retrieve(ctx, "退款什么时候到账", 1)
		if err != nil || len(chunks) != 1 || chunks[0].DocumentID != "fixture-refund#1" || chunks[0].Content != refund.Content {
			t.Fatalf("round=%d chunks=%+v err=%v", n, chunks, err)
		}
	}
	t.Log("HTTP embedding -> transactional indexing -> pgvector retrieval: 30/30 source assertions passed")
}
