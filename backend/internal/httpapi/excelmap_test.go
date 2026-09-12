package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// TR-19-C: Excel 智能列映射 endpoint — the model's mapping is returned and
// filtered to valid column indices + field keys.

func TestExcelMapUnconfiguredReturnsServiceUnavailable(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/ai/excel-map", `{"headers":["名称"]}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestExcelMapReturnsFilteredMapping(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Model hallucinates one bad index (9) and one bad key ("evil").
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"0\":\"company_name\",\"1\":\"credit_code\",\"9\":\"address\",\"2\":\"evil\"}"}}]}`))
	}))
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "k",
		Model:   "m",
	}, nil))

	w := do(t, s, http.MethodPost, "/api/v1/ai/excel-map",
		`{"headers":["名称","信用代码","备注"],"sample":[["杭州宏远","91330106MA2HXY7K4B","x"]]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp aiExcelMapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// Valid entries kept; index 9 (out of range) and key "evil" dropped.
	if resp.Mapping["0"] != "company_name" || resp.Mapping["1"] != "credit_code" {
		t.Errorf("mapping = %v", resp.Mapping)
	}
	if _, ok := resp.Mapping["9"]; ok {
		t.Errorf("out-of-range index 9 should be dropped: %v", resp.Mapping)
	}
	if _, ok := resp.Mapping["2"]; ok {
		t.Errorf("invalid key should be dropped: %v", resp.Mapping)
	}
}

func TestExcelMapRejectsEmptyHeaders(t *testing.T) {
	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: "http://127.0.0.1:9",
		APIKey:  "k",
		Model:   "m",
	}, nil))
	w := do(t, s, http.MethodPost, "/api/v1/ai/excel-map", `{"headers":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}

func TestBuildExcelMapPromptIncludesCatalogAndHeaders(t *testing.T) {
	p := buildExcelMapPrompt([]string{"名称", "信用代码"}, [][]string{{"杭州宏远", "9133"}})
	for _, want := range []string{"company_name", "credit_code", "[0] 名称", "[1] 信用代码", "杭州宏远"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}
