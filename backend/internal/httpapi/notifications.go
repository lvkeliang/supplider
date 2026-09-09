package httpapi

// HTTP surface for follow (关注) and the in-app change-notification feed
// (变更推送通知关注者). Business logic lives in supplier.Service; these
// handlers only bind query/body to the tier-agnostic methods. The same feed
// is what an enterprise deployment fans out to 钉钉/企微 — personal tier
// surfaces it in the bell, fully local with no AI/network.

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/supplider/supplider/backend/internal/datamodel"
)

// handleWatch follows or unfollows a supplier (POST .../watch). The flag
// comes from ?watched=0|1 or a {"watched":bool} body; POST with no argument
// means "follow". Toggling writes no change_log and does not reorder lists.
func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	watched := true
	if v := r.URL.Query().Get("watched"); v != "" {
		watched = v == "true" || v == "1" || v == "yes" || v == "on"
	}
	if r.Body != nil {
		var body struct {
			Watched *bool `json:"watched"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body)
		if body.Watched != nil {
			watched = *body.Watched
		}
	}
	doc, err := s.Service.SetWatched(r.Context(), r.PathValue("id"), watched)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// notificationsResponse is the GET /api/v1/notifications payload: the feed
// plus the total unread badge (independent of the unread-only filter).
type notificationsResponse struct {
	Unread int                      `json:"unread"`
	Items  []datamodel.Notification `json:"items"`
}

// handleListNotifications answers the notification center feed.
// ?unread=1 returns only unread entries; ?limit=N caps the page (<=100).
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, err := s.Service.ListNotifications(r.Context(), datamodel.NotificationQuery{
		UnreadOnly: q.Get("unread") == "true" || q.Get("unread") == "1",
		Limit:      limit,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	unread, err := s.Service.CountUnreadNotifications(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if items == nil {
		items = []datamodel.Notification{}
	}
	writeJSON(w, http.StatusOK, notificationsResponse{Unread: unread, Items: items})
}

// handleMarkNotificationRead acknowledges one notification.
func (s *Server) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	if err := s.Service.MarkNotificationRead(r.Context(), r.PathValue("id")); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{OK: true})
}

// handleMarkAllNotificationsRead clears the whole unread badge.
func (s *Server) handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if err := s.Service.MarkAllNotificationsRead(r.Context()); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{OK: true})
}
