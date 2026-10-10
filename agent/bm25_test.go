package agent

import (
	"strings"
	"testing"
)

func TestBM25AndRuleBoundaries(t *testing.T) {
	docs := policyDocuments()
	if len(docs) != 32 {
		t.Fatal(len(docs))
	}
	chunks := policyChunks(docs)
	for _, c := range chunks {
		for _, field := range []string{"适用条件：", "处理规则：", "例外情况：", "来源："} {
			if !strings.Contains(c.Content, field) {
				t.Fatalf("lost %s in %s", field, c.DocumentID)
			}
		}
	}
	for i, d := range docs {
		parts := strings.Split(d.Content, "。")
		got := bm25Rank(parts[0], chunks, 1)
		if len(got) != 1 || got[0].DocumentID != chunks[i].DocumentID {
			t.Fatalf("identifier %s: %v", parts[0], got)
		}
	}
	if len(bm25Rank("zzzz-999", chunks, 3)) != 0 {
		t.Fatal("unknown ASCII identifier matched")
	}
}
