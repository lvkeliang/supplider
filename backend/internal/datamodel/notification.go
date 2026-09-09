package datamodel

import (
	"context"
	"time"
)

// Notification is one in-app alert about a followed supplier (关注/变更推送).
// The personal tier raises these for watched suppliers and surfaces them in
// an in-app bell; the enterprise tier can fan the same payload out to
// 钉钉/企微. Notifications are stored separately from supplier documents so
// archiving/deleting a supplier never loses its history.
//
// DedupKey makes repeated sweeps idempotent: two notifications with the same
// non-empty key collapse to the first. Discrete user actions (blacklist,
// archive…) leave DedupKey empty so each action alerts once.
type Notification struct {
	ID string `json:"id"`
	// SupplierID/Name identify the subject; Name is denormalized so the feed
	// still renders after the document is archived or merged away.
	SupplierID   string `json:"supplier_id"`
	SupplierName string `json:"supplier_name"`

	// Type is a stable machine code (see NotificationType* constants).
	Type string `json:"type"`
	// Severity is one of info | warning | danger, driving UI color.
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`

	DedupKey  string    `json:"dedup_key,omitempty"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

// Notification types (变更类型).
const (
	NotifRiskFlagged   = "risk_flagged"  // 空壳风险信号新增
	NotifBlacklisted   = "blacklisted"   // 列入黑名单
	NotifUnblacklisted = "unblacklisted" // 移出黑名单
	NotifArchived      = "archived"      // 归档
	NotifRestored      = "restored"      // 恢复
	NotifMerged        = "merged"        // 主档案吸收了重复档案
	NotifMergedAway    = "merged_away"   // 被关注的档案并入其他主档案并归档
	NotifQualExpiry    = "qual_expiry"   // 资质临期/过期（按窗口去重）
)

// Severity levels.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityDanger  = "danger"
)

// KeepNotifications bounds feed growth: adapters prune oldest read and then
// oldest overall notifications beyond this many. Personal libraries are
// small; this only prevents runaway accumulation over years of sweeps.
const KeepNotifications = 500

// NotificationStore is the persistence port for the notification feed.
// Both adapters implement it alongside SupplierStore; business code depends
// on the interface, never on the driver.
type NotificationStore interface {
	// AddNotification inserts n (assigning ID/CreatedAt when zero). When
	// n.DedupKey is non-empty and already present, no row is inserted and
	// inserted=false with the existing notification returned.
	AddNotification(ctx context.Context, n Notification) (stored Notification, inserted bool, err error)

	// ListNotifications returns recent notifications, newest first. Limit
	// is capped at MaxPageSize; UnreadOnly filters to unread.
	ListNotifications(ctx context.Context, q NotificationQuery) ([]Notification, error)

	// CountUnreadNotifications returns the number of unread notifications.
	CountUnreadNotifications(ctx context.Context) (int, error)

	// MarkNotificationRead flags one notification read (idempotent).
	MarkNotificationRead(ctx context.Context, id string) error

	// MarkAllNotificationsRead clears the unread state of every notification.
	MarkAllNotificationsRead(ctx context.Context) error
}

// NotificationQuery is one feed request.
type NotificationQuery struct {
	UnreadOnly bool
	Limit      int
}

// Normalize clamps the limit into [1, MaxPageSize].
func (q *NotificationQuery) Normalize() {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Limit > MaxPageSize {
		q.Limit = MaxPageSize
	}
}
