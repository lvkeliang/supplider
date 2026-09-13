package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/documentextract"
	"github.com/supplider/supplider/backend/internal/domain"
)

// 文档分析搜索 (TR-19-E): the user uploads (or pastes) a requirement document,
// the LLM extracts the structured requirement elements, and the semantic index
// returns the most-similar suppliers, hard-filtered by region / category /
// qualification. It composes the non-AI path (keyword + structured filter) as
// its fallback — no embedding model → 503 and the UI hides the entry point.

// maxDocSearchBytes bounds an uploaded/pasted requirement document. 10MB is
// far larger than any real requirement brief.
const maxDocSearchBytes = 10 << 20

// docSearchRequest is the JSON (pasted text) body for POST /ai/doc-search.
type docSearchRequest struct {
	Text string `json:"text"`
	TopK int    `json:"top_k,omitempty"` // default 10, capped at 100
}

// docRequirement is the LLM-extracted requirement. Requirement is the concise
// summary actually embedded; the rest are hard filters applied post-match.
type docRequirement struct {
	Requirement  string `json:"requirement"`
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	Category     string `json:"category"`
	MinQualLevel string `json:"min_qual_level"`
}

// docSearchResult inlines the list summary plus score and a human-readable
// match reason (rule-derived from the score and the hard filters it passed).
type docSearchResult struct {
	domain.Summary
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// docSearchResponse returns the extracted requirement alongside ranked
// recommendations.
type docSearchResponse struct {
	Requirement docRequirement    `json:"requirement"`
	Results     []docSearchResult `json:"results"`
}

const docExtractPrompt = `你是供应商需求分析助手。从用户上传的需求文档中提取采购/合作需求，只返回一个 JSON 对象（不要 Markdown 代码块、不要解释），字段如下，没有提到的维度用空字符串：
- requirement: 需求的一句话总结（含关键材料/服务/工程量/规格，用于语义匹配供应商），不能为空
- province: 省（短名，如 "浙江"，不带"省"字）
- city: 市（短名，如 "杭州"，不带"市"字）
- district: 区县（如 "西湖区"）
- category: 最核心的一个品类关键词（如 "市政工程"/"商砼"/"脚手架"）
- min_qual_level: 最低资质等级，只能是 特级/一级/二级/三级/甲级/乙级/丙级 之一，没有则 ""`

// handleDocSearch runs the document-analysis pipeline: extract text (or read a
// document image via vision) → LLM requirement extraction → embed → semantic
// match → hard-filter → reason.
func (s *Server) handleDocSearch(w http.ResponseWriter, r *http.Request) {
	srch, status, msg := s.semanticSearcher()
	if srch == nil {
		writeError(w, status, msg)
		return
	}
	// semanticSearcher verified the gateway is enabled and can embed; the same
	// gateway serves the LLM extraction call below.
	gw := s.liveGateway()

	in, ok := readDocSearchInput(w, r)
	if !ok {
		return
	}
	topK := in.topK
	if topK <= 0 {
		topK = 10
	}
	if topK > 100 {
		topK = 100
	}

	// Build the extraction request: a document image goes straight to the
	// vision model (no local text); text/docx/xlsx are sent as prose.
	var msgs []aigateway.ChatMessage
	if in.image != nil {
		if strings.TrimSpace(in.imageMIME) == "" {
			in.imageMIME = "image/png"
		}
		msgs = []aigateway.ChatMessage{{
			Role:        "user",
			Content:     docExtractPrompt,
			ImageBase64: base64.StdEncoding.EncodeToString(in.image),
			ImageMIME:   in.imageMIME,
		}}
	} else {
		if strings.TrimSpace(in.text) == "" {
			writeError(w, http.StatusBadRequest, "需求文档为空")
			return
		}
		// A pasted doc can far exceed any LLM context window — clip to a generous
		// head (the key requirement elements usually lead) so the call never
		// blows up on an oversized paste.
		text := clipDocText(in.text)
		msgs = []aigateway.ChatMessage{
			{Role: "system", Content: docExtractPrompt},
			{Role: "user", Content: text},
		}
	}

	resp, err := gw.Complete(r.Context(), aigateway.ChatRequest{
		Task:     "doc_extract",
		Messages: msgs,
		JSONMode: true,
	})
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AI 模型未配置")
			return
		}
		writeError(w, http.StatusBadGateway, "需求解析失败："+err.Error())
		return
	}

	body, err := extractJSONObject(resp.Text)
	if err != nil {
		writeError(w, http.StatusBadGateway, "模型未返回有效 JSON："+err.Error())
		return
	}
	var req docRequirement
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadGateway, "模型返回无法解析："+err.Error())
		return
	}
	req.Requirement = strings.TrimSpace(req.Requirement)
	req.Province = strings.TrimSpace(req.Province)
	req.City = strings.TrimSpace(req.City)
	req.District = strings.TrimSpace(req.District)
	req.Category = strings.TrimSpace(req.Category)
	req.MinQualLevel = strings.TrimSpace(req.MinQualLevel)

	// A document with no extractable requirement is meaningless to embed.
	if req.Requirement == "" {
		writeError(w, http.StatusBadGateway, "未能从文档中提取出需求")
		return
	}
	// Reject an unrecognized qual level so a hard filter built on a typo never
	// silently drops every supplier.
	if req.MinQualLevel != "" && !domain.QualRankKnown(req.MinQualLevel) {
		writeError(w, http.StatusBadGateway, "模型返回了无法识别的资质等级："+req.MinQualLevel)
		return
	}

	// Search a larger candidate pool so hard filters can drop some results
	// without starving the final topK.
	pool := topK * 4
	if pool > 100 {
		pool = 100
	}
	scored, err := srch.Search(r.Context(), req.Requirement, pool)
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "未配置 Embedding 模型")
			return
		}
		writeError(w, http.StatusBadGateway, "语义匹配失败："+err.Error())
		return
	}

	results := make([]docSearchResult, 0, topK)
	for _, sc := range scored {
		doc, err := s.Service.Get(r.Context(), sc.ID)
		if err != nil {
			if errors.Is(err, datamodel.ErrNotFound) {
				continue
			}
			writeServiceError(w, err)
			return
		}
		if doc.Status == domain.StatusArchived || !docPassesHardFilters(doc, req) {
			continue
		}
		results = append(results, docSearchResult{
			Summary: domain.ToSummary(doc),
			Score:   sc.Score,
			Reason:  docMatchReason(sc.Score, doc, req),
		})
		if len(results) >= topK {
			break
		}
	}

	writeJSON(w, http.StatusOK, docSearchResponse{Requirement: req, Results: results})
}

