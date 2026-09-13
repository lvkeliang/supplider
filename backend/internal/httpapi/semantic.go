package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/search"
	"github.com/supplider/supplider/backend/internal/semantic"
)

// 语义搜索 (TR-19-E): POST /ai/index primes the vector index from the library,
// POST /ai/semantic-search embeds a natural-language query and returns the
// most-similar suppliers. Both are gated on a configured embedding model
// (CanEmbed) — without one the endpoints answer 503 and the UI hides the
// entry point; keyword + structured filter search stay the non-AI path.

// semanticSearchRequest is the POST /ai/semantic-search body.
type semanticSearchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k,omitempty"` // default 10, capped at 100
}

// semanticSearchResult inlines the list-page summary plus the cosine score.
type semanticSearchResult struct {
	domain.Summary
	Score float64 `json:"score"`
}

// semanticSearchResponse ranks results by descending similarity.
type semanticSearchResponse struct {
	Results []semanticSearchResult `json:"results"`
}

// semanticSearcher builds the semantic searcher from the live gateway and the
// wired vector store. It returns (nil, status, msg) when unavailable: no AI
// configured, no embedding model, or no vector index wired.
func (s *Server) semanticSearcher() (*semantic.Searcher, int, string) {
	gw := s.liveGateway()
	if gw == nil || !gw.Enabled() {
		return nil, http.StatusServiceUnavailable, "AI 模型未配置，请先在设置中配置模型"
	}
	if !gw.CanEmbed() {
		return nil, http.StatusServiceUnavailable, "未配置 Embedding 模型，语义搜索不可用"
	}
	if s.VectorStore == nil {
		return nil, http.StatusServiceUnavailable, "向量存储未接线"
	}
	return semantic.New(gw, s.VectorStore), 0, ""
}

// handleSemanticSearch embeds a query and returns the topK most similar
// suppliers (archived records are dropped).
func (s *Server) handleSemanticSearch(w http.ResponseWriter, r *http.Request) {
	srch, status, msg := s.semanticSearcher()
	if srch == nil {
		writeError(w, status, msg)
		return
	}

	var req semanticSearchRequest
	if !decodeOptionalJSONBody(w, r, 1<<16, &req) {
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "query 不能为空")
		return
	}
	topK := req.TopK
	if topK <= 0 {
		topK = 10
	}
	if topK > datamodel.MaxPageSize {
		topK = datamodel.MaxPageSize
	}

	scored, err := srch.Search(r.Context(), req.Query, topK)
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "未配置 Embedding 模型")
			return
		}
		writeError(w, http.StatusBadGateway, "语义搜索失败："+err.Error())
		return
	}

	results := make([]semanticSearchResult, 0, len(scored))
	for _, sc := range scored {
		doc, err := s.Service.Get(r.Context(), sc.ID)
		if err != nil {
			if errors.Is(err, datamodel.ErrNotFound) {
				continue
			}
			writeServiceError(w, err)
			return
		}
		if doc.Status == domain.StatusArchived {
			continue
		}
		results = append(results, semanticSearchResult{
			Summary: domain.ToSummary(doc),
			Score:   sc.Score,
		})
	}
	writeJSON(w, http.StatusOK, semanticSearchResponse{Results: results})
}

// handleSemanticIndex re-embeds every live supplier and rebuilds the vector
// index. It is the priming step before semantic search: the index is in-memory
// (non-persistent) for the personal tier, so it is rebuilt on demand.
func (s *Server) handleSemanticIndex(w http.ResponseWriter, r *http.Request) {
	srch, status, msg := s.semanticSearcher()
	if srch == nil {
		writeError(w, status, msg)
		return
	}

	docs, err := s.Service.Export(r.Context(), datamodel.SupplierFilter{})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items := make([]semantic.Doc, 0, len(docs))
	for _, d := range docs {
		items = append(items, semantic.Doc{ID: d.ID, Text: search.DocumentText(d)})
	}

	indexed, err := srch.Rebuild(r.Context(), items)
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "未配置 Embedding 模型")
			return
		}
		writeError(w, http.StatusBadGateway, "向量化索引失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(items), "indexed": indexed})
}
