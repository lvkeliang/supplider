package supplier_test

// Tests for the qualification-expiry maintenance scan (资质到期提醒).
// The clock is frozen so the 90/30/7-day windows are deterministic; the
// same test would run unchanged against a future MongoDB adapter since it
// exercises only the supplier.Service API over the SupplierStore port.

import (
	"context"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// frozen is a fixed "today": 2026-09-08 UTC.
var frozenClock = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

// expirySvc builds a service on a memory store with a frozen clock.
func expirySvc() *supplier.Service {
	return supplier.NewService(memory.New()).
		WithClock(func() time.Time { return frozenClock })
}

// mkSupplierWithQuals creates one active supplier carrying the given
// qualifications (expiry strings relative to the frozen clock).
func mkSupplierWithQuals(t *testing.T, svc *supplier.Service, name string, quals ...domain.Qualification) string {
	t.Helper()
	in := validInput()
	in.BasicInfo.CompanyName = name
	in.Qualifications = quals
	doc, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return doc.ID
}

// isoDate returns an ISO date n days from the frozen clock.
func isoDate(days int) string {
	return frozenClock.AddDate(0, 0, days).Format("2006-01-02")
}

func TestExpiringQualificationsWindowsAndOrder(t *testing.T) {
	svc := expirySvc()
	ctx := context.Background()

	mkSupplierWithQuals(t, svc, "过期公司", domain.Qualification{Type: "施工总承包", Level: "一级", Expiry: isoDate(-19)})
	mkSupplierWithQuals(t, svc, "七天内公司", domain.Qualification{Type: "专业承包", Level: "二级", Expiry: isoDate(5)})
	mkSupplierWithQuals(t, svc, "三十天内公司", domain.Qualification{Type: "监理", Expiry: isoDate(20)})
	mkSupplierWithQuals(t, svc, "九十天内公司", domain.Qualification{Type: "设计", Expiry: isoDate(60)})
	mkSupplierWithQuals(t, svc, "远期公司", domain.Qualification{Type: "咨询", Expiry: isoDate(200)})

	alerts, err := svc.ExpiringQualifications(ctx, 90)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(alerts) != 4 {
		t.Fatalf("got %d alerts, want 4 (200-day cert excluded): %+v", len(alerts), alerts)
	}

	// Most urgent first: -19, 5, 20, 60.
	wantDays := []int{-19, 5, 20, 60}
	wantBuckets := []string{supplier.BucketExpired, supplier.Bucket7d, supplier.Bucket30d, supplier.Bucket90d}
	for i, a := range alerts {
		if a.DaysLeft != wantDays[i] {
			t.Errorf("alert %d days_left = %d, want %d", i, a.DaysLeft, wantDays[i])
		}
		if a.Bucket != wantBuckets[i] {
			t.Errorf("alert %d (%s) bucket = %q, want %q", i, a.SupplierName, a.Bucket, wantBuckets[i])
		}
	}
}

func TestExpiringQualificationsEdgeAndBadDates(t *testing.T) {
	svc := expirySvc()
	ctx := context.Background()

	// Expires TODAY → 0 days left, 7-day bucket.
	mkSupplierWithQuals(t, svc, "今天到期公司",
		domain.Qualification{Type: "今天到期资质", Expiry: isoDate(0)})
	// Full RFC3339 timestamp instead of a bare date — accepted, normalized.
	mkSupplierWithQuals(t, svc, "时间戳公司",
		domain.Qualification{Type: "时间戳资质", Expiry: frozenClock.AddDate(0, 0, 3).Format(time.RFC3339)})
	// A Chinese-locale slash date (7 days out) is a real date and alerts.
	mkSupplierWithQuals(t, svc, "斜杠日期公司",
		domain.Qualification{Type: "斜杠资质", Expiry: "2026/09/15"})
	// Empty and genuinely unparseable expiry strings — skipped, never error
	// the scan (long-term certs / free-form notes).
	mkSupplierWithQuals(t, svc, "无到期日公司",
		domain.Qualification{Type: "长期资质", Expiry: ""},
		domain.Qualification{Type: "乱写资质", Expiry: "长期有效"})

	alerts, err := svc.ExpiringQualifications(ctx, 0) // 0 → default 90-day window
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(alerts) != 3 {
		t.Fatalf("got %d alerts, want 3 (today + RFC3339 + slash date; bad/empty skipped): %+v", len(alerts), alerts)
	}
	if alerts[0].DaysLeft != 0 || alerts[0].Bucket != supplier.Bucket7d {
		t.Errorf("today cert: days=%d bucket=%q, want 0 / %s", alerts[0].DaysLeft, alerts[0].Bucket, supplier.Bucket7d)
	}
	if alerts[1].DaysLeft != 3 {
		t.Errorf("rfc3339 cert days = %d, want 3", alerts[1].DaysLeft)
	}
	if alerts[2].DaysLeft != 7 || alerts[2].Expiry != "2026/09/15" {
		t.Errorf("slash-date cert = %+v, want 7 days left with raw string preserved", alerts[2])
	}
}

// TestExpiryAcceptsChineseDateSpellings proves every common Chinese-locale
// date spelling drives reminders (they used to be silently skipped, so the
// certificate never raised a 90/30/7 alert). All resolve to the same calendar
// day; the raw stored string is shown verbatim in the alert.
func TestExpiryAcceptsChineseDateSpellings(t *testing.T) {
	svc := expirySvc()
	ctx := context.Background()
	// Every spelling resolves to frozen clock + 10 days = 2026-09-18.
	spellings := map[string]string{
		"ISO 零填充": "2026-09-18",
		"ISO 非填充": "2026-9-18",
		"斜杠零填充":   "2026/09/18",
		"斜杠非填充":   "2026/9/18",
		"点分":      "2026.09.18",
		"中文年月日":   "2026年09月18日",
		"中文非填充":   "2026年9月18日",
	}
	for label, s := range spellings {
		mkSupplierWithQuals(t, svc, "日期写法公司"+label,
			domain.Qualification{Type: "资质", Expiry: s})
	}
	alerts, err := svc.ExpiringQualifications(ctx, 90)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(alerts) != len(spellings) {
		t.Fatalf("got %d alerts, want %d (every spelling parses): %+v", len(alerts), len(spellings), alerts)
	}
	for _, a := range alerts {
		if a.DaysLeft != 10 {
			t.Errorf("%s (%q): days_left = %d, want 10", a.SupplierName, a.Expiry, a.DaysLeft)
		}
	}
}

func TestExpiringQualificationsExcludesArchived(t *testing.T) {
	svc := expirySvc()
	ctx := context.Background()

	id := mkSupplierWithQuals(t, svc, "将归档公司",
		domain.Qualification{Type: "过期资质", Expiry: isoDate(-2)})
	if err := svc.Archive(ctx, id); err != nil {
		t.Fatalf("archive: %v", err)
	}

	alerts, err := svc.ExpiringQualifications(ctx, 90)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("archived supplier generated %d alerts, want 0: %+v", len(alerts), alerts)
	}
}
