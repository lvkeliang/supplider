package backup_test

// The backup archive bundles a DB snapshot, attachment files (preserving
// the <supplier-id>/<file> layout) and a manifest into one zip.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/supplider/supplider/backend/internal/backup"
)

func TestWriteArchiveBundlesSnapshotAttachmentsManifest(t *testing.T) {
	ctx := context.Background()

	// Fake snapshot: write a marker file where VACUUM INTO would.
	snap := func(_ context.Context, destPath string) error {
		return os.WriteFile(destPath, []byte("SNAPSHOT-DB-BYTES"), 0o644)
	}

	// Attachment tree: <root>/<supplier-id>/<file> like localfs layout.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sup_x", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sup_x", "license.jpg"), []byte("JPEG-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sup_x", "sub", "note.txt"), []byte("NOTE"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	m, err := backup.WriteArchive(ctx, &buf, snap, root, map[string]any{"tier": "personal"})
	if err != nil {
		t.Fatalf("WriteArchive: %v", err)
	}
	if m.AttachmentCnt != 2 {
		t.Errorf("attachment count = %d, want 2", m.AttachmentCnt)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	entries := map[string]string{} // name → contents
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[f.Name] = string(data)
	}

	if entries["supplider.db"] != "SNAPSHOT-DB-BYTES" {
		t.Errorf("archive missing DB snapshot content: %q", entries["supplider.db"])
	}
	if entries["attachments/sup_x/license.jpg"] != "JPEG-BYTES" {
		t.Errorf("attachment missing from archive: have %v", keys(entries))
	}
	if entries["attachments/sup_x/sub/note.txt"] != "NOTE" {
		t.Errorf("nested attachment missing: have %v", keys(entries))
	}
	var manifest backup.Manifest
	if raw, ok := entries["manifest.json"]; !ok {
		t.Errorf("manifest.json missing")
	} else if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Errorf("manifest invalid: %v", err)
	} else {
		if manifest.Format != backup.ArchiveFormat || manifest.Version != backup.ArchiveVersion {
			t.Errorf("manifest identity wrong: %s v%d", manifest.Format, manifest.Version)
		}
		if manifest.Extra["tier"] != "personal" {
			t.Errorf("manifest extra not carried through: %+v", manifest.Extra)
		}
	}
}

// A library with no attachment directory still produces a valid archive
// containing the database and manifest.
func TestWriteArchiveNoAttachments(t *testing.T) {
	snap := func(_ context.Context, destPath string) error {
		return os.WriteFile(destPath, []byte("DB"), 0o644)
	}
	var buf bytes.Buffer
	m, err := backup.WriteArchive(context.Background(), &buf, snap, "", nil)
	if err != nil {
		t.Fatalf("WriteArchive: %v", err)
	}
	if m.AttachmentCnt != 0 {
		t.Errorf("attachment count = %d, want 0", m.AttachmentCnt)
	}
	if buf.Len() == 0 {
		t.Fatal("archive should contain db + manifest even with no attachments")
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range zr.File {
		seen[f.Name] = true
	}
	if !seen["supplider.db"] || !seen["manifest.json"] {
		t.Errorf("archive missing db/manifest: %v", seen)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
