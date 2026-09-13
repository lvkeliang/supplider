package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
	vsmem "github.com/supplider/supplider/backend/internal/vectorstore/memory"
)

// TR-19-E: 语义搜索 — index (rebuild) + semantic-search endpoints, gated on a
// configured embedding model.

// embeddingsUpstream is a fake OpenAI /embeddings endpoint: it returns a fixed
// [1,0,0,0] vector per input text, so cosine(query, doc) is always 1.
func embeddingsUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		data := make([]map[string]any, 0, len(body.Input))
		for i := range body.Input {
			data = append(data, map[string]any{"index": i, "embedding": []float32{1, 0, 0, 0}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func embeddingGateway(t *testing.T, upstream *httptest.Server) *aigateway.Service {
	t.Helper()
	return aigateway.New(aigateway.Config{
		Format:         aigateway.FormatOpenAI,
		BaseURL:        upstream.URL,
		APIKey:         "sk-test",
		Model:          "deepseek-chat",
		EmbeddingModel: "text-embedding-3-small",
	}, nil)
}

func TestSemanticSearchUnconfiguredReturns503(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/ai/semantic-search", `{"query":"杭州混凝土"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestSemanticSearchRequiresEmbeddingModel(t *testing.T) {
	upstream := embeddingsUpstream(t)
	defer upstream.Close()

	s := newTestServer(t)
	// Gateway configured for chat but with NO embedding model → 503.
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	}, nil))
	s.WithVectorStore(vsmem.New())

	w := do(t, s, http.MethodPost, "/api/v1/ai/semantic-search", `{"query":"杭州混凝土"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestSemanticSearchRejectsEmptyQuery(t *testing.T) {
	upstream := embeddingsUpstream(t)
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(embeddingGateway(t, upstream))
	s.WithVectorStore(vsmem.New())

	w := do(t, s, http.MethodPost, "/api/v1/ai/semantic-search", `{"query":"   "}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}

func TestSemanticIndexAndSearchRoundTrip(t *testing.T) {
	upstream := embeddingsUpstream(t)
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(embeddingGateway(t, upstream))
	s.WithVectorStore(vsmem.New())
	createSupplier(t, s, "杭州混凝土工程有限公司")

	// Index the library.
	w := do(t, s, http.MethodPost, "/api/v1/ai/index", "")
	if w.Code != http.StatusOK {
		t.Fatalf("index status %d body=%s", w.Code, w.Body.String())
	}
	var idx map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &idx); err != nil {
		t.Fatal(err)
	}
	if idx["indexed"].(float64) != 1 {
		t.Fatalf("indexed = %v, want 1", idx["indexed"])
	}

	// Search returns the supplier with its summary inlined and a score.
	w = do(t, s, http.MethodPost, "/api/v1/ai/semantic-search", `{"query":"杭州混凝土"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("search status %d body=%s", w.Code, w.Body.String())
	}
	var resp semanticSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %+v, want 1", resp.Results)
	}
	if resp.Results[0].Name != "杭州混凝土工程有限公司" {
		t.Fatalf("name = %q", resp.Results[0].Name)
	}
	if resp.Results[0].Score != 1 {
		t.Fatalf("score = %v, want 1", resp.Results[0].Score)
	}
}
