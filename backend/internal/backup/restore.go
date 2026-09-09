// In-app restore / migration (应用内恢复). A backup zip produced by
// WriteArchive can be uploaded WHILE the sidecar runs: it is validated and
// extracted into a staging directory, and the actual swap happens at the
// next process start (ApplyPendingRestore, invoked before any store is
// opened) so we never replace a live SQLite file. The current library is
// moved aside into a timestamped rollback directory first, so a bad restore
// is recoverable.
//
// Directory layout inside <DataDir>:
//
//	supplider.db (+ -wal/-shm)      live library
//	attachments/                    live objects
//	restore.staging/                validated archive, applied at next boot
//	restore.rollback-<UTC time>/    pre-restore library (newest kept)
package backup

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ManifestName is the manifest's path inside the archive and staging dir.
const ManifestName = "manifest.json"

const (
	// stagingDir is the validated pending restore.
	stagingDir = "restore.staging"
	// rollbackPrefix names pre-restore snapshots kept for recovery.
	rollbackPrefix = "restore.rollback-"
	// maxUncompressed guards against zip-bomb extraction: total extracted
	// bytes must not exceed this (per-file backups are far smaller in the
	// personal use case; attach files ≤50MB each).
	maxUncompressed = 2 << 30 // 2 GiB
	// maxZipEntries bounds the number of extracted files.
	maxZipEntries = 100_000
)

// CheckDatabase verifies an extracted database snapshot is openable and
// consistent BEFORE it is allowed to stage. Implemented by the concrete
// adapter (sqlite), injected so this package stays driver-free.
type CheckDatabase func(dbPath string) error

// Stage reads a backup zip from r, validates it (manifest + database check)
// and installs it as the pending restore, replacing any previous staging.
// Nothing live is touched. The caller should bound r (MaxBytesReader).
func Stage(r io.Reader, dataDir string, check CheckDatabase) (Manifest, error) {
	if dataDir == "" {
		return Manifest{}, errors.New("backup: restore requires a persistent data directory")
	}
	if check == nil {
		return Manifest{}, errors.New("backup: database checker not wired (restore unsupported on this tier)")
	}

	// Spool the upload: zip needs ReaderAt (central directory), and a
	// temp file avoids holding whole archives in memory.
	spool, err := os.CreateTemp(dataDir, ".restore-upload-*.zip")
	if err != nil {
		return Manifest{}, err
	}
	spoolPath := spool.Name()
	defer os.Remove(spoolPath)
	if _, err := io.Copy(spool, r); err != nil {
		spool.Close()
		return Manifest{}, fmt.Errorf("backup: reading upload: %w", err)
	}
	if err := spool.Close(); err != nil {
		return Manifest{}, err
	}

	zr, err := zip.OpenReader(spoolPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: not a valid zip archive: %w", err)
	}
	defer zr.Close()

	var manifest Manifest
	var sawDB, sawManifest bool
	// Extract into a sibling temp dir (same filesystem → atomic rename).
	work, err := os.MkdirTemp(dataDir, ".restore-work-*")
	if err != nil {
		return Manifest{}, err
	}
	// On any failure before staging is committed, discard the work dir.
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(work)
		}
	}()

	var total int64
	for i, zf := range zr.File {
		if i >= maxZipEntries {
			return Manifest{}, errors.New("backup: archive contains too many entries")
		}
		name := filepath.FromSlash(zf.Name)
		target, allowed, isDir := safeExtractPath(work, name)
		if !allowed {
			return Manifest{}, fmt.Errorf("backup: archive contains path outside allowed layout: %q", zf.Name)
		}
		if isDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return Manifest{}, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return Manifest{}, err
		}
		// Regular files only — reject symlinks/devices (hardlinks are not
		// representable in zip; Mode().IsRegular plus zero non-regular bits).
		if zf.Mode()&os.ModeType != 0 {
			return Manifest{}, fmt.Errorf("backup: archive entry %q is not a regular file", zf.Name)
		}
		if zf.UncompressedSize64 > maxUncompressed {
			return Manifest{}, fmt.Errorf("backup: archive entry %q exceeds size limit", zf.Name)
		}
		n, err := extractFile(zf, target)
		if err != nil {
			return Manifest{}, err
		}
		total += n
		if total > maxUncompressed {
			return Manifest{}, errors.New("backup: archive exceeds total uncompressed size limit")
		}
		switch filepath.ToSlash(zf.Name) {
		case DBName:
			sawDB = true
		case ManifestName:
			raw, err := os.ReadFile(target)
			if err != nil {
				return Manifest{}, err
			}
			if err := json.Unmarshal(raw, &manifest); err != nil {
				return Manifest{}, fmt.Errorf("backup: invalid manifest: %w", err)
			}
			sawManifest = true
		}
	}

	if !sawManifest {
		return Manifest{}, fmt.Errorf("backup: archive is missing %s", ManifestName)
	}
	if manifest.Format != ArchiveFormat {
		return Manifest{}, fmt.Errorf("backup: unrecognized archive format %q", manifest.Format)
	}
	if manifest.Version != ArchiveVersion {
		return Manifest{}, fmt.Errorf("backup: unsupported archive version %d (this build supports %d)",
			manifest.Version, ArchiveVersion)
	}
	if !sawDB {
		return Manifest{}, fmt.Errorf("backup: archive is missing %s", DBName)
	}
	// Adapter-level check: the snapshot must open and pass integrity_check.
	if err := check(filepath.Join(work, filepath.FromSlash(DBName))); err != nil {
		return Manifest{}, fmt.Errorf("backup: database snapshot failed validation: %w", err)
	}

	// Commit: replace any prior staging. Rename cannot replace a non-empty
	// directory, so move the old one out of the way first (same directory).
	staging := filepath.Join(dataDir, stagingDir)
	if _, err := os.Stat(staging); err == nil {
		old := filepath.Join(dataDir, fmt.Sprintf(".restore-old-%d", time.Now().UnixNano()))
		if err := os.Rename(staging, old); err != nil {
			return Manifest{}, err
		}
		_ = os.RemoveAll(old)
	}
	if err := os.Rename(work, staging); err != nil {
		return Manifest{}, err
	}
	committed = true
	return manifest, nil
}

