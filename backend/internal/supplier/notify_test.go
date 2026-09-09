package supplier_test

// Tests for follow (关注) + in-app change notifications: watched suppliers
// raise feed entries on blacklist/risk-flip/archive/merge and on nearing
// qualification expiry (deduped per window); unwatched suppliers stay silent.
// Uses the memory store, which implements both datamodel ports.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// watchedService seeds one clean supplier, watches it and returns the
// service, store, id and a frozen clock pointer.
func watchedService(t *testing.T) (*supplier.Service, *memory.Store, string) {
	t.Helper()
	st := memory.New()
	svc := supplier.NewService(st)
	ctx := context.Background()
	doc, err := svc.Create(ctx, cleanInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.SetWatched(ctx, doc.ID, true); err != nil {
		t.Fatalf("watch: %v", err)
	}
	return svc, st, doc.ID
}

func TestWatchDoesNotTouchChangeLogOrTimestamp(t *testing.T) {
	svc, _, id := watchedService(t)
	ctx := context.Background()
	before, _ := svc.Get(ctx, id)
	updatedAt := before.UpdatedAt
	changelog := len(before.ChangeLog)

	doc, err := svc.SetWatched(ctx, id, true) // idempotent
	if err != nil || !doc.Watched {
		t.Fatalf("watch: watched=%v err=%v", doc.Watched, err)
	}
	if len(doc.ChangeLog) != changelog || !doc.UpdatedAt.Equal(updatedAt) {
		t.Fatal("watching must add no change_log and must not bump updated_at")
	}
	off, err := svc.SetWatched(ctx, id, false)
	if err != nil || off.Watched {
		t.Fatalf("unwatch: watched=%v err=%v", off.Watched, err)
	}
}

func TestBlacklistNotifiesWatchersOnly(t *testing.T) {
	svc, st, id := watchedService(t)
	ctx := context.Background()

	// An unwatched second supplier blacklists silently.
	other, err := svc.Create(ctx, cleanInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Blacklist(ctx, other.ID, ""); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.CountUnreadNotifications(ctx); c != 0 {
		t.Fatalf("unwatched blacklist must not notify, got %d", c)
	}

	// Blacklisting the watched supplier raises one danger entry.
	if _, err := svc.Blacklist(ctx, id, "确认造假"); err != nil {
		t.Fatal(err)
	}
	feed, err := svc.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 50})
	if err != nil || len(feed) != 1 {
		t.Fatalf("want 1 notification, got %d err=%v", len(feed), err)
	}
	n := feed[0]
	if n.Type != datamodel.NotifBlacklisted || n.Severity != datamodel.SeverityDanger ||
		n.SupplierID != id || !strings.Contains(n.Body, "确认造假") {
		t.Fatalf("wrong notification: %+v", n)
	}
	if c, _ := st.CountUnreadNotifications(ctx); c != 1 {
		t.Fatalf("want 1 unread, got %d", c)
	}

	// Acknowledging clears the badge; mark-all also works.
	if err := svc.MarkNotificationRead(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.CountUnreadNotifications(ctx); c != 0 {
		t.Fatalf("after read want 0 unread, got %d", c)
	}
}

func TestRiskFlipNotifiesOnlyOnNewVerdict(t *testing.T) {
	svc, _, id := watchedService(t)
	ctx := context.Background()

	// Edit to a malformed credit code: R102 (high) flips shell-risk on.
	bi1 := riskyBasic(cleanInput().BasicInfo)
	if _, err := svc.Update(ctx, id, supplier.UpdateInput{BasicInfo: &bi1}); err != nil {
		t.Fatalf("update: %v", err)
	}
	feed, _ := svc.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 50})
	if len(feed) != 1 || feed[0].Type != datamodel.NotifRiskFlagged {
		t.Fatalf("risk flip must raise one risk notification, got %+v", feed)
	}

	// Another risky edit while the verdict already stands raises nothing new.
	bi2 := riskyBasic(cleanInput().BasicInfo)
	bi2.LegalPerson = "李四"
	if _, err := svc.Update(ctx, id, supplier.UpdateInput{BasicInfo: &bi2}); err != nil {
		t.Fatal(err)
	}
	feed, _ = svc.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 50})
	if len(feed) != 1 {
		t.Fatalf("unchanged risk verdict must not renotify, got %d", len(feed))
	}
}

