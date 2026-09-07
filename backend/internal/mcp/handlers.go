package mcp

// tools/call, resources/read and prompts/get handlers. Each runs the same
// *supplier.Service use case the CLI/HTTP API expose; results are returned
// as pretty-printed JSON text blocks an MCP host can read or pass on.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// ---- tools/call ----

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: "invalid tools/call params: " + err.Error()}
	}
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var text string
	var ferr error
	switch p.Name {
	case "search_suppliers":
		text, ferr = s.toolSearch(ctx, args)
	case "get_supplier":
		text, ferr = s.toolGet(ctx, args)
	case "add_supplier":
		text, ferr = s.toolAdd(ctx, args)
	case "expiring_qualifications":
		text, ferr = s.toolExpiring(ctx, args)
	case "shell_risk_queue":
		text, ferr = s.toolShellQueue(ctx)
	case "supplier_risk":
		text, ferr = s.toolSupplierRisk(ctx, args)
	case "compare_suppliers":
		text, ferr = s.toolCompare(ctx, args)
	case "blacklist_supplier":
		text, ferr = s.toolBlacklist(ctx, args)
	default:
		return nil, &rpcError{Code: errMethodNotFound, Message: "unknown tool: " + p.Name}
	}
	if ferr != nil {
		// Tool ran but reported a problem (bad id, validation) — surface to
		// the model as a tool error so it can correct, not as a protocol fault.
		return toolError(ferr.Error()), nil
	}
	return toolResult(text), nil
}

func decodeArgs(args json.RawMessage, v any) {
	// Arguments are loosely typed by the host; ignore unknown fields and
	// tolerate missing ones (handlers apply defaults).
	_ = json.Unmarshal(args, v)
}

func (s *Server) toolSearch(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Q               string  `json:"q"`
		Province        string  `json:"province"`
		City            string  `json:"city"`
		District        string  `json:"district"`
		Category        string  `json:"category"`
		MinQualLevel    string  `json:"min_qual_level"`
		MinRating       float64 `json:"min_rating"`
		IncludeArchived bool    `json:"include_archived"`
		Limit           int     `json:"limit"`
	}
	decodeArgs(args, &a)
	if a.Limit <= 0 {
		a.Limit = 20
	}

	f := datamodel.SupplierFilter{
		Keyword:         strings.TrimSpace(a.Q),
		Province:        strings.TrimSpace(a.Province),
		City:            strings.TrimSpace(a.City),
		District:        strings.TrimSpace(a.District),
		Categories:      splitCSV(a.Category),
		MinRating:       a.MinRating,
		IncludeArchived: a.IncludeArchived,
	}
	if lvl := strings.TrimSpace(a.MinQualLevel); lvl != "" {
		f.MinQualRank = domain.QualRank(lvl)
	}

	page, err := s.svc.List(ctx, datamodel.Query{Filter: f, Limit: a.Limit})
	if err != nil {
		return "", err
	}
	out := map[string]any{
		"count":       len(page.Items),
		"next_cursor": page.NextCursor,
		"items":       page.Items,
	}
	header := fmt.Sprintf("找到 %d 家供应商", len(page.Items))
	if page.NextCursor != "" {
		header += "（还有更多，可用 limit/分页继续）"
	}
	return header + "：\n" + pretty(out), nil
}

func (s *Server) toolGet(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	decodeArgs(args, &a)
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	doc, err := s.svc.Get(ctx, strings.TrimSpace(a.ID))
	if err != nil {
		return "", err
	}
	return pretty(doc), nil
}

func (s *Server) toolAdd(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		CompanyName       string   `json:"company_name"`
		Province          string   `json:"province"`
		City              string   `json:"city"`
		District          string   `json:"district"`
		CreditCode        string   `json:"credit_code"`
		LegalPerson       string   `json:"legal_person"`
		RegisteredCapital string   `json:"registered_capital"`
		EstablishmentDate string   `json:"establishment_date"`
		BusinessScope     string   `json:"business_scope"`
		SupplierType      string   `json:"supplier_type"`
		ContactName       string   `json:"contact_name"`
		ContactPhone      string   `json:"contact_phone"`
		Categories        []string `json:"categories"`
		Visibility        int      `json:"visibility"`
		Owner             string   `json:"owner"`
	}
	decodeArgs(args, &a)
	if strings.TrimSpace(a.Owner) == "" {
		a.Owner = "local"
	}

	doc, err := s.svc.Create(ctx, supplier.CreateInput{
		Owner:      a.Owner,
		Visibility: a.Visibility,
		Categories: a.Categories,
		BasicInfo: domain.BasicInfo{
			CompanyName:       a.CompanyName,
			CreditCode:        a.CreditCode,
			LegalPerson:       a.LegalPerson,
			RegisteredCapital: a.RegisteredCapital,
			EstablishmentDate: a.EstablishmentDate,
			BusinessScope:     a.BusinessScope,
			SupplierType:      a.SupplierType,
			ContactName:       a.ContactName,
			ContactPhone:      a.ContactPhone,
			Region:            domain.Region{Province: a.Province, City: a.City, District: a.District},
		},
	})
	if err != nil {
		return "", err
	}
	status := "未触发空壳风险"
	if doc.RiskFlags.ShellRisk {
		status = "⚠ 检测到空壳风险，建议用 shell_risk_queue / supplier_risk 复核：" + doc.RiskFlags.Notes
	}
	return fmt.Sprintf("已录入供应商 %s（%s）。%s\n%s", doc.ID, doc.BasicInfo.CompanyName, status, pretty(doc)), nil
}

