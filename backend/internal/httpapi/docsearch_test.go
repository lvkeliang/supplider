package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/supplider/supplider/backend/internal/aigateway"
	vsmem "github.com/supplider/supplider/backend/internal/vectorstore/memory"
)

// TR-19-E: 文档分析搜索 — requirement extraction → semantic match → hard
// filters → ranked recommendations.

// docSearchUpstream serves both the /chat/completions (requirement extraction)
// and /embeddings (text → deterministic vector) routes.
func docSearchUpstream(t *testing.T, extractJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat/completions":
			content, _ := json.Marshal(extractJSON)
			w.Write([]byte(`{"choices":[{"message":{"content":` + string(content) + `}}]}`))
		case "/embeddings":
			var body struct {
				Input []string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			data := make([]map[string]any, 0, len(body.Input))
			for i, in := range body.Input {
				data = append(data, map[string]any{"index": i, "embedding": testVec(in)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			http.NotFound(w, r)
		}
	}))
}

// testVec derives a 64-dim vector from text so texts sharing runes rank closer
// (deterministic, no network).
func testVec(text string) []float32 {
	v := make([]float32, 64)
	for _, r := range text {
		h := uint32(r) * 2654435761
		idx := h % 64
		if h&(1<<31) != 0 {
			v[idx]++
		} else {
			v[idx]--
		}
	}
	return v
}

func docSearchGateway(t *testing.T, upstream *httptest.Server) *aigateway.Service {
	t.Helper()
	return aigateway.New(aigateway.Config{
		Format:         aigateway.FormatOpenAI,
		BaseURL:        upstream.URL,
		APIKey:         "sk-test",
		Model:          "deepseek-chat",
		EmbeddingModel: "text-embedding-3-small",
	}, nil)
}

// createFullSupplier posts a supplier with region, categories and a
// qualification so hard filters have something to test against.
func createFullSupplier(t *testing.T, s *Server, body string) {
	t.Helper()
	if w := do(t, s, http.MethodPost, "/api/v1/suppliers", body); w.Code != http.StatusCreated {
		t.Fatalf("create status %d body=%s", w.Code, w.Body.String())
	}
}

func TestDocSearchUnconfiguredReturns503(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/ai/doc-search", `{"text":"需要市政工程供应商"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestClipDocTextBoundedAndUTF8Safe(t *testing.T) {
	// Short text passes through unchanged.
	if got := clipDocText("需要杭州市政工程供应商"); got != "需要杭州市政工程供应商" {
		t.Fatalf("short text altered: %q", got)
	}
	// A long run (> max bytes and > max runes) is clipped to the rune budget,
	// still valid UTF-8.
	long := strings.Repeat("混凝土C30", 20000) // 40k runes / 100k+ bytes
	got := clipDocText(long)
	if got == long {
		t.Fatal("long text must be clipped")
	}
	if r := utf8.RuneCountInString(got); r != maxDocInputChars {
		t.Fatalf("clipped rune count = %d, want %d", r, maxDocInputChars)
	}
	if !utf8.ValidString(got) {
		t.Fatal("clipped text must be valid UTF-8")
	}
	// A big Chinese doc whose rune count is within budget is left intact
	// (bytes exceed the byte heuristic but runes don't).
	chinese := strings.Repeat("混凝土", 9000) // 27k runes, 81k bytes
	if got := clipDocText(chinese); got != chinese {
		t.Fatal("in-budget rune count must not be clipped")
	}
}

