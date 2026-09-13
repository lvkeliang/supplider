package httpapi

import (
	"net/http"
	"strconv"
)

// 操作审计日志 (Medium「操作审计日志」): GET /api/v1/audit returns the recent
// cross-supplier lifecycle trail (newest first). Lifecycle mutations in the
// handlers call s.Audit.Record(...); field-level history lives per supplier in
// change_log. Personal tier single actor ("local").

// maxAuditLimit caps a single query.
const maxAuditLimit = 500

// handleAudit returns the recent audit trail, newest first.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > maxAuditLimit {
		limit = maxAuditLimit
	}
	items := s.Audit.List(r.Context(), limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(items),
		"items": items,
	})
}