func (s *Server) toolExpiring(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Within int `json:"within"`
	}
	decodeArgs(args, &a)
	if a.Within <= 0 {
		a.Within = supplier.DefaultExpiryWindow
	}
	alerts, err := s.svc.ExpiringQualifications(ctx, a.Within)
	if err != nil {
		return "", err
	}
	expired := 0
	for _, al := range alerts {
		if al.Bucket == supplier.BucketExpired {
			expired++
		}
	}
	out := map[string]any{"within_days": a.Within, "count": len(alerts), "expired": expired, "items": alerts}
	return fmt.Sprintf("%d 项资质需要处理（其中 %d 项已过期）：\n%s", len(alerts), expired, pretty(out)), nil
}

func (s *Server) toolShellQueue(ctx context.Context) (string, error) {
	items, err := s.svc.ShellRiskSuppliers(ctx)
	if err != nil {
		return "", err
	}
	out := map[string]any{"count": len(items), "items": items}
	if len(items) == 0 {
		return "没有待人工审核的空壳风险供应商（队列已清空）。", nil
	}
	return fmt.Sprintf("%d 家供应商待人工审核：\n%s", len(items), pretty(out)), nil
}

func (s *Server) toolSupplierRisk(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	decodeArgs(args, &a)
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	rep, err := s.svc.RiskReport(ctx, strings.TrimSpace(a.ID))
	if err != nil {
		return "", err
	}
	verdict := "未发现空壳风险（本地规则全部通过）"
	if rep.ShellRisk {
		verdict = "⚠ 判定为空壳风险，建议人工审核（审核后可用 review 闭环标记）"
	}
	return verdict + "：\n" + pretty(rep), nil
}

func (s *Server) toolCompare(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		IDs []string `json:"ids"`
	}
	decodeArgs(args, &a)
	if len(a.IDs) < 2 {
		return "", fmt.Errorf("compare needs at least two supplier ids")
	}
	type row struct {
		ID               string   `json:"id"`
		Name             string   `json:"name"`
		Region           string   `json:"region"`
		Categories       []string `json:"categories,omitempty"`
		TopQual          string   `json:"top_qual,omitempty"`
		Rating           float64  `json:"rating"`
		PerformanceCount int      `json:"performance_count"`
		ShellRisk        bool     `json:"shell_risk"`
		RiskReviewed     bool     `json:"risk_reviewed"`
		Status           string   `json:"status"`
	}
	rows := make([]row, 0, len(a.IDs))
	for _, id := range a.IDs {
		doc, err := s.svc.Get(ctx, strings.TrimSpace(id))
		if err != nil {
			return "", fmt.Errorf("supplier %s: %w", id, err)
		}
		top, _ := domain.TopQualification(doc)
		r := doc.BasicInfo.Region
		rows = append(rows, row{
			ID:               doc.ID,
			Name:             doc.BasicInfo.CompanyName,
			Region:           strings.TrimSpace(r.Province + " " + r.City + " " + r.District),
			Categories:       doc.Categories,
			TopQual:          top,
			Rating:           doc.Rating,
			PerformanceCount: len(doc.PerformanceHistory),
			ShellRisk:        doc.RiskFlags.ShellRisk,
			RiskReviewed:     doc.RiskFlags.Reviewed,
			Status:           doc.Status,
		})
	}
	return "供应商对比：\n" + pretty(rows), nil
}

