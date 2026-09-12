package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/domain"
)

// 自然语言搜索 (TR-19-D): the user types "杭州本地能做市政工程的二级资质以上
// 供应商" and the LLM turns it into structured filter parameters. The manual
// keyword + filters stay the non-AI path (no key → the AI-search toggle is
// hidden).

// aiNLSearchRequest is the POST /ai/nl-search body.
type aiNLSearchRequest struct {
	Query string `json:"query"`
}

// aiNLSearchResponse is the structured filter the frontend applies directly to
// ListParams. Empty string / 0 = that dimension unconstrained.
type aiNLSearchResponse struct {
	Keyword      string  `json:"keyword"`
	Province     string  `json:"province"`
	City         string  `json:"city"`
	District     string  `json:"district"`
	Category     string  `json:"category"`
	MinQualLevel string  `json:"min_qual_level"`
	MinRating    float64 `json:"min_rating"`
}

const nlSearchPrompt = `你是供应商搜索结构化助手。把用户的自然语言需求转成一个 JSON 对象（不要 Markdown、不要解释），字段如下，没有提到的维度用空字符串/0：
- keyword: 自由关键词（公司名/法人/经营范围等），没有则 ""
- province: 省（短名，如 "浙江"，不带"省"字），没有则 ""
- city: 市（短名，如 "杭州"，不带"市"字），没有则 ""
- district: 区县（如 "西湖区"），没有则 ""
- category: 品类关键词（如 "市政工程" 或 "施工服务"），没有则 ""
- min_qual_level: 最低资质等级，只能是以下之一：特级/一级/二级/三级/甲级/乙级/丙级；没有则 ""
- min_rating: 最低评分（0-5 数字），没有则 0
注意："本地"指地域，请把地域词拆到 province/city/district；"二级资质以上"指 min_qual_level="二级"`

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

	resp, err := gw.Complete(r.Context(), aigateway.ChatRequest{
		Task: "nl2filter",
		Messages: []aigateway.ChatMessage{{
			Role:    "system",
			Content: nlSearchPrompt,
		}, {
			Role:    "user",
			Content: req.Query,
		}},
		JSONMode: true,
	})
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AI 模型未配置")
			return
		}
		writeError(w, http.StatusBadGateway, "自然语言解析失败："+err.Error())
		return
	}

	body, err := extractJSONObject(resp.Text)
	if err != nil {
		writeError(w, http.StatusBadGateway, "模型未返回有效 JSON："+err.Error())
		return
	}
	var out aiNLSearchResponse
	if err := json.Unmarshal(body, &out); err != nil {
		writeError(w, http.StatusBadGateway, "模型返回无法解析："+err.Error())
		return
	}

	// Sanitize: reject unknown qual levels (a hard filter built on a typo
	// would be silently dropped by the backend otherwise) and clamp rating.
	if out.MinQualLevel != "" && !domain.QualRankKnown(out.MinQualLevel) {
		writeError(w, http.StatusBadGateway, "模型返回了无法识别的资质等级："+out.MinQualLevel)
		return
	}
	if out.MinRating < 0 {
		out.MinRating = 0
	}
	if out.MinRating > 5 {
		out.MinRating = 5
	}
	out.Keyword = strings.TrimSpace(out.Keyword)
	out.Province = strings.TrimSpace(out.Province)
	out.City = strings.TrimSpace(out.City)
	out.District = strings.TrimSpace(out.District)
	out.Category = strings.TrimSpace(out.Category)
	out.MinQualLevel = strings.TrimSpace(out.MinQualLevel)

	writeJSON(w, http.StatusOK, out)
}
