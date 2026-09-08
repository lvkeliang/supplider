// Package backup produces a single-file archive of a personal-tier
// library: a consistent SQLite snapshot (VACUUM INTO, safe while the
// sidecar is running) plus every attachment file, bundled as a zip with a
// manifest. It is the local, zero-dependency realization of the PRD's
// 数据备份与迁移 requirement — a user copies one .zip to another machine
// (or keeps it off-disk) and restores by unzipping over the data directory
// while the app is closed.
package backup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ArchiveFormat / ArchiveVersion identify the backup shape so a future
// restore path can recognize (and migrate) older files.
const (
	ArchiveFormat  = "supplider-backup"
	ArchiveVersion = 1
	// DBName is the snapshot's path inside the archive.
	DBName = "supplider.db"
	// AttachmentsPrefix roots attachment files inside the archive.
	AttachmentsPrefix = "attachments/"
)

// DBSnapshot writes a consistent standalone database copy to destPath
// (implemented by the sqlite adapter via VACUUM INTO).
type DBSnapshot func(ctx context.Context, destPath string) error

// Manifest describes one backup archive.
type Manifest struct {
	Format        string         `json:"format"`
	Version       int            `json:"version"`
	CreatedAt     time.Time      `json:"created_at"`
	Database      string         `json:"database"`
	Attachments   string         `json:"attachments"`
	AttachmentCnt int            `json:"attachment_count"`
	Extra         map[string]any `json:"extra,omitempty"` // tier, app version, ...
}

// WriteArchive streams a complete backup zip to w: a temp database snapshot
// taken through snap, every regular file under attachmentsRoot (stored
// under attachments/ preserving the <supplier-id>/<file> layout), and a
// manifest.json. attachmentsRoot may be empty/non-existent (a library with
// no attachment directory) — the archive then contains the database only.
func WriteArchive(ctx context.Context, w io.Writer, snap DBSnapshot, attachmentsRoot string, extra map[string]any) (Manifest, error) {
	manifest := Manifest{
		Format:      ArchiveFormat,
		Version:     ArchiveVersion,
		CreatedAt:   time.Now().UTC(),
		Database:    DBName,
		Attachments: AttachmentsPrefix,
		Extra:       extra,
	}

	tmp, err := os.MkdirTemp("", "supplider-backup-")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(tmp)

	snapPath := filepath.Join(tmp, DBName)
	if err := snap(ctx, snapPath); err != nil {
		return manifest, err
	}

	zw := zip.NewWriter(w)

	// Database snapshot.
	if err := addFile(zw, DBName, snapPath); err != nil {
		return manifest, err
	}

	// Attachments (walk the local-FS root; layout <root>/<supplier-id>/<file>).
	if attachmentsRoot != "" {
		_ = filepath.WalkDir(attachmentsRoot, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(attachmentsRoot, path)
			if err != nil {
				return nil
			}
			if err := addFile(zw, AttachmentsPrefix+filepath.ToSlash(rel), path); err != nil {
				return err
			}
			manifest.AttachmentCnt++
			return nil
		})
	}

	// Manifest last so the attachment count is final.
	mf, err := zw.Create("manifest.json")
	if err != nil {
		return manifest, err
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return manifest, err
	}

	if err := zw.Close(); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// addFile streams one on-disk file into the zip under name.
func addFile(zw *zip.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, f)
	return err
}