// maxDocInputChars bounds the requirement text sent to the LLM. A brief/清理
// document is usually a few KB; 30k chars covers real briefs while staying well
// inside any model context window.
const maxDocInputChars = 30000

// clipDocText truncates a document to maxDocInputChars runes at a UTF-8-safe
// boundary (the key requirement elements conventionally lead the document).
func clipDocText(s string) string {
	if len(s) <= maxDocInputChars {
		return s
	}
	// Walk runes, keep a packing margin so a trailing multi-byte rune never
	// produces invalid UTF-8.
	runes := []rune(s)
	if len(runes) > maxDocInputChars {
		return string(runes[:maxDocInputChars])
	}
	return s
}

// docSearchInput is the result of reading one doc-search request: either
// extracted text (plain/docx/xlsx) or a raw document image routed to vision.
type docSearchInput struct {
	text      string
	topK      int
	image     []byte // non-nil → vision mode (LLM reads the image directly)
	imageMIME string
}

// readDocSearchInput reads a multipart file (text/docx/xlsx/image) or a JSON
// {text, top_k} body. It writes the error response itself and returns ok=false
// on failure.
func readDocSearchInput(w http.ResponseWriter, r *http.Request) (docSearchInput, bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxDocSearchBytes+(1<<20))
		if err := r.ParseMultipartForm(maxDocSearchBytes); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("文档超过 %dMB 限制", maxDocSearchBytes/(1024*1024)))
				return docSearchInput{}, false
			}
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return docSearchInput{}, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, `expected a multipart "file" field: `+err.Error())
			return docSearchInput{}, false
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, maxDocSearchBytes))
		if err != nil {
			writeError(w, http.StatusBadRequest, "read document: "+err.Error())
			return docSearchInput{}, false
		}
		if len(raw) == 0 {
			writeError(w, http.StatusBadRequest, "文档为空")
			return docSearchInput{}, false
		}

		text, err := documentextract.FromFile(raw, header.Header.Get("Content-Type"))
		if err != nil {
			switch {
			case errors.Is(err, documentextract.ErrImage):
				return docSearchInput{image: raw, imageMIME: header.Header.Get("Content-Type"), topK: 10}, true
			case errors.Is(err, documentextract.ErrUnsupported):
				writeError(w, http.StatusUnsupportedMediaType, "暂不支持该文档格式（如 PDF）；请粘贴文本，或改用 .txt/.md/.docx/.xlsx/图片")
				return docSearchInput{}, false
			default:
				writeError(w, http.StatusBadRequest, "文档解析失败："+err.Error())
				return docSearchInput{}, false
			}
		}
		return docSearchInput{text: text, topK: 10}, true
	}

	var req docSearchRequest
	if !decodeOptionalJSONBody(w, r, maxDocSearchBytes, &req) {
		return docSearchInput{}, false
	}
	return docSearchInput{text: req.Text, topK: req.TopK}, true
}

