package contract

// NotificationStore contract: the same suite runs against the memory and
// SQLite adapters (and, later, MongoDB), pinning dedup, ordering, unread
// counts and read-state semantics across every implementation.

import (
	"context"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
)

// RunNotificationStoreTests executes the NotificationStore contract against
// a fresh store. The value is also asserted to implement NotificationStore.
func RunNotificationStoreTests(t *testing.T, store datamodel.SupplierStore) {
	ns, ok := store.(datamodel.NotificationStore)
	if !ok {
		t.Fatalf("%T does not implement datamodel.NotificationStore", store)
	}
	t.Run("AddListOrderAndUnread", func(t *testing.T) { testNotifAddList(t, ns) })
	t.Run("DedupKeyCollapses", func(t *testing.T) { testNotifDedup(t, ns) })
	t.Run("ReadState", func(t *testing.T) { testNotifReadState(t, ns) })
}

func testNotifAddList(t *testing.T, ns datamodel.NotificationStore) {
	ctx := context.Background()
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	mk := func(id string, minutes int) datamodel.Notification {
		return datamodel.Notification{
			ID: id, SupplierID: "sup_x", SupplierName: "测试有限公司",
			Type: datamodel.NotifBlacklisted, Severity: datamodel.SeverityDanger,
			Title: "t" + id, CreatedAt: base.Add(time.Duration(minutes) * time.Minute),
		}
	}
	if err := ns.MarkAllNotificationsRead(ctx); err != nil { // harmless on empty
		t.Fatal(err)
	}
	if _, n, err := ns.AddNotification(ctx, mk("n1", 1)); err != nil || !n {
		t.Fatalf("add n1: inserted=%v err=%v", n, err)
	}
	if _, n, err := ns.AddNotification(ctx, mk("n2", 2)); err != nil || !n {
		t.Fatalf("add n2: inserted=%v err=%v", n, err)
	}

	got, err := ns.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "n2" || got[1].ID != "n1" {
		t.Fatalf("want newest-first [n2 n1], got %v", ids(got))
	}
	if c, _ := ns.CountUnreadNotifications(ctx); c != 2 {
		t.Fatalf("want 2 unread, got %d", c)
	}

	// Limit is honored.
	got, _ = ns.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 1})
	if len(got) != 1 || got[0].ID != "n2" {
		t.Fatalf("limit 1 must return only n2, got %v", ids(got))
	}
}

func testNotifDedup(t *testing.T, ns datamodel.NotificationStore) {
	ctx := context.Background()
	first := datamodel.Notification{
		ID: "d1", Type: datamodel.NotifQualExpiry, DedupKey: "exp:sup_x:CERT1:7d",
		Title: "first", CreatedAt: time.Now().UTC(),
	}
	stored, inserted, err := ns.AddNotification(ctx, first)
	if err != nil || !inserted {
		t.Fatalf("first dedup insert: inserted=%v err=%v", inserted, err)
	}
	// A repeat with a different ID but the SAME key must collapse.
	repeat := first
	repeat.ID = "d2"
	repeat.Title = "second"
	stored2, inserted2, err := ns.AddNotification(ctx, repeat)
	if err != nil || inserted2 {
		t.Fatalf("repeat must not insert: inserted=%v err=%v", inserted2, err)
	}
	if stored2.ID != stored.ID || stored2.Title != "first" {
		t.Fatalf("repeat must return the existing row, got %+v", stored2)
	}
	got, _ := ns.ListNotifications(ctx, datamodel.NotificationQuery{Limit: 50})
	var count int
	for _, n := range got {
		if n.DedupKey == "exp:sup_x:CERT1:7d" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("dedup key must appear exactly once, got %d", count)
	}
}

func testNotifReadState(t *testing.T, ns datamodel.NotificationStore) {
	ctx := context.Background()
	now := time.Now().UTC()
	add := func(id string) {
		t.Helper()
		if _, _, err := ns.AddNotification(ctx, datamodel.Notification{
			ID: id, Type: datamodel.NotifArchived, Title: id, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("r1")
	add("r2")

	if err := ns.MarkNotificationRead(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := ns.CountUnreadNotifications(ctx); c < 1 {
		t.Fatalf("after one read want >=1 unread (other suite rows may exist), got %d", c)
	}
	unread, _ := ns.ListNotifications(ctx, datamodel.NotificationQuery{UnreadOnly: true, Limit: 50})
	for _, n := range unread {
		if n.ID == "r1" {
			t.Fatal("r1 marked read must not appear in unread list")
		}
	}

	if err := ns.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatal(err)
	}
	if c, _ := ns.CountUnreadNotifications(ctx); c != 0 {
		t.Fatalf("after mark-all want 0 unread, got %d", c)
	}
}

func ids(ns []datamodel.Notification) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.ID
	}
	return out
}
