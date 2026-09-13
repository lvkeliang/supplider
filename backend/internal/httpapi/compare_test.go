package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// TR-19 补充: AI 比价摘要 — compromised only when a provider is configured.

func createSupplierGetID(t *testing.T, s *Server, name string) string {
	t.Helper()
	body := `{"basic_info":{"company_name":"` + name + `","region":{"province":"浙江","city":"杭州"}}}`
	w := do(t, s, http.MethodPost, "/api/v1/suppliers", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %s: status %d body=%s", name, w.Code, w.Body.String())
	}
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID == "" {
		t.Fatalf("create %s returned no id", name)
	}
	return doc.ID
}

func compareUpstream(t *testing.T, summary string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		content, _ := json.Marshal(summary)
		w.Write([]byte(`{"choices":[{"message":{"content":` + string(content) + `}}]}`))
	}))
}

func TestAICompareUnconfiguredReturns503(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/ai/compare", `{"ids":["a","b"]}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestAICompareRequiresAtLeastTwo(t *testing.T) {
	upstream := compareUpstream(t, "")
	defer upstream.Close()
	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "k",
		Model:   "m",
	}, nil))

	w := do(t, s, http.MethodPost, "/api/v1/ai/compare", `{"ids":["a"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}

func TestAICompareSummarizes(t *testing.T) {
	upstream := compareUpstream(t, "**推荐 A 公司**：资质二级、价格更低、评分更高。")
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	}, nil))

	// One supplier with full data, one minimal — both in the profile.
	body := `{"category":"市政工程","qualifications":[{"type":"建筑工程施工总承包","level":"一级"}]}`
	a := createSupplierGetID(t, s, "杭州市政总承包有限公司")
	w := do(t, s, http.MethodPatch, "/api/v1/suppliers/"+a, body)
	if w.Code != http.StatusOK {
		t.Fatalf("patch A status %d body=%s", w.Code, w.Body.String())
	}
	b := createSupplierGetID(t, s, "杭州XX市政贸易有限公司")

	w = do(t, s, http.MethodPost, "/api/v1/ai/compare", `{"ids":["`+a+`","`+b+`","missing_000"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp aiCompareResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// The missing id is skipped but the two existing suppliers are analyzed.
	if resp.Summary == "" || !strings.HasPrefix(resp.Summary, "**推荐 A 公司**") {
		t.Fatalf("summary = %q", resp.Summary)
	}
}
