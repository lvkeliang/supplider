package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/redact"
)

// AI 比价摘要 (PRD 3.5「比价：多报价方案对比摘要与推荐」): the user has
// already built a manual comparison (list ✓ → CompareView) and an AI button
// turns that into a prose recommendation. It is an ENHANCEMENT over the
// non-AI comparison table — no provider configured → 503 and the button is
// hidden (AI 原生但可降级).

const maxCompareIDs = 10

// aiCompareRequest is the POST /ai/compare body.
type aiCompareRequest struct {
	IDs []string `json:"ids"`
}

// aiCompareResponse is the generated comparison summary (Markdown text).
type aiCompareResponse struct {
	Summary string `json:"summary"`
}

const comparePrompt = `你是采购比价选型助手。下面是要对比的多家供应商档案。请用中文给出简洁分析：
1. 各家优劣势——资质等级、价格区间、评分（交付/质量/配合度）、合作项目数、风险提示
2. 横向对比要点（价格、资质、信誉）
3. 最终采购推荐——明确写出推荐哪家、原因
用简洁 Markdown 分点列出，重点突出，控制在 300 字内。只基于档案中有的数据，不要编造。`

// handleAICompare summarizes a set of suppliers for a selection decision.
func (s *Server) handleAICompare(w http.ResponseWriter, r *http.Request) {
	gw := s.liveGateway()
	if gw == nil || !gw.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "AI 模型未配置，请先在设置中配置模型")
		return
	}

	var req aiCompareRequest
	if !decodeOptionalJSONBody(w, r, 1<<16, &req) {
		return
	}
	if len(req.IDs) < 2 {
		writeError(w, http.StatusBadRequest, "请至少选择两家供应商进行对比")
		return
	}
	if len(req.IDs) > maxCompareIDs {
		req.IDs = req.IDs[:maxCompareIDs]
	}

	profile, err := s.buildCompareProfile(r.Context(), req.IDs)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if strings.TrimSpace(profile) == "" {
		writeError(w, http.StatusBadRequest, "所选的供应商均不存在")
		return
	}

	resp, err := gw.Complete(r.Context(), aigateway.ChatRequest{
		Task: "compare",
		Messages: []aigateway.ChatMessage{
			{Role: "system", Content: comparePrompt},
			{Role: "user", Content: profile},
		},
	})
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AI 模型未配置")
			return
		}
		writeError(w, http.StatusBadGateway, "比价分析失败："+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, aiCompareResponse{Summary: strings.TrimSpace(resp.Text)})
}

// buildCompareProfile renders a compact, human-readable profile of each
// selected supplier for the LLM. Missing ids are skipped (best-effort).
func (s *Server) buildCompareProfile(ctx context.Context, ids []string) (string, error) {
	var b strings.Builder
	for i, id := range ids {
		doc, err := s.Service.Get(ctx, id)
		if err != nil {
			if errors.Is(err, datamodel.ErrNotFound) {
				continue
			}
			return "", err
		}
		fmt.Fprintf(&b, "%d. %s", i+1, doc.BasicInfo.CompanyName)
		region := []string{doc.BasicInfo.Region.Province, doc.BasicInfo.Region.City, doc.BasicInfo.Region.District}
		if reg := strings.Join(nonEmpty(region), ""); reg != "" {
			b.WriteString("（" + reg + "）")
		}
		b.WriteByte('\n')

		if q := qualSummary(doc); q != "" {
			fmt.Fprintf(&b, "   资质：%s\n", q)
		}
		if len(doc.Categories) > 0 {
			fmt.Fprintf(&b, "   品类：%s\n", strings.Join(doc.Categories, "、"))
		}
		if doc.Rating > 0 {
			fmt.Fprintf(&b, "   评分：%.2f（交付 %.2f / 质量 %.2f / 配合度 %.2f）\n", doc.Rating, dimAvg(doc.PerformanceHistory, func(p domain.Performance) float64 { return p.Delivery }), dimAvg(doc.PerformanceHistory, func(p domain.Performance) float64 { return p.Quality }), dimAvg(doc.PerformanceHistory, func(p domain.Performance) float64 { return p.Cooperation }))
		}
		if prices := priceSummary(doc); prices != "" {
			fmt.Fprintf(&b, "   价格区间：%s\n", prices)
		}
		if len(doc.PerformanceHistory) > 0 {
			fmt.Fprintf(&b, "   合作项目：%d 个\n", len(doc.PerformanceHistory))
		}
		if risks := riskSummary(doc); risks != "" {
			fmt.Fprintf(&b, "   风险提示：%s\n", risks)
		}
	}
	// AI 云端请求脱敏: scrub contact PII before the archive leaves for the LLM.
	return redact.Text(b.String()), nil
}

func qualSummary(d *domain.Supplier) string {
	parts := make([]string, 0, len(d.Qualifications))
	for _, q := range d.Qualifications {
		parts = append(parts, strings.TrimSpace(q.Type+" "+q.Level))
	}
	return strings.Join(parts, "；")
}

func priceSummary(d *domain.Supplier) string {
	parts := make([]string, 0, len(d.ProductsServices))
	for _, p := range d.ProductsServices {
		if strings.TrimSpace(p.UnitPriceRange) == "" {
			continue
		}
		parts = append(parts, p.Name+":"+p.UnitPriceRange)
	}
	return strings.Join(parts, "；")
}

func riskSummary(d *domain.Supplier) string {
	var parts []string
	if d.Status == domain.StatusBlacklisted {
		parts = append(parts, "黑名单")
	}
	if d.RiskFlags.ShellRisk {
		parts = append(parts, "空壳风险")
	}
	return strings.Join(parts, "、")
}

// dimAvg is the mean of one performance sub-score over the records that set it
// (0 = unset → excluded), matching the frontend CompareView.
func dimAvg(ps []domain.Performance, pick func(domain.Performance) float64) float64 {
	var sum float64
	n := 0
	for _, p := range ps {
		v := pick(p)
		if v > 0 {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
