package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type rankedFixture struct {
	items []KnowledgeChunk
	fail  bool
}

func (r rankedFixture) Retrieve(context.Context, string, int) ([]KnowledgeChunk, error) {
	if r.fail {
		return nil, errors.New("unavailable")
	}
	return r.items, nil
}
func TestRRFRankingAndDegradation(t *testing.T) {
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("policy-%d", i)
		a := rankedFixture{items: []KnowledgeChunk{{DocumentID: "lexical-only", Score: 999}, {DocumentID: id, Score: 1}, {DocumentID: id}}}
		b := rankedFixture{items: []KnowledgeChunk{{DocumentID: "vector-only", Score: 0.99}, {DocumentID: id, Score: 0.01}}}
		r := &RRFRetriever{Lexical: a, Vector: b}
		got, err := r.Retrieve(context.Background(), "query", 3)
		if err != nil || len(got) != 3 || got[0].DocumentID != id {
			t.Fatalf("case %d: %v %v", i, got, err)
		}
		r.Vector = rankedFixture{fail: true}
		got, err = r.Retrieve(context.Background(), "query", 1)
		if err != nil || len(got) != 1 || got[0].DocumentID != "lexical-only" {
			t.Fatal(got, err)
		}
	}
	t.Log("30 controlled rank cases: lexical top-1 0/30, vector top-1 0/30, RRF top-1 30/30; algorithm fixture only")
	r := &RRFRetriever{Lexical: rankedFixture{fail: true}, Vector: rankedFixture{fail: true}}
	if _, err := r.Retrieve(context.Background(), "query", 3); err == nil {
		t.Fatal("both failures hidden")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Retrieve(ctx, "query", 3); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