// TestDocSearchFlagRequiresEmbeddingModel pins the feature-flag split: a
// chat-only provider lights ai_enabled/ai_nl_search but NOT ai_doc_search,
// which additionally needs an embedding model (Anthropic has no /embeddings).
func TestDocSearchFlagRequiresEmbeddingModel(t *testing.T) {
	upstream := embeddingsUpstream(t)
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	}, nil))

	w := do(t, s, http.MethodGet, "/api/v1/features", "")
	var feats struct {
		AIEnabled   bool `json:"ai_enabled"`
		AINLSearch  bool `json:"ai_nl_search"`
		AIDocSearch bool `json:"ai_doc_search"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feats); err != nil {
		t.Fatal(err)
	}
	if !feats.AIEnabled || !feats.AINLSearch {
		t.Fatalf("chat features should be on: %+v", feats)
	}
	if feats.AIDocSearch {
		t.Fatalf("ai_doc_search must be false without an embedding model: %+v", feats)
	}
}

func TestDocSearchRequiresEmbeddingModel(t *testing.T) {
	upstream := docSearchUpstream(t, "")
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	}, nil))
	s.WithVectorStore(vsmem.New())

	w := do(t, s, http.MethodPost, "/api/v1/ai/doc-search", `{"text":"需要市政工程供应商"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestDocSearchRejectsEmptyDocument(t *testing.T) {
	upstream := docSearchUpstream(t, "")
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(docSearchGateway(t, upstream))
	s.WithVectorStore(vsmem.New())

	w := do(t, s, http.MethodPost, "/api/v1/ai/doc-search", `{"text":"   "}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}

func TestDocSearchReturnsMatchingSupplier(t *testing.T) {
	extract := `{"requirement":"杭州 市政工程 二级资质","province":"浙江","city":"杭州","category":"市政工程","min_qual_level":"二级"}`
	upstream := docSearchUpstream(t, extract)
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(docSearchGateway(t, upstream))
	s.WithVectorStore(vsmem.New())

	createFullSupplier(t, s, `{
		"basic_info":{"company_name":"杭州混凝土工程有限公司","region":{"province":"浙江","city":"杭州"}},
		"categories":["市政工程"],
		"qualifications":[{"type":"建筑工程施工总承包","level":"二级"}]
	}`)
	createFullSupplier(t, s, `{
		"basic_info":{"company_name":"北京建材贸易有限公司","region":{"province":"北京","city":"北京"}},
		"categories":["物资供应"]
	}`)

	if w := do(t, s, http.MethodPost, "/api/v1/ai/index", ""); w.Code != http.StatusOK {
		t.Fatalf("index status %d body=%s", w.Code, w.Body.String())
	}

	w := do(t, s, http.MethodPost, "/api/v1/ai/doc-search", `{"text":"需要在杭州找市政工程的二级资质供应商"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("doc-search status %d body=%s", w.Code, w.Body.String())
	}
	var resp docSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Requirement.Category != "市政工程" || resp.Requirement.MinQualLevel != "二级" {
		t.Fatalf("extracted requirement = %+v", resp.Requirement)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	if resp.Results[0].Name != "杭州混凝土工程有限公司" {
		t.Fatalf("top result = %q, want 杭州 supplier (results %+v)", resp.Results[0].Name, resp.Results)
	}
	if resp.Results[0].Reason == "" {
		t.Fatal("reason must not be empty")
	}
}

func TestDocSearchHardFilterDropsMismatch(t *testing.T) {
	extract := `{"requirement":"市政工程","province":"浙江","city":"杭州","category":"市政工程","min_qual_level":""}`
	upstream := docSearchUpstream(t, extract)
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(docSearchGateway(t, upstream))
	s.WithVectorStore(vsmem.New())

	// Only a non-matching supplier (wrong region + category) is indexed.
	createFullSupplier(t, s, `{
		"basic_info":{"company_name":"上海物流运输有限公司","region":{"province":"上海","city":"上海"}},
		"categories":["物流运输"]
	}`)
	if w := do(t, s, http.MethodPost, "/api/v1/ai/index", ""); w.Code != http.StatusOK {
		t.Fatalf("index status %d body=%s", w.Code, w.Body.String())
	}

	w := do(t, s, http.MethodPost, "/api/v1/ai/doc-search", `{"text":"杭州市政工程"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("doc-search status %d body=%s", w.Code, w.Body.String())
	}
	var resp docSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("results = %+v, want empty (hard filter must drop non-matching supplier)", resp.Results)
	}
}