// docPassesHardFilters applies the extracted region / category / qualification
// constraints to one candidate. Empty dimensions are unconstrained.
func docPassesHardFilters(doc *domain.Supplier, req docRequirement) bool {
	if req.Province != "" && doc.BasicInfo.Region.Province != req.Province {
		return false
	}
	if req.City != "" && doc.BasicInfo.Region.City != req.City {
		return false
	}
	if req.District != "" && doc.BasicInfo.Region.District != req.District {
		return false
	}
	if req.Category != "" && !docCategoryMatches(doc, req.Category) {
		return false
	}
	if req.MinQualLevel != "" && domain.HighestQualRank(doc) < domain.QualRank(req.MinQualLevel) {
		return false
	}
	return true
}

// docCategoryMatches reports whether a supplier carries the wanted category —
// exact (case-insensitive) or as a substring of a longer category tag.
func docCategoryMatches(doc *domain.Supplier, want string) bool {
	for _, c := range doc.Categories {
		if strings.EqualFold(c, want) || strings.Contains(c, want) {
			return true
		}
	}
	return false
}

// docMatchReason builds a compact human-readable reason from the similarity
// bucket and the hard filters the candidate satisfied.
func docMatchReason(score float64, doc *domain.Supplier, req docRequirement) string {
	parts := make([]string, 0, 4)
	switch {
	case score >= 0.9:
		parts = append(parts, "高度匹配")
	case score >= 0.7:
		parts = append(parts, "较好匹配")
	case score >= 0.5:
		parts = append(parts, "相关")
	default:
		parts = append(parts, "弱相关")
	}
	if req.Province != "" && doc.BasicInfo.Region.Province == req.Province {
		parts = append(parts, "地域匹配")
	}
	if req.Category != "" && docCategoryMatches(doc, req.Category) {
		parts = append(parts, "品类匹配")
	}
	if req.MinQualLevel != "" && domain.HighestQualRank(doc) >= domain.QualRank(req.MinQualLevel) {
		parts = append(parts, "资质达标")
	}
	return strings.Join(parts, " · ")
}
