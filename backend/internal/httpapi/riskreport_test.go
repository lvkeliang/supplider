package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// TR-19 补充: AI 空壳风险报告 — risk assessment layered over the local rule
// engine, gated on a configured provider.

func TestAIRiskReportUnconfiguredReturns503(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/ai/risk-report/sup_2026_000001", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestAIRiskReportNotFoundReturns404(t *testing.T) {
	upstream := compareUpstream(t, "")
	defer upstream.Close()
	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "k",
		Model:   "m",
	}, nil))

	w := do(t, s, http.MethodPost, "/api/v1/ai/risk-report/sup_2026_999999", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
}

func TestAIRiskReportGeneratesReport(t *testing.T) {
	upstream := compareUpstream(t, "**风险等级：中**。该供应商档案资料简陋且成立时间短。建议核验营业执照、资质原件与银行流水。")
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	}, nil))

	id := createSupplierGetID(t, s, "杭州某贸易有限公司")

	w := do(t, s, http.MethodPost, "/api/v1/ai/risk-report/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp aiRiskReportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Report == "" || !strings.Contains(resp.Report, "风险等级") {
		t.Fatalf("report = %q", resp.Report)
	}
}
