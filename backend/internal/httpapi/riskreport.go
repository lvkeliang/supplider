package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/risk"
)

// AI 空壳风险报告 (PRD 3.5「风险：工商变更记录→空壳风险评估报告」): an LLM
// reads a supplier's archive + the local rule-engine signals + change history
// and writes a plain-language risk assessment with a verdict and due-diligence
// steps. It layers on the NON-AI rule engine — without a key the signals alone
// drive the shell_risk flag (AI 原生但可降级).

// aiRiskReportResponse is the generated risk assessment (Markdown text).
type aiRiskReportResponse struct {
	Report string `json:"report"`
}

const riskReportPrompt = `你是供应商风险审计专家。基于以下供应商档案、本地规则引擎信号与变更记录，用中文写一份简洁的风险评估报告：
1. 风险等级结论（低 / 中 / 高，并给出一句话理由）
2. 关键风险信号与依据（成立时间短、变更频繁、资质无效/临期冲突、无合作记录、资本过低、黑名单/空壳标记、信用代码异常等）
3. 尽职调查建议（核验哪些材料、关注什么）
用简洁 Markdown 分点，总字数 500 字内。只基于提供的数据，不要编造。`

// handleAIRiskReport generates a shell-company risk assessment for a supplier.
func (s *Server) handleAIRiskReport(w http.ResponseWriter, r *http.Request) {
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
		Task: "risk_report",
		Messages: []aigateway.ChatMessage{
			{Role: "system", Content: riskReportPrompt},
			{Role: "user", Content: riskReportProfile(doc)},
		},
	})
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AI 模型未配置")
			return
		}
		writeError(w, http.StatusBadGateway, "风险报告生成失败："+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, aiRiskReportResponse{Report: strings.TrimSpace(resp.Text)})
}

// riskReportProfile flattens supplier data + rule-engine signals + change
// history into one text the LLM wants for a shell-risk assessment.
func riskReportProfile(d *domain.Supplier) string {
	var b strings.Builder
	b.WriteString("公司：" + d.BasicInfo.CompanyName + "\n")
	region := strings.Join(nonEmpty([]string{d.BasicInfo.Region.Province, d.BasicInfo.Region.City, d.BasicInfo.Region.District}), " ")
	if region != "" {
		b.WriteString("地域：" + region + "\n")
	}
	if v := d.BasicInfo.EstablishmentDate; v != "" {
		b.WriteString("成立日期：" + v + "\n")
	}
	if v := d.BasicInfo.RegisteredCapital; v != "" {
		b.WriteString("注册资本：" + v + "\n")
	}
	if v := d.BasicInfo.CreditCode; v != "" {
		b.WriteString("统一社会信用代码：" + v + "\n")
	}
	if d.Rating > 0 {
		b.WriteString(fmt.Sprintf("综合评分：%.2f\n", d.Rating))
	}
	if len(d.PerformanceHistory) > 0 {
		b.WriteString(fmt.Sprintf("合作项目数：%d\n", len(d.PerformanceHistory)))
	}
	if len(d.Qualifications) > 0 {
		part := make([]string, 0, len(d.Qualifications))
		for _, q := range d.Qualifications {
			part = append(part, strings.TrimSpace(q.Type+" "+q.Level)+"(到期:"+q.Expiry+")")
		}
		b.WriteString("资质：" + strings.Join(part, "；") + "\n")
	}
	if d.BlacklistReason != "" {
		b.WriteString("黑名单原因：" + d.BlacklistReason + "\n")
	}

	// Local rule-engine signals (the NON-AI layer both flags shell_risk and
	// feeds this report).
	rep := risk.Evaluate(d, time.Now().UTC())
	b.WriteString(fmt.Sprintf("规则引擎判定：shell_risk=%v\n", rep.ShellRisk))
	for _, sig := range rep.Signals {
		b.WriteString(fmt.Sprintf("- [%s][%s] %s\n", string(sig.Severity), sig.Code, sig.Message))
	}

	// Recent change history (工商/档案变更 → shell 特征).
	if len(d.ChangeLog) > 0 {
		cl := make([]domain.ChangeEntry, len(d.ChangeLog))
		copy(cl, d.ChangeLog)
		sort.Slice(cl, func(i, j int) bool { return cl[i].Date.After(cl[j].Date) })
		top := cl
		if len(top) > 10 {
			top = top[:10]
		}
		b.WriteString("最近变更：\n")
		for _, c := range top {
			b.WriteString(fmt.Sprintf("- %s: %v → %v（%s）\n", c.Field, c.Old, c.New, c.Date.Format("2006-01")))
		}
	}
	return b.String()
}
