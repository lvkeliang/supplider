package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// TR-19-D: 自然语言搜索 endpoint maps an NL query to a structured filter.

func TestNLSearchUnconfiguredReturnsServiceUnavailable(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/ai/nl-search", `{"query":"杭州本地市政"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestNLSearchParsesQueryToFilter(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"province\":\"浙江\",\"city\":\"杭州\",\"category\":\"市政工程\",\"min_qual_level\":\"二级\",\"min_rating\":4}"}}]}`))
	}))
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "k",
		Model:   "m",
	}, nil))

	w := do(t, s, http.MethodPost, "/api/v1/ai/nl-search", `{"query":"杭州本地能做市政工程的二级资质以上供应商"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var out aiNLSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Province != "浙江" || out.City != "杭州" || out.Category != "市政工程" {
		t.Errorf("filter = %+v", out)
	}
	if out.MinQualLevel != "二级" || out.MinRating != 4 {
		t.Errorf("qual/rating = %+v", out)
	}
}

func TestNLSearchRejectsUnknownQualLevel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"min_qual_level\":\"肆级\"}"}}]}`))
	}))
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "k",
		Model:   "m",
	}, nil))

	w := do(t, s, http.MethodPost, "/api/v1/ai/nl-search", `{"query":"肆级资质"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502 (body=%s)", w.Code, w.Body.String())
	}
}

func TestNLSearchRejectsEmptyQuery(t *testing.T) {
	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: "http://127.0.0.1:9",
		APIKey:  "k",
		Model:   "m",
	}, nil))
	w := do(t, s, http.MethodPost, "/api/v1/ai/nl-search", `{"query":"   "}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}
