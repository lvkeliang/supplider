package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// TR-19 补充: AI 档案摘要 — one-paragraph summary gated on a configured provider.

func TestAISummarizeUnconfiguredReturns503(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/ai/summarize/sup_2026_000001", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestAISummarizeNotFoundReturns404(t *testing.T) {
	upstream := compareUpstream(t, "")
	defer upstream.Close()
	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "k",
		Model:   "m",
	}, nil))

	w := do(t, s, http.MethodPost, "/api/v1/ai/summarize/sup_2026_999999", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
}

func TestAISummarizeGeneratesSummary(t *testing.T) {
	upstream := compareUpstream(t, "该杭州市政总承包公司主营市政工程施工，具备一级总承包资质，综合评分高，暂无风险。**概括·信任等级：可信**")
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	}, nil))

	id := createSupplierGetID(t, s, "杭州市政总承包有限公司")

	w := do(t, s, http.MethodPost, "/api/v1/ai/summarize/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp aiSummaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Summary == "" || !strings.Contains(resp.Summary, "可信") {
		t.Fatalf("summary = %q", resp.Summary)
	}
}