func (s *Server) toolBlacklist(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
		Remove bool   `json:"remove"`
	}
	decodeArgs(args, &a)
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	id := strings.TrimSpace(a.ID)
	if a.Remove {
		doc, err := s.svc.Unblacklist(ctx, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已将 %s（%s）移出黑名单，恢复在库。", doc.ID, doc.BasicInfo.CompanyName), nil
	}
	doc, err := s.svc.Blacklist(ctx, id, a.Reason)
	if err != nil {
		return "", err
	}
	reason := doc.BlacklistReason
	if reason == "" {
		reason = "(未填原因)"
	}
	return fmt.Sprintf("已将 %s（%s）列入黑名单：%s。该供应商仍可被搜到但会醒目标记为禁用。",
		doc.ID, doc.BasicInfo.CompanyName, reason), nil
}

// ---- resources ----

func staticResources() []map[string]any {
	return []map[string]any{
		{"uri": "supplider://suppliers", "name": "供应商列表（首页摘要）", "description": "在库供应商摘要首页（含空壳风险/审核标记）", "mimeType": "application/json"},
		{"uri": "supplider://risk/shell", "name": "空壳风险待审核队列", "description": "本地规则判定为空壳风险、尚未人工审核的供应商", "mimeType": "application/json"},
		{"uri": "supplider://reminders/expiring", "name": "资质到期提醒", "description": "已过期 / 90 天内到期的资质清单", "mimeType": "application/json"},
	}
}

func resourceTemplates() []map[string]any {
	return []map[string]any{
		{"uriTemplate": "supplider://supplier/{id}", "name": "供应商完整档案", "description": "按 id 读取一家供应商的完整文档式档案", "mimeType": "application/json"},
	}
}

func (s *Server) readResource(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: "invalid resources/read params: " + err.Error()}
	}
	uri := strings.TrimSpace(p.URI)

	mkText := func(v any) any {
		return map[string]any{"contents": []any{map[string]any{"uri": uri, "mimeType": "application/json", "text": pretty(v)}}}
	}

	switch {
	case uri == "supplider://suppliers":
		page, err := s.svc.List(ctx, datamodel.Query{Limit: 100})
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return mkText(map[string]any{"count": len(page.Items), "items": page.Items}), nil
	case uri == "supplider://risk/shell":
		items, err := s.svc.ShellRiskSuppliers(ctx)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return mkText(map[string]any{"count": len(items), "items": items}), nil
	case uri == "supplider://reminders/expiring":
		alerts, err := s.svc.ExpiringQualifications(ctx, supplier.DefaultExpiryWindow)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return mkText(map[string]any{"count": len(alerts), "items": alerts}), nil
	case strings.HasPrefix(uri, "supplider://supplier/"):
		id := strings.TrimPrefix(uri, "supplider://supplier/")
		doc, err := s.svc.Get(ctx, id)
		if err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: "supplier not found: " + id}
		}
		return mkText(doc), nil
	default:
		return nil, &rpcError{Code: errInvalidParams, Message: "unknown resource uri: " + uri}
	}
}

// ---- prompts ----

func promptDefs() []map[string]any {
	return []map[string]any{
		{
			"name":        "supplier-due-diligence",
			"description": "对一家供应商做尽调/背调：拉取档案、空壳风险信号、资质到期情况，给出是否合作的建议。",
			"arguments": []map[string]any{
				{"name": "query", "description": "供应商 id（sup_ 开头）或公司名关键词", "required": true},
			},
		},
	}
}

func (s *Server) getPrompt(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: "invalid prompts/get params: " + err.Error()}
	}
	if p.Name != "supplier-due-diligence" {
		return nil, &rpcError{Code: errMethodNotFound, Message: "unknown prompt: " + p.Name}
	}
	q := strings.TrimSpace(p.Arguments["query"])
	text := fmt.Sprintf(`请对供应商「%s」做一次合作前尽调，步骤如下：
1. 若 %[1]q 是 sup_ 开头的 id，用 get_supplier 读取档案；否则先用 search_suppliers 搜索关键词定位，取最匹配的一家（必要时列出候选让我确认）。
2. 用 supplier_risk 查看该供应商的空壳特征检测信号，注意高/中严重度规则代码与中文解释；若在 shell_risk_queue 中说明尚未人工审核。
3. 用 expiring_qualifications 或在档案中确认其资质是否临期/已过期。
4. 综合档案完整度、资质、评分绩效与风险信号，输出：① 风险摘要 ② 资质与履约能力 ③ 建议（可合作 / 需补料复核 / 不建议），并说明依据。
所有数据来自本地 Supplider 库，无外部 AI/网络；风险信号仅供人工参考。`, q)

	return map[string]any{
		"description": "供应商尽调/背调",
		"messages": []any{
			map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": text}},
		},
	}, nil
}

// splitCSV splits a comma-separated argument into trimmed non-empty parts.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
