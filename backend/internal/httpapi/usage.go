package httpapi

import (
	"net/http"

	"github.com/supplider/supplider/backend/internal/usage"
)

// AI 用量统计 (Medium「AI Gateway 统一适配层」): GET /ai/usage returns the
// current UTC month's cumulative chat-completion token counts (calls / tokens
// in / out) plus a per-task breakdown, read from the persisted settings blob.
// Zero counters (AI never used / not configured) are a valid, useful answer.

// handleAIUsage reports the current month's AI token usage.
func (s *Server) handleAIUsage(w http.ResponseWriter, r *http.Request) {
	u := usage.New(s.Service).Current(r.Context())
	writeJSON(w, http.StatusOK, u)
}
