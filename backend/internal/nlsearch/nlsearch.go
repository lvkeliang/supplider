// Package nlsearch turns a natural-language search query into a structured
// supplier filter via the AI gateway (PRD「自然语言搜索」). It is shared by the
// HTTP endpoint (POST /ai/nl-search), the MCP tool (nl_search_suppliers) and
// the CLI so the prompt + sanitization never diverge across surfaces. It is
// AI-native but degradable: without a configured provider it returns
// aigateway.ErrAIDisabled and callers fall back to handing the raw text to the
// keyword search.
package nlsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/domain"
)

// ErrUnknownQualLevel is returned when the model emits an unrecognized
// qualification level (a hard filter built on a typo would otherwise silently
// drop every supplier).
var ErrUnknownQualLevel = errors.New("nlsearch: unknown qual level")

// Filter is the structured result of parsing a natural-language query.
// Empty string / 0 = that dimension unconstrained.
type Filter struct {
	Keyword      string  `json:"keyword"`
	Province     string  `json:"province"`
	City         string  `json:"city"`
	District     string  `json:"district"`
	Category     string  `json:"category"`
	MinQualLevel string  `json:"min_qual_level"`
	MinRating    float64 `json:"min_rating"`
}

const prompt = `你是供应商搜索结构化助手。把用户的自然语言需求转成一个 JSON 对象（不要 Markdown、不要解释），字段如下，没有提到的维度用空字符串/0：
- keyword: 自由关键词（公司名/法人/经营范围等），没有则 ""
- province: 省（短名，如 "浙江"，不带"省"字），没有则 ""
- city: 市（短名，如 "杭州"，不带"市"字），没有则 ""
- district: 区县（如 "西湖区"），没有则 ""
- category: 品类关键词（如 "市政工程" 或 "施工服务"），没有则 ""
- min_qual_level: 最低资质等级，只能是以下之一：特级/一级/二级/三级/甲级/乙级/丙级；没有则 ""
- min_rating: 最低评分（0-5 数字），没有则 0
注意："本地"指地域，请把地域词拆到 province/city/district；"二级资质以上"指 min_qual_level="二级"`

// Parse runs the NL→filter completion and sanitizes the result (trim, clamp
// rating, reject unknown qual level). It is injectable-safe: pass any
// aigateway.Gateway (production or a fake in tests).
func Parse(ctx context.Context, gw aigateway.Gateway, query string) (Filter, error) {
	resp, err := gw.Complete(ctx, aigateway.ChatRequest{
		Task: "nl2filter",
		Messages: []aigateway.ChatMessage{
			{Role: "system", Content: prompt},
			{Role: "user", Content: query},
		},
		JSONMode: true,
	})
	if err != nil {
		return Filter{}, err
	}

	obj, err := extractJSONObject(resp.Text)
	if err != nil {
		return Filter{}, err
	}
	var f Filter
	if err := json.Unmarshal(obj, &f); err != nil {
		return Filter{}, err
	}

	f.Keyword = strings.TrimSpace(f.Keyword)
	f.Province = strings.TrimSpace(f.Province)
	f.City = strings.TrimSpace(f.City)
	f.District = strings.TrimSpace(f.District)
	f.Category = strings.TrimSpace(f.Category)
	f.MinQualLevel = strings.TrimSpace(f.MinQualLevel)
	if f.MinQualLevel != "" && !domain.QualRankKnown(f.MinQualLevel) {
		return Filter{}, fmt.Errorf("%w: %s", ErrUnknownQualLevel, f.MinQualLevel)
	}
	if f.MinRating < 0 {
		f.MinRating = 0
	}
	if f.MinRating > 5 {
		f.MinRating = 5
	}
	return f, nil
}

// extractJSONObject returns the first {...} object in a model reply, stripping
// markdown fences and leading prose some providers add despite JSON mode.
func extractJSONObject(text string) ([]byte, error) {
	s := strings.TrimSpace(text)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in response")
	}
	return []byte(s[start : end+1]), nil
}
