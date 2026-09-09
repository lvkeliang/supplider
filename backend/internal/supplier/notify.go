// Follow (关注) a supplier and the in-app change-notification feed. This is
// the personal-tier, fully-local realization of PRD 维护 "变更推送通知关注者":
// a watched supplier raising an event (new shell-risk signal, blacklist,
// archive, merge, a qualification nearing expiry) creates a notification in
// the bell. Enterprise can later fan the same datamodel.Notification payload
// to 钉钉/企微; no business code changes.
//
// Everything degrades gracefully: when the store offers no NotificationStore
// the helpers no-op, and watching itself works without any AI/network.
package supplier

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
)

// SetWatched follows (true) or unfollows (false) a supplier. Toggling the
// flag is personal UI state — it writes no change_log and does not bump
// UpdatedAt, so lists do not reorder. Archived suppliers may still be
// watched; their feed history is preserved through archive/merge.
func (s *Service) SetWatched(ctx context.Context, id string, watched bool) (*domain.Supplier, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if doc.Watched == watched {
		return doc, nil // idempotent
	}
	doc.Watched = watched
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// notify raises one in-app notification for a watched supplier. It is a
// best-effort side effect: a notification failure must never fail the
// underlying lifecycle action, and nothing is raised for an unwatched
// supplier or a store without a feed. Empty dedup means "always alert"
// (discrete user actions); a non-empty key lets repeated sweeps collapse.
func (s *Service) notify(ctx context.Context, doc *domain.Supplier, typ, severity, title, body, dedup string) {
	if s.notifs == nil || doc == nil || !doc.Watched {
		return
	}
	n := datamodel.Notification{
		ID:           newNotificationID(s.now()),
		SupplierID:   doc.ID,
		SupplierName: doc.BasicInfo.CompanyName,
		Type:         typ,
		Severity:     severity,
		Title:        title,
		Body:         body,
		DedupKey:     dedup,
		CreatedAt:    s.now(),
	}
	if _, _, err := s.notifs.AddNotification(ctx, n); err != nil {
		// Feed write must never break the action that triggered it.
		_ = err
	}
}

// newNotificationID returns an unguessable id (not_<year>_<8 hex>).
func newNotificationID(now time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("not_%d_%d", now.Year(), now.UnixNano())
	}
	return fmt.Sprintf("not_%d_%s", now.Year(), hex.EncodeToString(b[:]))
}

// ---- feed read/ack (thin pass-throughs; nil-safe) ----

// ListNotifications returns recent feed entries (newest first).
func (s *Service) ListNotifications(ctx context.Context, q datamodel.NotificationQuery) ([]datamodel.Notification, error) {
	if s.notifs == nil {
		return []datamodel.Notification{}, nil
	}
	return s.notifs.ListNotifications(ctx, q)
}

// CountUnreadNotifications returns the unread count for the bell badge.
func (s *Service) CountUnreadNotifications(ctx context.Context) (int, error) {
	if s.notifs == nil {
		return 0, nil
	}
	return s.notifs.CountUnreadNotifications(ctx)
}

// MarkNotificationRead acknowledges one notification.
func (s *Service) MarkNotificationRead(ctx context.Context, id string) error {
	if s.notifs == nil {
		return nil
	}
	return s.notifs.MarkNotificationRead(ctx, id)
}

// MarkAllNotificationsRead clears the whole unread badge.
func (s *Service) MarkAllNotificationsRead(ctx context.Context) error {
	if s.notifs == nil {
		return nil
	}
	return s.notifs.MarkAllNotificationsRead(ctx)
}

// NotifyWatchedExpiring scans qualification expiry and raises one
// notification per watched supplier per reminder window (90/30/7 days and
// expired). Dedup keys include the window, so crossing 90→30→7→expired each
// raises once while the daily/boot sweep is otherwise idempotent. Returns
// the number of newly raised notifications.
func (s *Service) NotifyWatchedExpiring(ctx context.Context) (int, error) {
	if s.notifs == nil {
		return 0, nil
	}
	alerts, err := s.ExpiringQualifications(ctx, 90)
	if err != nil {
		return 0, err
	}
	raised := 0
	for i := range alerts {
		a := alerts[i]
		doc, err := s.store.Get(ctx, a.SupplierID)
		if err != nil || !doc.Watched {
			continue
		}
		severity, title := expirySeverityTitle(a.Bucket)
		body := fmt.Sprintf("资质《%s%s》将于 %s 到期", a.QualType, qualLevelLabel(a.QualLevel), a.Expiry)
		switch a.Bucket {
		case BucketExpired:
			body = fmt.Sprintf("资质《%s%s》已于 %s 过期（已过期 %d 天），请尽快更新",
				a.QualType, qualLevelLabel(a.QualLevel), a.Expiry, -a.DaysLeft)
		case Bucket7d:
			body = fmt.Sprintf("资质《%s%s》将于 %s 到期（仅剩 %d 天），请尽快续期",
				a.QualType, qualLevelLabel(a.QualLevel), a.Expiry, a.DaysLeft)
		case Bucket30d, Bucket90d:
			body = fmt.Sprintf("资质《%s%s》将于 %s 到期（剩余 %d 天）",
				a.QualType, qualLevelLabel(a.QualLevel), a.Expiry, a.DaysLeft)
		}
		key := fmt.Sprintf("exp:%s:%s:%s", a.SupplierID, qualIdentity(a), a.Bucket)
		_, inserted, err := s.notifs.AddNotification(ctx, datamodel.Notification{
			ID:           newNotificationID(s.now()),
			SupplierID:   doc.ID,
			SupplierName: doc.BasicInfo.CompanyName,
			Type:         datamodel.NotifQualExpiry,
			Severity:     severity,
			Title:        title,
			Body:         body,
			DedupKey:     key,
			CreatedAt:    s.now(),
		})
		if err != nil {
			return raised, err
		}
		if inserted {
			raised++
		}
	}
	return raised, nil
}

// expirySeverityTitle maps a reminder window to its severity and title.
func expirySeverityTitle(bucket string) (severity, title string) {
	switch bucket {
	case BucketExpired:
		return datamodel.SeverityDanger, "关注供应商资质已过期"
	case Bucket7d:
		return datamodel.SeverityDanger, "关注供应商资质 7 天内到期"
	case Bucket30d:
		return datamodel.SeverityWarning, "关注供应商资质 30 天内到期"
	default:
		return datamodel.SeverityInfo, "关注供应商资质即将到期"
	}
}

// qualIdentity is the stable per-certificate identity used in dedup keys:
// certificate number when present, else type+level+expiry (a re-issued cert
// with a new number/date is correctly treated as a new event).
func qualIdentity(a ExpiryAlert) string {
	if c := strings.TrimSpace(a.CertNo); c != "" {
		return "cert:" + c
	}
	return "q:" + strings.TrimSpace(a.QualType) + "|" + strings.TrimSpace(a.QualLevel) + "|" + a.Expiry
}

// qualLevelLabel renders the level with a leading space when present so
// messages read "建筑工程施工总承包一级" without an orphan gap when absent.
func qualLevelLabel(level string) string {
	if level = strings.TrimSpace(level); level != "" {
		return level
	}
	return ""
}
