package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/nlsearch"
)

// 自然语言搜索 (TR-19-D): the user types "杭州本地能做市政工程的二级资质以上
// 供应商" and the LLM turns it into structured filter parameters. The manual
// keyword + filters stay the non-AI path (no key → the AI-search toggle is
// hidden). The prompt + sanitization live in internal/nlsearch so the HTTP,
// MCP and CLI surfaces share identical behavior.

// aiNLSearchRequest is the POST /ai/nl-search body.
type aiNLSearchRequest struct {
	Query string `json:"query"`
}

// aiNLSearchResponse is the wire type for the parsed structure (an alias of the
// shared nlsearch.Filter — same JSON fields, no divergence).
type aiNLSearchResponse = nlsearch.Filter

// handleAINLSearch maps a natural-language query to a structured filter.
func (s *Server) handleAINLSearch(w http.ResponseWriter, r *http.Request) {
	gw := s.liveGateway()
	if gw == nil || !gw.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "AI 模型未配置，请先在设置中配置模型")
		return
	}

	var req aiNLSearchRequest
	if !decodeOptionalJSONBody(w, r, 1<<16, &req) {
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "query 不能为空")
		return
	}

	f, err := nlsearch.Parse(r.Context(), gw, req.Query)
	if err != nil {
		switch {
		case errors.Is(err, aigateway.ErrAIDisabled):
			writeError(w, http.StatusServiceUnavailable, "AI 模型未配置")
		case errors.Is(err, nlsearch.ErrUnknownQualLevel):
			writeError(w, http.StatusBadGateway, "模型返回了无法识别的资质等级")
		default:
			writeError(w, http.StatusBadGateway, "自然语言解析失败："+err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, f)
}
