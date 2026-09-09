package sqlite

// NotificationStore implementation (see datamodel.NotificationStore). The
// feed is a separate table so archiving/merging a supplier never loses its
// notification history. Dedup is enforced by a partial unique index on
// dedup_key (see migrate): INSERT OR IGNORE collapses repeated sweeps while
// empty-key discrete events always insert.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
)

// AddNotification implements datamodel.NotificationStore. The caller (service
// layer) owns ID/CreatedAt, mirroring SupplierStore.Put. When dedup_key is
// already present the existing row is returned with inserted=false.
func (s *Store) AddNotification(ctx context.Context, n datamodel.Notification) (datamodel.Notification, bool, error) {
	if n.ID == "" {
		return n, false, fmt.Errorf("sqlite: add notification requires an id")
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO notifications
    (id, supplier_id, supplier_name, type, severity, title, body, dedup_key, is_read, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.SupplierID, n.SupplierName, n.Type, n.Severity, n.Title, n.Body,
		n.DedupKey, boolToInt(n.Read), n.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return n, false, fmt.Errorf("sqlite: add notification: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Collapsed onto an existing dedup key — return that row.
		existing, err := s.notificationByDedup(ctx, n.DedupKey)
		if err != nil {
			return n, false, err
		}
		return existing, false, nil
	}
	s.pruneNotifications(ctx)
	return n, true, nil
}

func (s *Store) notificationByDedup(ctx context.Context, key string) (datamodel.Notification, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, supplier_id, supplier_name, type, severity, title, body, dedup_key, is_read, created_at
		 FROM notifications WHERE dedup_key = ?`, key)
	n, err := scanNotification(row)
	if err != nil {
		return datamodel.Notification{}, fmt.Errorf("sqlite: load notification by dedup: %w", err)
	}
	return n, nil
}

// ListNotifications implements datamodel.NotificationStore (newest first).
func (s *Store) ListNotifications(ctx context.Context, q datamodel.NotificationQuery) ([]datamodel.Notification, error) {
	q.Normalize()
	where := ""
	args := []any{}
	if q.UnreadOnly {
		where = " WHERE is_read = 0"
	}
	args = append(args, q.Limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, supplier_id, supplier_name, type, severity, title, body, dedup_key, is_read, created_at
FROM notifications`+where+`
ORDER BY created_at DESC, id DESC
LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list notifications: %w", err)
	}
	defer rows.Close()
	out := make([]datamodel.Notification, 0, q.Limit)
	for rows.Next() {
		var n datamodel.Notification
		var read int
		var created string
		if err := rows.Scan(&n.ID, &n.SupplierID, &n.SupplierName, &n.Type, &n.Severity,
			&n.Title, &n.Body, &n.DedupKey, &read, &created); err != nil {
			return nil, fmt.Errorf("sqlite: scan notification: %w", err)
		}
		n.Read = read != 0
		if n.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("sqlite: parse notification time: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CountUnreadNotifications implements datamodel.NotificationStore.
func (s *Store) CountUnreadNotifications(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE is_read = 0`).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: count unread notifications: %w", err)
	}
	return n, nil
}

// MarkNotificationRead implements datamodel.NotificationStore.
func (s *Store) MarkNotificationRead(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET is_read = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: mark notification read: %w", err)
	}
	return nil
}

// MarkAllNotificationsRead implements datamodel.NotificationStore.
func (s *Store) MarkAllNotificationsRead(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET is_read = 1 WHERE is_read = 0`); err != nil {
		return fmt.Errorf("sqlite: mark all notifications read: %w", err)
	}
	return nil
}

// pruneNotifications keeps only the newest KeepNotifications rows so years
// of daily sweeps cannot grow the table without bound.
func (s *Store) pruneNotifications(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `
DELETE FROM notifications
 WHERE id NOT IN (
     SELECT id FROM notifications ORDER BY created_at DESC, id DESC LIMIT ?
 )`, datamodel.KeepNotifications)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rowScanner lets the single scan helper serve *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNotification(row rowScanner) (datamodel.Notification, error) {
	var n datamodel.Notification
	var read int
	var created string
	if err := row.Scan(&n.ID, &n.SupplierID, &n.SupplierName, &n.Type, &n.Severity,
		&n.Title, &n.Body, &n.DedupKey, &read, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return n, datamodel.ErrNotFound
		}
		return n, err
	}
	n.Read = read != 0
	var err error
	if n.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return n, err
	}
	return n, nil
}