// safeExtractPath confines extraction to root and limits entries to the
// documented archive layout. Returns the absolute target, whether the entry
// is allowed at all, and whether it is a directory entry.
func safeExtractPath(root, name string) (target string, allowed bool, isDir bool) {
	if name == "" || filepath.IsAbs(name) {
		return "", false, false
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false, false
	}
	isDir = strings.HasSuffix(name, string(filepath.Separator))
	if isDir {
		clean = strings.TrimSuffix(clean, string(filepath.Separator))
	}
	top := clean
	if idx := strings.Index(clean, string(filepath.Separator)); idx >= 0 {
		top = clean[:idx]
	}
	switch {
	case clean == filepath.FromSlash(DBName), clean == ManifestName:
	case top == filepath.FromSlash(strings.TrimSuffix(AttachmentsPrefix, "/")):
	default:
		return "", false, false
	}
	return filepath.Join(root, clean), true, isDir
}

// extractFile copies one zip file to dest with mode 0o600 (personal data).
func extractFile(zf *zip.File, dest string) (int64, error) {
	rc, err := zf.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, io.LimitReader(rc, maxUncompressed+1))
}

// PendingRestore reports the staged restore awaiting the next startup.
func PendingRestore(dataDir string) (Manifest, bool, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(dataDir, stagingDir, ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, false, fmt.Errorf("backup: invalid staged manifest: %w", err)
	}
	return m, true, nil
}

// CancelRestore discards a staged restore. Idempotent: succeeds when
// nothing is staged.
func CancelRestore(dataDir string) error {
	return os.RemoveAll(filepath.Join(dataDir, stagingDir))
}

// ApplyPendingRestore swaps the staged archive into place. MUST run at boot
// before any database connection opens. It moves the current library into a
// rollback directory, then moves the staged files live. On failure it
// attempts to move the current library back and returns an error (the
// caller should refuse to start rather than open a half-swapped database).
func ApplyPendingRestore(dataDir string) (Manifest, error) {
	var manifest Manifest
	staging := filepath.Join(dataDir, stagingDir)
	raw, err := os.ReadFile(filepath.Join(staging, ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return manifest, nil // nothing staged: normal boot
	}
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, fmt.Errorf("invalid staged manifest: %w", err)
	}
	stagedDB := filepath.Join(staging, filepath.FromSlash(DBName))
	if _, err := os.Stat(stagedDB); err != nil {
		return manifest, fmt.Errorf("staged database missing: %w", err)
	}

	rollback := filepath.Join(dataDir, rollbackPrefix+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(rollback, 0o755); err != nil {
		return manifest, err
	}

	// Moved tracks live files relocated to rollback so a failed swap can
	// be undone in reverse order.
	type moved struct{ from, to string }
	done := []moved{}
	rollbackErr := func(format string, args ...any) error {
		for i := len(done) - 1; i >= 0; i-- {
			_ = os.MkdirAll(filepath.Dir(done[i].from), 0o755)
			_ = os.Rename(done[i].to, done[i].from)
		}
		_ = os.RemoveAll(rollback)
		return fmt.Errorf(format, args...)
	}

	moveLive := func(rel string) error {
		from := filepath.Join(dataDir, rel)
		if _, err := os.Stat(from); err != nil {
			return nil // nothing live to preserve
		}
		to := filepath.Join(rollback, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		done = append(done, moved{from: from, to: to})
		return nil
	}
	// WAL/SHM first: they must never survive into the restored database.
	for _, rel := range []string{"supplider.db-wal", "supplider.db-shm", "supplider.db", "attachments"} {
		if err := moveLive(rel); err != nil {
			return manifest, rollbackErr("moving %s to rollback: %v", rel, err)
		}
	}

	// Move staged files live (rename within dataDir = same filesystem).
	if err := os.Rename(stagedDB, filepath.Join(dataDir, filepath.FromSlash(DBName))); err != nil {
		return manifest, rollbackErr("activating restored database: %v", err)
	}
	stagedAtt := filepath.Join(staging, filepath.FromSlash(strings.TrimSuffix(AttachmentsPrefix, "/")))
	if _, err := os.Stat(stagedAtt); err == nil {
		if err := os.Rename(stagedAtt, filepath.Join(dataDir, "attachments")); err != nil {
			return manifest, rollbackErr("activating restored attachments: %v", err)
		}
	}

	// Commit complete: staging is consumed; keep only the newest rollback.
	_ = os.RemoveAll(staging)
	pruneRollbacks(dataDir, rollback)
	return manifest, nil
}

// pruneRollbacks removes every restore.rollback-* directory except keep so
// repeated restores cannot fill the disk with old libraries.
func pruneRollbacks(dataDir, keep string) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), rollbackPrefix) {
			continue
		}
		full := filepath.Join(dataDir, e.Name())
		if full != keep {
			_ = os.RemoveAll(full)
		}
	}
}
