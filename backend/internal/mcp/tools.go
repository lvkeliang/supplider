package mcp

// Tool / resource / prompt definitions and the tools/call + resources/read
// dispatch for the MCP server. Everything here is tier-agnostic business
// code — it talks to *supplier.Service, the same use cases the CLI/HTTP API
// expose, so an AI agent gets the same data the app shows.

import (
	"encoding/json"
	"fmt"
)

// textContent is one MCP content block (text).
func textContent(t string) map[string]any {
	return map[string]any{"type": "text", "text": t}
}

// toolResult wraps a successful call's text.
func toolResult(text string) any {
	return map[string]any{"content": []any{textContent(text)}}
}

// toolError wraps a FAILURE OF THE TOOL (bad input / not found) — distinct
// from a protocol error. It tells the model the call ran but reported an
// issue, so the model can correct the request.
func toolError(text string) any {
	return map[string]any{"content": []any{textContent(text)}, "isError": true}
}

// pretty JSON-encodes a value for a text block.
func pretty(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// ---- tools/list ----

// schema shorthand for a JSON-Schema object with the given properties and
// required keys.
func schema(props map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name":        "search_suppliers",
			"description": "搜索供应商（中文全文/拼音/同义词 + 地域/品类/资质/评分筛选）。q 可空（空即列出首页）。返回摘要列表（id/名称/地域/品类/最高资质/评分/空壳风险/审核状态）。无 AI，纯本地检索。",
			"inputSchema": schema(map[string]any{
				"q":                strProp("关键词：公司名/信用代码/法人/经营范围/品类；支持中文子串、拼音、首字母、同义词"),
				"province":         strProp("省，如 浙江"),
				"city":             strProp("市，如 杭州"),
				"category":         strProp("品类，逗号分隔表示 OR"),
				"min_qual_level":   strProp("最低资质等级，如 二级 / 一级 / 特级"),
				"min_rating":       map[string]any{"type": "number", "description": "最低综合评分 0-5"},
				"include_archived": map[string]any{"type": "boolean", "description": "是否包含已归档供应商，默认 false"},
				"limit":            map[string]any{"type": "integer", "description": "每页条数，默认 20，最大 100"},
			}),
		},
		{
			"name":        "get_supplier",
			"description": "按 id 读取供应商完整档案（文档式：基本信息/资质/品类/产品服务/绩效/风险标记/自定义字段/附件/变更记录）。",
			"inputSchema": schema(map[string]any{
				"id": strProp("供应商 id（sup_ 开头，由 search/add 返回）"),
			}, "id"),
		},
		{
			"name":        "add_supplier",
			"description": "录入一家新供应商。必填 company_name/province/city；其余可选。录入后自动跑本地空壳特征检测（不阻断录入）。",
			"inputSchema": schema(map[string]any{
				"company_name":       strProp("公司名称（必填）"),
				"province":           strProp("省（必填），如 浙江"),
				"city":               strProp("市（必填），如 杭州"),
				"district":           strProp("区县"),
				"credit_code":        strProp("统一社会信用代码（18 位，会做 GB 32100 校验位检测）"),
				"legal_person":       strProp("法定代表人"),
				"registered_capital": strProp("注册资本，如 5000万人民币"),
				"establishment_date": strProp("成立日期 YYYY-MM-DD"),
				"business_scope":     strProp("经营范围"),
				"supplier_type":      strProp("供应商类型，如 施工商/贸易商/服务商"),
				"contact_name":       strProp("联系人"),
				"contact_phone":      strProp("联系电话"),
				"categories":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "品类标签数组"},
				"visibility":         map[string]any{"type": "integer", "description": "可见性 0=仅自己 1=指定人 …，默认 0"},
				"owner":              strProp("录入人，默认 local"),
			}, "company_name", "province", "city"),
		},
		{
			"name":        "expiring_qualifications",
			"description": "资质到期提醒扫描：列出已过期或在 90/30/7 天窗口内到期的资质（按紧迫度排序）。纯本地日期计算。",
			"inputSchema": schema(map[string]any{
				"within": map[string]any{"type": "integer", "description": "前瞻天数，默认 90"},
			}),
		},
		{
			"name":        "shell_risk_queue",
			"description": "空壳风险待审核队列：列出本地规则检测为空壳风险、且尚未人工审核的在库供应商（附逐条信号与高/中计数）。",
			"inputSchema": schema(map[string]any{}),
		},
		{
			"name":        "supplier_risk",
			"description": "查看某供应商的空壳特征检测逐条信号（信用代码校验位/成立时长/资料完整度/资质/注册资本等规则代码与中文解释）。",
			"inputSchema": schema(map[string]any{
				"id": strProp("供应商 id"),
			}, "id"),
		},
		{
			"name":        "blacklist_supplier",
			"description": "淘汰阶段：把供应商列入黑名单（确认造假/严重违约等，仍可搜到但醒目标记为禁用），或从黑名单移出。这是人工生命周期操作，会写入变更记录。",
			"inputSchema": schema(map[string]any{
				"id":     strProp("供应商 id"),
				"reason": strProp("列入原因，如 资质造假/严重违约（移出时可省略）"),
				"remove": map[string]any{"type": "boolean", "description": "true 表示移出黑名单（恢复在库），默认 false=列入"},
			}, "id"),
		},
		{
			"name":        "compare_suppliers",
			"description": "并排对比多家供应商：地域/品类/最高资质/评分/绩效均分/价格区间/风险/状态，辅助比价选型。",
			"inputSchema": schema(map[string]any{
				"ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "供应商 id 列表（至少 2 个）",
				},
			}, "ids"),
		},
	}
}
