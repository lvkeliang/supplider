package memory

// In-memory datamodel.NotificationStore (reference implementation; the
// SQLite adapter runs the same contract tests). Notifications are kept in
// insertion order under the store mutex and ordered newest-first on read.

import (
	"context"
	"sort"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
)

// AddNotification implements datamodel.NotificationStore.
func (s *Store) AddNotification(_ context.Context, n datamodel.Notification) (datamodel.Notification, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	if n.DedupKey != "" {
		for i := range s.notifs {
			if s.notifs[i].DedupKey == n.DedupKey {
				return s.notifs[i], false, nil // collapse onto the first
			}
		}
	}
	s.notifs = append(s.notifs, n)
	// Prune to the newest KeepNotifications.
	if len(s.notifs) > datamodel.KeepNotifications {
		sorted := append([]datamodel.Notification(nil), s.notifs...)
		sortNotifications(sorted)
		cut := sorted[:datamodel.KeepNotifications]
		keep := make(map[string]struct{}, len(cut))
		for _, k := range cut {
			keep[k.ID] = struct{}{}
		}
		pruned := s.notifs[:0]
		for _, x := range s.notifs {
			if _, ok := keep[x.ID]; ok {
				pruned = append(pruned, x)
			}
		}
		s.notifs = pruned
	}
	return n, true, nil
}

// ListNotifications implements datamodel.NotificationStore (newest first).
func (s *Store) ListNotifications(_ context.Context, q datamodel.NotificationQuery) ([]datamodel.Notification, error) {
	q.Normalize()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]datamodel.Notification, 0, len(s.notifs))
	for _, n := range s.notifs {
		if q.UnreadOnly && n.Read {
			continue
		}
		out = append(out, n)
	}
	sortNotifications(out)
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// CountUnreadNotifications implements datamodel.NotificationStore.
func (s *Store) CountUnreadNotifications(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := 0
	for _, n := range s.notifs {
		if !n.Read {
			c++
		}
	}
	return c, nil
}

// MarkNotificationRead implements datamodel.NotificationStore.
func (s *Store) MarkNotificationRead(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.notifs {
		if s.notifs[i].ID == id {
			s.notifs[i].Read = true
			break
		}
	}
	return nil
}

// MarkAllNotificationsRead implements datamodel.NotificationStore.
func (s *Store) MarkAllNotificationsRead(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.notifs {
		s.notifs[i].Read = true
	}
	return nil
}

// sortNotifications orders newest-first (created_at desc, id desc tiebreak),
// matching the SQLite adapter.
func sortNotifications(ns []datamodel.Notification) {
	sort.Slice(ns, func(i, j int) bool {
		if !ns[i].CreatedAt.Equal(ns[j].CreatedAt) {
			return ns[i].CreatedAt.After(ns[j].CreatedAt)
		}
		return ns[i].ID > ns[j].ID
	})
}
