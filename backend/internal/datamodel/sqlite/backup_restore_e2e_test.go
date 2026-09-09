package sqlite_test

// End-to-end data-safety path: VACUUM INTO snapshot + attachments + manifest
// produced by backup.WriteArchive, staged with the REAL checker, swapped in
// by ApplyPendingRestore at "next boot", then reopened — with the previous
// library preserved in a rollback directory. The backup package unit tests
// use stubs; this proves the concrete adapters compose without data loss.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/supplider/supplider/backend/internal/backup"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
)

func TestBackupStageApplyRoundTripWithRealSQLite(t *testing.T) {
	ctx := context.Background()

	// --- source "machine": a populated library with an attachment ---
	srcDir := t.TempDir()
	src, err := sqlite.Open(filepath.Join(srcDir, "supplider.db"))
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	doc := &domain.Supplier{
		ID:        "sup_restore_e2e",
		Status:    domain.StatusActive,
		BasicInfo: domain.BasicInfo{CompanyName: "恢复链路验证有限公司", Region: domain.Region{Province: "浙江", City: "杭州"}},
	}
	if err := src.Put(ctx, doc); err != nil {
		t.Fatalf("put supplier: %v", err)
	}
	if err := src.PutSetting(ctx, "visibility_policy", `{"max_level":1,"buffer_days":7}`); err != nil {
		t.Fatalf("put setting: %v", err)
	}
	attRoot := filepath.Join(srcDir, "attachments")
	attRel := filepath.Join("sup_restore_e2e", "license.txt")
	if err := os.MkdirAll(filepath.Join(attRoot, filepath.Dir(attRel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attRoot, attRel), []byte("cert-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	// --- produce the backup zip while a (reopened) live DB exists ---
	live, err := sqlite.Open(filepath.Join(srcDir, "supplider.db"))
	if err != nil {
		t.Fatalf("reopen source: %v", err)
	}
	var archive bytes.Buffer
	if _, err := backup.WriteArchive(ctx, &archive, live.SnapshotTo, attRoot, nil); err != nil {
		t.Fatalf("WriteArchive: %v", err)
	}
	live.Close()

	// --- target "machine" with its own older library (rollback path) ---
	dstDir := t.TempDir()
	dst, err := sqlite.Open(filepath.Join(dstDir, "supplider.db"))
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	old := &domain.Supplier{
		ID:        "sup_old_local",
		Status:    domain.StatusActive,
		BasicInfo: domain.BasicInfo{CompanyName: "旧库本地公司", Region: domain.Region{Province: "江苏", City: "南京"}},
	}
	if err := dst.Put(ctx, old); err != nil {
		t.Fatalf("put old supplier: %v", err)
	}
	if err := dst.Close(); err != nil {
		t.Fatalf("close target: %v", err)
	}

	// Stage with the real adapter checker.
	if _, err := backup.Stage(bytes.NewReader(archive.Bytes()), dstDir, sqlite.CheckSnapshot); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, staged, err := backup.PendingRestore(dstDir); err != nil || !staged {
		t.Fatalf("pending restore not found: staged=%v err=%v", staged, err)
	}

	// "Reboot": apply before opening any DB connection.
	if _, err := backup.ApplyPendingRestore(dstDir); err != nil {
		t.Fatalf("ApplyPendingRestore: %v", err)
	}
	if _, staged, _ := backup.PendingRestore(dstDir); staged {
		t.Fatal("staging must be consumed after apply")
	}

	// The restored library is live and complete.
	restored, err := sqlite.Open(filepath.Join(dstDir, "supplider.db"))
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer restored.Close()
	got, err := restored.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("restored supplier missing: %v", err)
	}
	if got.BasicInfo.CompanyName != "恢复链路验证有限公司" {
		t.Errorf("restored name = %q", got.BasicInfo.CompanyName)
	}
	if v, _ := restored.GetSetting(ctx, "visibility_policy"); v != `{"max_level":1,"buffer_days":7}` {
		t.Errorf("restored setting = %q", v)
	}
	if _, err := restored.Get(ctx, old.ID); err == nil {
		t.Error("old library supplier must not survive the swap (it lives in rollback)")
	}

	// Attachment came across and is readable.
	attBytes, err := os.ReadFile(filepath.Join(dstDir, "attachments", attRel))
	if err != nil {
		t.Fatalf("restored attachment missing: %v", err)
	}
	if string(attBytes) != "cert-bytes" {
		t.Errorf("attachment content = %q", attBytes)
	}

	// Exactly one rollback directory keeps the pre-restore library.
	entries, _ := os.ReadDir(dstDir)
	rollbacks := 0
	for _, e := range entries {
		if e.IsDir() && bytes.HasPrefix([]byte(e.Name()), []byte("restore.rollback-")) {
			rollbacks++
			if _, err := os.Stat(filepath.Join(dstDir, e.Name(), "supplider.db")); err != nil {
				t.Errorf("rollback %s missing supplider.db: %v", e.Name(), err)
			}
		}
	}
	if rollbacks != 1 {
		t.Errorf("want exactly 1 rollback dir, got %d", rollbacks)
	}
}
