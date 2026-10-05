package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleEmbedderContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer embed-key" {
			t.Fatalf("unexpected embedding request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "embed-test" || body["input"] != "地址修改规则" {
			t.Fatalf("unexpected body: %+v", body)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer server.Close()
	vector, err := (&OpenAICompatibleEmbedder{BaseURL: server.URL + "/v1", APIKey: "embed-key", Model: "embed-test"}).Embed(context.Background(), "地址修改规则")
	if err != nil || len(vector) != 3 {
		t.Fatalf("embedding failed: %v %v", vector, err)
	}
}

func TestVectorLiteralIsPgvectorCompatible(t *testing.T) {
	if got := vectorLiteral([]float32{0.1, -0.25, 1}); got != "[0.1,-0.25,1]" {
		t.Fatalf("vector literal=%q", got)
	}
}

func TestKnowledgeDocumentChunkingUsesBoundedChunks(t *testing.T) {
	chunks := splitKnowledgeDocument("一二三四五六七八九十", 4)
	if len(chunks) != 3 || chunks[0] != "一二三四" || chunks[2] != "九十" {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}
