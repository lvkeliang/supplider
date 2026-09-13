package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/redact"
)

// AI 档案摘要 (PRD 3.5「维护：供应商档案一段话摘要」): turn a supplier's
// archive into a one-paragraph, third-party-view summary (定位/资质/信誉/风险)
// for a quick read before engaging. It is an ENHANCEMENT over reading the
// document cards — no provider configured → 503 and the button is hidden
// (AI 原生但可降级).

// aiSummaryResponse is the generated one-paragraph summary.
type aiSummaryResponse struct {
	Summary string `json:"summary"`
}

const summarizePrompt = `你是供应商档案摘要助手。根据以下供应商档案信息，用中文生成一段 2-3 句的第三方视角摘要：概括公司定位与主营、资质与信誉（评分/合作）、风险提示（如有）。开头直接概述，结尾另起一行以 **概括·信任等级：** 标注（可信/需核实/高风险，依据黑名单、空壳风险、评分等）。只基于档案中有的数据，不要编造。`

// handleAISummarize generates a one-paragraph summary of a supplier archive.
func (s *Server) handleAISummarize(w http.ResponseWriter, r *http.Request) {
	gw := s.liveGateway()
	if gw == nil || !gw.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "AI 模型未配置，请先在设置中配置模型")
		return
	}

	id := r.PathValue("id")
	doc, err := s.Service.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, datamodel.ErrNotFound) {
			writeError(w, http.StatusNotFound, "供应商不存在")
			return
		}
		writeServiceError(w, err)
		return
	}

	resp, err := gw.Complete(r.Context(), aigateway.ChatRequest{
		Task: "summarize",
		Messages: []aigateway.ChatMessage{
			{Role: "system", Content: summarizePrompt},
			{Role: "user", Content: summarizeProfile(doc)},
		},
	})
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AI 模型未配置")
			return
		}
		writeError(w, http.StatusBadGateway, "档案摘要失败："+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, aiSummaryResponse{Summary: strings.TrimSpace(resp.Text)})
}

// summarizeProfile flattens the fields an LLM needs for a one-paragraph
// summary. Empty fields are omitted so the model never invents data.
func summarizeProfile(d *domain.Supplier) string {
	var b strings.Builder
	b.WriteString("公司：" + d.BasicInfo.CompanyName + "\n")
	region := strings.Join(nonEmpty([]string{d.BasicInfo.Region.Province, d.BasicInfo.Region.City, d.BasicInfo.Region.District}), " ")
	if region != "" {
		b.WriteString("地域：" + region + "\n")
	}
	if v := d.BasicInfo.SupplierType; v != "" {
		b.WriteString("供应商类型：" + v + "\n")
	}
	if v := d.BasicInfo.CreditCode; v != "" {
		b.WriteString("统一社会信用代码：" + v + "\n")
	}
	if v := d.BasicInfo.LegalPerson; v != "" {
		b.WriteString("法定代表人：" + v + "\n")
	}
	if v := d.BasicInfo.EstablishmentDate; v != "" {
		b.WriteString("成立日期：" + v + "\n")
	}
	if v := d.BasicInfo.RegisteredCapital; v != "" {
		b.WriteString("注册资本：" + v + "\n")
	}
	if v := d.BasicInfo.BusinessScope; v != "" {
		b.WriteString("经营范围：" + v + "\n")
	}
	if q := qualSummary(d); q != "" {
		b.WriteString("资质：" + q + "\n")
	}
	if len(d.Categories) > 0 {
		b.WriteString("品类：" + strings.Join(d.Categories, "、") + "\n")
	}
	if d.Rating > 0 {
		fmt.Fprintf(&b, "评分：%.2f（交付 %.2f / 质量 %.2f / 配合度 %.2f）\n", d.Rating, dimAvg(d.PerformanceHistory, func(p domain.Performance) float64 { return p.Delivery }), dimAvg(d.PerformanceHistory, func(p domain.Performance) float64 { return p.Quality }), dimAvg(d.PerformanceHistory, func(p domain.Performance) float64 { return p.Cooperation }))
	}
	if len(d.PerformanceHistory) > 0 {
		b.WriteString(fmt.Sprintf("合作项目：%d 个\n", len(d.PerformanceHistory)))
	}
	if prices := priceSummary(d); prices != "" {
		b.WriteString("主营/价格：" + prices + "\n")
	}
	if d.BlacklistReason != "" {
		b.WriteString("黑名单原因：" + d.BlacklistReason + "\n")
	}
	if d.RiskFlags.ShellRisk {
		b.WriteString("风险提示：空壳风险\n")
	}
	// AI 云端请求脱敏: scrub contact PII (phone/email/信用代码) before the
	// archive leaves the machine for a cloud LLM.
	return redact.Text(b.String())
}
