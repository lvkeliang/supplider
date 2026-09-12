package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/importer"
)

// Excel 智能列映射 (TR-19-C): given the spreadsheet headers + a few sample
// values, the configured LLM suggests a column→field mapping that the import
// preview then applies (the user still reviews/adjusts it — AI is advisory,
// the manual dropdown mapping is the non-AI path).

// aiExcelMapRequest is the POST /ai/excel-map body.
type aiExcelMapRequest struct {
	Headers []string   `json:"headers"`
	Sample  [][]string `json:"sample"`
}

// aiExcelMapResponse returns column index (string, matching the UI's mapping
// keys) → field key.
type aiExcelMapResponse struct {
	Mapping map[string]string `json:"mapping"`
}

// validFieldKeys is the set the model may return, plus the two sentinels.
func validFieldKeys() map[string]bool {
	m := map[string]bool{importer.FieldCustom: true, importer.FieldIgnore: true}
	for _, f := range importer.FieldOptions() {
		m[f.Key] = true
	}
	return m
}

// handleAIExcelMap suggests a column mapping for a previewed spreadsheet.
func (s *Server) handleAIExcelMap(w http.ResponseWriter, r *http.Request) {
	gw := s.liveGateway()
	if gw == nil || !gw.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "AI 模型未配置，请先在设置中配置模型")
		return
	}

	var req aiExcelMapRequest
	if !decodeOptionalJSONBody(w, r, 1<<16, &req) {
		return
	}
	if len(req.Headers) == 0 {
		writeError(w, http.StatusBadRequest, "headers 不能为空")
		return
	}

	resp, err := gw.Complete(r.Context(), aigateway.ChatRequest{
		Task:     "excel-map",
		Messages: []aigateway.ChatMessage{{Role: "user", Content: buildExcelMapPrompt(req.Headers, req.Sample)}},
		JSONMode: true,
	})
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AI 模型未配置")
			return
		}
		writeError(w, http.StatusBadGateway, "智能映射失败："+err.Error())
		return
	}

	body, err := extractJSONObject(resp.Text)
	if err != nil {
		writeError(w, http.StatusBadGateway, "模型未返回有效 JSON："+err.Error())
		return
	}
	var raw map[string]string
	if err := json.Unmarshal(body, &raw); err != nil {
		writeError(w, http.StatusBadGateway, "模型返回无法解析："+err.Error())
		return
	}

	// Keep only valid column indices → valid field keys; drop hallucinations
	// (the user reviews the result anyway, so a dropped entry is harmless).
	validKeys := validFieldKeys()
	clean := make(map[string]string, len(raw))
	for colStr, key := range raw {
		idx, err := strconv.Atoi(colStr)
		if err != nil || idx < 0 || idx >= len(req.Headers) {
			continue
		}
		if !validKeys[key] {
			continue
		}
		clean[strconv.Itoa(idx)] = key
	}

	writeJSON(w, http.StatusOK, aiExcelMapResponse{Mapping: clean})
}

// buildExcelMapPrompt renders the field catalog + headers + samples for the
// model to map by semantics.
func buildExcelMapPrompt(headers []string, sample [][]string) string {
	var b strings.Builder
	b.WriteString("你是 Excel 列映射助手。把下面的电子表格列映射到供应商字段。")
	b.WriteString("只返回一个 JSON 对象（不要 Markdown、不要解释），键是列索引（0 起始的字符串），值是字段 key。")
	b.WriteString("可用字段 key 及含义：\n")
	for _, f := range importer.FieldOptions() {
		fmt.Fprintf(&b, "- %s = %s%s\n", f.Key, f.Label, map[bool]string{true: "（必填）", false: ""}[f.Required])
	}
	b.WriteString("- custom = 保留为自定义字段\n- \"\" = 忽略该列\n\n")

	b.WriteString("列头：\n")
	for i, h := range headers {
		fmt.Fprintf(&b, "[%d] %s\n", i, strings.TrimSpace(h))
	}
	if len(sample) > 0 {
		b.WriteString("\n示例数据行（前几行）：\n")
		for _, row := range sample {
			b.WriteString("| " + strings.Join(row, " | ") + " |\n")
		}
	}
	return b.String()
}