func TestArchiveAndMergeNotify(t *testing.T) {
	svc, _, id := watchedService(t)
	ctx := context.Background()
	if err := svc.Archive(ctx, id); err != nil {
		t.Fatal(err)
	}
	feed, _ := svc.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 50})
	if len(feed) != 1 || feed[0].Type != datamodel.NotifArchived {
		t.Fatalf("archive must notify watcher, got %+v", feed)
	}

	// Merge: watch the master, merge a duplicate → master "merged" notice.
	masterIn := cleanInput()
	masterIn.BasicInfo.CompanyName = "杭州合并主档案有限公司"
	master, _ := svc.Create(ctx, masterIn)
	dupIn := cleanInput()
	dupIn.BasicInfo.CompanyName = "杭州合并重复档案有限公司"
	dup, _ := svc.Create(ctx, dupIn)
	if _, err := svc.SetWatched(ctx, master.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.MergeSuppliers(ctx, master.ID, dup.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	feed, _ = svc.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 50})
	found := false
	for _, n := range feed {
		if n.Type == datamodel.NotifMerged && n.SupplierID == master.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("master watcher must receive a merged notice, feed=%+v", feed)
	}
}

func TestWatchedOnlyFilter(t *testing.T) {
	svc, _, id := watchedService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, cleanInput()); err != nil { // unwatched
		t.Fatal(err)
	}
	page, err := svc.List(ctx, datamodel.Query{
		Filter: datamodel.SupplierFilter{WatchedOnly: true},
		Limit:  100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != id || !page.Items[0].Watched {
		t.Fatalf("watched-only filter must return the single watched supplier, got %+v", page.Items)
	}
}

func TestExpiryNotificationRaisedOncePerWindow(t *testing.T) {
	st := memory.New()
	svc := supplier.NewService(st)
	frozen := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	svc.WithClock(func() time.Time { return frozen })
	ctx := context.Background()

	in := cleanInput()
	in.Qualifications = []domain.Qualification{
		{Type: "建筑工程施工总承包", Level: "一级", CertNo: "CERT-90", Expiry: "2026-12-03"}, // 85 days out
	}
	doc, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetWatched(ctx, doc.ID, true); err != nil {
		t.Fatal(err)
	}

	n, err := svc.NotifyWatchedExpiring(ctx)
	if err != nil || n != 1 {
		t.Fatalf("first sweep (90d window) must raise 1, got n=%d err=%v", n, err)
	}
	feed, _ := svc.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 50})
	if len(feed) != 1 || feed[0].Type != datamodel.NotifQualExpiry || feed[0].Severity != datamodel.SeverityInfo {
		t.Fatalf("want one info (90d) expiry notification, got %+v", feed)
	}
	// Repeated sweeps collapse on the per-window dedup key.
	n, _ = svc.NotifyWatchedExpiring(ctx)
	if n != 0 {
		t.Fatalf("second sweep must be idempotent (0 new), got %d", n)
	}

	// Advance 60 days: the same cert now sits 25 days out → 30d window, a
	// different dedup suffix, so one new notification fires.
	svc.WithClock(func() time.Time { return frozen.Add(60 * 24 * time.Hour) })
	n, _ = svc.NotifyWatchedExpiring(ctx)
	if n != 1 {
		t.Fatalf("crossing into 30-day window must raise once, got %d", n)
	}
}

func TestExpirySkipsUnwatched(t *testing.T) {
	svc := supplier.NewService(memory.New())
	svc.WithClock(func() time.Time { return time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC) })
	ctx := context.Background()
	in := cleanInput()
	in.Qualifications = []domain.Qualification{{Type: "资质", CertNo: "X", Expiry: "2026-09-12"}}
	if _, err := svc.Create(ctx, in); err != nil { // not watched
		t.Fatal(err)
	}
	n, err := svc.NotifyWatchedExpiring(ctx)
	if err != nil || n != 0 {
		t.Fatalf("unwatched expiry must raise nothing, n=%d err=%v", n, err)
	}
}

// riskyBasic returns a copy of b with a malformed credit code that trips the
// high-severity R102 rule (shell-risk), keeping every other field clean.
func riskyBasic(b domain.BasicInfo) domain.BasicInfo {
	b.CreditCode = "BAD" // length != 18 → R102 high
	return b
}
