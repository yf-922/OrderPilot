package agent

import (
	"context"
	"errors"
	"testing"
)

type unavailableRetriever struct{}

func (unavailableRetriever) Retrieve(context.Context, string, int) ([]KnowledgeChunk, error) {
	return nil, errors.New("network timeout")
}

func TestKnowledgeFallbackAndCancellation(t *testing.T) {
	r := &FallbackRetriever{Primary: unavailableRetriever{}, Fallback: DefaultKnowledgeRetriever()}
	chunks, err := r.Retrieve(context.Background(), "退款什么时候到账", 3)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("fallback failed: %v %v", chunks, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.Retrieve(ctx, "退款", 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestKnowledgeConfiguration(t *testing.T) {
	t.Setenv("PGVECTOR_DSN", "")
	r, closeDB, err := NewKnowledgeRetrieverFromEnv(context.Background())
	if err != nil || r == nil {
		t.Fatal(err)
	}
	closeDB()
	t.Setenv("PGVECTOR_DSN", "postgres://invalid")
	t.Setenv("EMBEDDING_BASE_URL", "")
	if _, _, err = NewKnowledgeRetrieverFromEnv(context.Background()); err == nil {
		t.Fatal("incomplete configuration accepted")
	}
}
