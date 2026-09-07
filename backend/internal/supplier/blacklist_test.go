package supplier_test

// Tests for the blacklist (黑名单/淘汰) lifecycle: blacklist keeps a supplier
// visible but badged, records reason + change_log, restores via unblacklist,
// and refuses archived suppliers.

import (
	"context"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestBlacklistAndUnblacklist(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	doc, err := svc.Create(ctx, cleanInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bl, err := svc.Blacklist(ctx, doc.ID, "资质造假")
	if err != nil {
		t.Fatalf("Blacklist: %v", err)
	}
	if bl.Status != domain.StatusBlacklisted || bl.BlacklistReason != "资质造假" {
		t.Errorf("blacklist state wrong: status=%s reason=%q", bl.Status, bl.BlacklistReason)
	}

	// Status + reason are recorded in change_log (auditable).
	var sawStatus, sawReason bool
	for _, c := range bl.ChangeLog {
		if c.Field == "status" && c.New == domain.StatusBlacklisted {
			sawStatus = true
		}
		if c.Field == "blacklist_reason" && c.New == "资质造假" {
			sawReason = true
		}
	}
	if !sawStatus || !sawReason {
		t.Errorf("change_log missing status/reason entries: %+v", bl.ChangeLog)
	}

	// Blacklisted suppliers are STILL visible in the default list (they must
	// warn, not disappear) and selectable via status=blacklisted.
	page, err := svc.List(ctx, datamodel.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Status != domain.StatusBlacklisted {
		t.Errorf("default list should keep the blacklisted supplier: %+v", page.Items)
	}
	blPage, err := svc.List(ctx, datamodel.Query{Filter: datamodel.SupplierFilter{Status: domain.StatusBlacklisted}})
	if err != nil || len(blPage.Items) != 1 {
		t.Errorf("status=blacklisted filter: %d items, err=%v", len(blPage.Items), err)
	}

	// Unblacklist returns it to active and clears the reason.
	restored, err := svc.Unblacklist(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Unblacklist: %v", err)
	}
	if restored.Status != domain.StatusActive || restored.BlacklistReason != "" {
		t.Errorf("unblacklist state wrong: status=%s reason=%q", restored.Status, restored.BlacklistReason)
	}
}

func TestBlacklistArchivedRejected(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	doc, _ := svc.Create(ctx, cleanInput())
	if err := svc.Archive(ctx, doc.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := svc.Blacklist(ctx, doc.ID, "x"); err == nil ||
		!strings.Contains(err.Error(), "must be restored") {
		t.Fatalf("blacklisting an archived supplier must error, got %v", err)
	}
}

func TestBlacklistIdempotent(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	doc, _ := svc.Create(ctx, cleanInput())

	first, _ := svc.Blacklist(ctx, doc.ID, "r1")
	second, _ := svc.Blacklist(ctx, doc.ID, "r2") // already blacklisted → no-op
	if len(second.ChangeLog) != len(first.ChangeLog) || second.BlacklistReason != "r1" {
		t.Errorf("second blacklist should be a no-op: %d vs %d log entries",
			len(second.ChangeLog), len(first.ChangeLog))
	}

	// Unblacklist on a non-blacklisted supplier is a no-op.
	restored, _ := svc.Unblacklist(ctx, doc.ID)
	again, err := svc.Unblacklist(ctx, doc.ID)
	if err != nil || again.Status != domain.StatusActive {
		t.Errorf("unblacklist no-op wrong: %+v err=%v", again, err)
	}
	_ = restored
}
