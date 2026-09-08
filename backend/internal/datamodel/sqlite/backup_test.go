package sqlite_test

// SnapshotTo (VACUUM INTO) backs up a LIVE database: writes made before the
// snapshot are present in the standalone copy; the copy is independent
// (opens on its own with no -wal/-shm sidecars).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
)

func TestSnapshotToConsistentCopy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := sqlite.Open(filepath.Join(dir, "supplider.db"))
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer store.Close()

	doc := &domain.Supplier{
		ID:        "sup_backup_0001",
		Status:    domain.StatusActive,
		BasicInfo: domain.BasicInfo{CompanyName: "备份验证有限公司", Region: domain.Region{Province: "浙江", City: "杭州"}},
	}
	if err := store.Put(ctx, doc); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.PutSetting(ctx, "visibility_policy", `{"max_level":0,"buffer_days":7}`); err != nil {
		t.Fatalf("put setting: %v", err)
	}

	snapPath := filepath.Join(dir, "snapshot.db")
	if err := store.SnapshotTo(ctx, snapPath); err != nil {
		t.Fatalf("SnapshotTo: %v", err)
	}

	// Open the snapshot as its own database and verify content.
	snap, err := sqlite.Open(snapPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()

	got, err := snap.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("snapshot missing supplier: %v", err)
	}
	if got.BasicInfo.CompanyName != "备份验证有限公司" {
		t.Errorf("snapshot supplier mismatch: %s", got.BasicInfo.CompanyName)
	}
	v, err := snap.GetSetting(ctx, "visibility_policy")
	if err != nil || v != `{"max_level":0,"buffer_days":7}` {
		t.Errorf("snapshot setting mismatch: %q err=%v", v, err)
	}

	// Write to the source AFTER the snapshot must not leak into the copy.
	if err := store.PutSetting(ctx, "visibility_policy", `{"max_level":1}`); err != nil {
		t.Fatalf("update setting: %v", err)
	}
	v, _ = snap.GetSetting(ctx, "visibility_policy")
	if v != `{"max_level":0,"buffer_days":7}` {
		t.Errorf("snapshot must be a point-in-time copy, got %q", v)
	}
}
