package backup_test

// Tests for staged in-app restore (应用内恢复): validate a backup zip,
// stage it without touching live data, then swap at next boot with an
// automatic rollback copy. Uses a stub database checker (the SQLite
// adapter's real CheckSnapshot is tested in the sqlite package) so these
// tests pin only the file mechanics: extraction confinement (zip-slip),
// manifest validation, apply/rollback and cancel.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/backup"
)

// writeArchive builds a backup zip in memory with the given DB bytes,
// manifest and (optionally) attachment files.
func writeArchive(t *testing.T, db []byte, m backup.Manifest, atts map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name string, data []byte) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	put(backup.DBName, db)
	for name, data := range atts {
		put(backup.AttachmentsPrefix+name, data)
	}
	raw, _ := json.Marshal(m)
	put(backup.ManifestName, raw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func validManifest() backup.Manifest {
	return backup.Manifest{
		Format:        backup.ArchiveFormat,
		Version:       backup.ArchiveVersion,
		Database:      backup.DBName,
		Attachments:   backup.AttachmentsPrefix,
		AttachmentCnt: 1,
	}
}

func acceptChecker(_ string) error { return nil }

func seedLiveLibrary(t *testing.T, dataDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, "supplider.db"),
		[]byte("LIVE-DATABASE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "supplider.db-wal"),
		[]byte("LIVE-WAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "attachments", "sup_1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "attachments", "sup_1", "old.txt"),
		[]byte("old attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStageApplySwapAndRollback(t *testing.T) {
	dataDir := t.TempDir()
	seedLiveLibrary(t, dataDir)

	zipBytes := writeArchive(t, []byte("RESTORED-DATABASE"), validManifest(),
		map[string][]byte{"sup_2/new.txt": []byte("new attachment")})

	// Stage: nothing live may move yet.
	m, err := backup.Stage(bytes.NewReader(zipBytes), dataDir, acceptChecker)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if m.Format != backup.ArchiveFormat {
		t.Fatalf("manifest round-trip wrong: %+v", m)
	}
	if got, _ := os.ReadFile(filepath.Join(dataDir, "supplider.db")); string(got) != "LIVE-DATABASE" {
		t.Fatalf("live db changed during staging: %q", got)
	}

	pending, staged, err := backup.PendingRestore(dataDir)
	if err != nil || !staged || pending.AttachmentCnt != 1 {
		t.Fatalf("PendingRestore wrong: %+v staged=%v err=%v", pending, staged, err)
	}

	// Apply at "next boot".
	if _, err := backup.ApplyPendingRestore(dataDir); err != nil {
		t.Fatalf("ApplyPendingRestore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dataDir, "supplider.db"))
	if err != nil || string(got) != "RESTORED-DATABASE" {
		t.Fatalf("restored db not live: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "supplider.db-wal")); !os.IsNotExist(err) {
		t.Fatal("stale WAL must not survive a restore")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "restore.staging")); !os.IsNotExist(err) {
		t.Fatal("staging dir must be consumed")
	}
	newAtt := filepath.Join(dataDir, "attachments", "sup_2", "new.txt")
	if b, err := os.ReadFile(newAtt); err != nil || string(b) != "new attachment" {
		t.Fatalf("restored attachment missing: %v err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "attachments", "sup_1", "old.txt")); !os.IsNotExist(err) {
		t.Fatal("old attachment must be replaced by the archive, not merged")
	}

	// The pre-restore library sits in exactly one rollback directory.
	rollbacks, _ := filepath.Glob(filepath.Join(dataDir, "restore.rollback-*"))
	if len(rollbacks) != 1 {
		t.Fatalf("want 1 rollback dir, got %v", rollbacks)
	}
	b, err := os.ReadFile(filepath.Join(rollbacks[0], "supplider.db"))
	if err != nil || string(b) != "LIVE-DATABASE" {
		t.Fatalf("rollback db wrong: %q err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(rollbacks[0], "attachments", "sup_1", "old.txt")); err != nil {
		t.Fatalf("rollback attachments missing: %v", err)
	}

	// Second boot with no staging: no-op.
	if _, err := backup.ApplyPendingRestore(dataDir); err != nil {
		t.Fatalf("idempotent boot: %v", err)
	}
}

func TestStageReplacesPreviousStagingAndCancel(t *testing.T) {
	dataDir := t.TempDir()

	z1 := writeArchive(t, []byte("DB-1"), validManifest(), nil)
	if _, err := backup.Stage(bytes.NewReader(z1), dataDir, acceptChecker); err != nil {
		t.Fatal(err)
	}
	z2 := writeArchive(t, []byte("DB-2"), validManifest(), nil)
	if _, err := backup.Stage(bytes.NewReader(z2), dataDir, acceptChecker); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.ApplyPendingRestore(dataDir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dataDir, "supplider.db")); string(b) != "DB-2" {
		t.Fatalf("latest staging must win, got %q", b)
	}

	// Cancel: stage again, cancel, boot must be a no-op.
	z3 := writeArchive(t, []byte("DB-3"), validManifest(), nil)
	if _, err := backup.Stage(bytes.NewReader(z3), dataDir, acceptChecker); err != nil {
		t.Fatal(err)
	}
	if err := backup.CancelRestore(dataDir); err != nil {
		t.Fatal(err)
	}
	if _, staged, _ := backup.PendingRestore(dataDir); staged {
		t.Fatal("cancel must remove pending restore")
	}
	if _, err := backup.ApplyPendingRestore(dataDir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dataDir, "supplider.db")); string(b) != "DB-2" {
		t.Fatalf("canceled restore must not alter live data, got %q", b)
	}
}

func TestStageRejectsBadArchives(t *testing.T) {
	dataDir := t.TempDir()

	cases := map[string][]byte{
		"not a zip":          []byte("definitely not a zip"),
		"zip-slip traversal": zipSlipArchive(t),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := backup.Stage(bytes.NewReader(body), dataDir, acceptChecker); err == nil {
				t.Fatal("bad archive must be rejected")
			}
			if _, staged, _ := backup.PendingRestore(dataDir); staged {
				t.Fatal("failed stage must leave no pending restore")
			}
		})
	}

	// Valid zip, but manifest/format/version wrong.
	bad := validManifest()
	bad.Format = "something-else"
	body := writeArchive(t, []byte("x"), bad, nil)
	if _, err := backup.Stage(bytes.NewReader(body), dataDir, acceptChecker); err == nil {
		t.Fatal("unknown format must be rejected")
	}
	bad = validManifest()
	bad.Version = 999
	body = writeArchive(t, []byte("x"), bad, nil)
	if _, err := backup.Stage(bytes.NewReader(body), dataDir, acceptChecker); err == nil ||
		!strings.Contains(err.Error(), "version") {
		t.Fatalf("unsupported version must be rejected with a clear error, got %v", err)
	}

	// Valid archive rejected by the adapter's database checker.
	body = writeArchive(t, []byte("corrupt db"), validManifest(), nil)
	_, err := backup.Stage(bytes.NewReader(body), dataDir, func(_ string) error {
		return os.ErrInvalid
	})
	if err == nil || !strings.Contains(err.Error(), "validation") {
		t.Fatalf("checker failure must reject staging, got %v", err)
	}
}

func TestStageRequiresDataDirAndChecker(t *testing.T) {
	if _, err := backup.Stage(bytes.NewReader(nil), "", acceptChecker); err == nil {
		t.Fatal("empty data dir must error")
	}
	if _, err := backup.Stage(bytes.NewReader(nil), t.TempDir(), nil); err == nil {
		t.Fatal("nil checker must error")
	}
}

// zipSlipArchive builds a zip whose DB entry escapes the extraction root.
func zipSlipArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../evil-supplider.db")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("evil"))
	w2, _ := zw.Create(backup.ManifestName)
	_, _ = w2.Write([]byte("{}"))
	_ = zw.Close()
	return buf.Bytes()
}
