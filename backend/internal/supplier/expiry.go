// Expiry reminders (资质到期提醒): the non-AI maintenance scan for the
// supplier lifecycle "维护" phase. Every qualification carries an ISO
// expiry date; this module scans active suppliers and flags certificates
// that are already expired or fall inside the PRD reminder windows
// (提前 90/30/7 天). This is the degradable base path — the [AI]/API
// 工商变更监控 layer later pushes changes; local expiry scanning needs no
// network and no keys.
package supplier

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
)

// Reminder windows per PRD: 资质到期提醒（提前 90/30/7 天）.
const (
	DefaultExpiryWindow = 90 // outermost window, days

	BucketExpired = "expired" // 已过期 (days_left < 0)
	Bucket7d      = "7d"      // 7 天内到期
	Bucket30d     = "30d"     // 30 天内到期
	Bucket90d     = "90d"     // 90 天内到期
)

// ExpiryAlert is one certificate needing attention (过期 or 临期).
type ExpiryAlert struct {
	SupplierID   string `json:"supplier_id"`
	SupplierName string `json:"supplier_name"`
	Province     string `json:"province,omitempty"`
	City         string `json:"city,omitempty"`
	QualType     string `json:"qual_type"`
	QualLevel    string `json:"qual_level,omitempty"`
	CertNo       string `json:"cert_no,omitempty"`
	// Expiry is the raw stored date string (ISO date), shown verbatim.
	Expiry string `json:"expiry"`
	// DaysLeft: days from today to the expiry date. Negative = expired
	// that many days ago; 0 = expires today.
	DaysLeft int    `json:"days_left"`
	Bucket   string `json:"bucket"` // expired | 7d | 30d | 90d
}

// WithClock overrides the time source. Production uses wall-clock UTC;
// tests inject a frozen clock so reminder windows are deterministic.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// ExpiringQualifications scans ACTIVE suppliers (keyset-paginated full
// documents, same walk pattern as Export) and returns every qualification
// whose expiry date is within withinDays or already past. Unparseable or
// empty expiry strings are silently skipped (they cannot drive a reminder).
// Results are sorted by urgency: most overdue first, then nearest expiry.
//
// Archived suppliers are excluded by the default filter — history is
// retained but no longer generates maintenance noise.
func (s *Service) ExpiringQualifications(ctx context.Context, withinDays int) ([]ExpiryAlert, error) {
	if withinDays <= 0 {
		withinDays = DefaultExpiryWindow
	}
	today := truncToDate(s.now())

	alerts := make([]ExpiryAlert, 0)
	cursor := ""
	for {
		page, err := s.store.List(ctx, datamodel.Query{
			// Empty filter: live (non-archived) records only.
			Limit:  datamodel.MaxPageSize,
			Cursor: cursor,
			Sort:   datamodel.Sort{Field: datamodel.SortCreatedAt, Order: datamodel.OrderDesc},
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range page.Items {
			for _, q := range doc.Qualifications {
				exp, ok := parseExpiryDate(q.Expiry)
				if !ok {
					continue
				}
				daysLeft := int(exp.Sub(today).Hours() / 24)
				if daysLeft > withinDays {
					continue
				}
				alerts = append(alerts, ExpiryAlert{
					SupplierID:   doc.ID,
					SupplierName: doc.BasicInfo.CompanyName,
					Province:     doc.BasicInfo.Region.Province,
					City:         doc.BasicInfo.Region.City,
					QualType:     strings.TrimSpace(q.Type),
					QualLevel:    q.Level,
					CertNo:       q.CertNo,
					Expiry:       q.Expiry,
					DaysLeft:     daysLeft,
					Bucket:       bucketFor(daysLeft),
				})
			}
		}
		if page.NextCursor == "" || len(alerts) >= MaxExportDocs {
			break
		}
		cursor = page.NextCursor
	}

	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].DaysLeft != alerts[j].DaysLeft {
			return alerts[i].DaysLeft < alerts[j].DaysLeft
		}
		if alerts[i].SupplierName != alerts[j].SupplierName {
			return alerts[i].SupplierName < alerts[j].SupplierName
		}
		return alerts[i].QualType < alerts[j].QualType
	})
	return alerts, nil
}

// bucketFor maps a days-left figure to the PRD reminder window.
func bucketFor(daysLeft int) string {
	switch {
	case daysLeft < 0:
		return BucketExpired
	case daysLeft <= 7:
		return Bucket7d
	case daysLeft <= 30:
		return Bucket30d
	default:
		return Bucket90d
	}
}

// truncToDate normalizes an instant to UTC midnight — day differences are
// calendar-day differences, unaffected by hour/timezone.
func truncToDate(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// expiryDateLayouts lists the accepted date spellings, most canonical
// first. Chinese users frequently type "2026/10/1", "2026.10.1" or
// "2026年10月1日"; rejecting these silently meant the certificate never
// raised a 90/30/7 reminder, so every common spelling is accepted. Go's
// numeric layouts also accept the zero-padded form, so "2006-1-2" covers
// "2026-01-02" too.
var expiryDateLayouts = []string{
	"2006-1-2",
	"2006/1/2",
	"2006.1.2",
	"2006年1月2日",
}

// parseExpiryDate accepts the stored ISO date ("2026-10-01"), common
// Chinese-locale spellings (slash/dot/年月日, zero padding optional) and,
// defensively, a full RFC3339 timestamp, returning UTC midnight. Empty or
// unparseable strings report false — the reminder scan skips them instead
// of failing the whole batch.
func parseExpiryDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range expiryDateLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, true
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return truncToDate(t), true
	}
	return time.Time{}, false
}
