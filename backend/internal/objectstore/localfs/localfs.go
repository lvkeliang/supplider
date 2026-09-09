// Package localfs is the personal / small-business objectstore adapter:
// attachments live as plain files under the user's data directory
// (<DataDir>/attachments/<supplier-id>/<file>), so the desktop app stores
// documents with zero external services and no daemon.
//
// It implements the S3-shaped objectstore.Store port; the MinIO/S3
// adapters drop in behind the same interface later. Standard library only —
// consistent with the personal zero-dependency constraint.
package localfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/objectstore"
)

// Store is a filesystem-backed objectstore.Store.
type Store struct {
	root string // absolute directory holding all objects
}

// New opens (creating if needed) a local FS object store rooted at root.
// Convention: root is <DataDir>/attachments.
func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("localfs: root directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("localfs: resolve root %q: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("localfs: create root %q: %w", abs, err)
	}
	return &Store{root: abs}, nil
}

// Root reports the absolute on-disk root (useful for logs / backups).
func (s *Store) Root() string { return s.root }

// Put streams an object into root/<key>. It enforces the 50MB hard limit
// regardless of the caller-supplied size: a limited reader rejects any
// stream that overruns, so a lying Content-Length cannot fill the disk.
func (s *Store) Put(_ context.Context, key, name, mimeType string, r io.Reader, size int64) (objectstore.Object, error) {
	target, err := s.resolve(key)
	if err != nil {
		return objectstore.Object{}, err
	}
	if size > objectstore.MaxAttachmentSize {
		return objectstore.Object{}, &objectstore.ErrTooLarge{Size: size, Max: objectstore.MaxAttachmentSize}
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return objectstore.Object{}, fmt.Errorf("localfs: mkdir for %q: %w", key, err)
	}

	// Write to a temp file then rename, so a failed/over-long upload never
	// leaves a partial object at its final path.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".upload-*")
	if err != nil {
		return objectstore.Object{}, fmt.Errorf("localfs: create temp for %q: %w", key, err)
	}
	tmpName := tmp.Name()
	// One extra byte lets us detect an over-limit stream: if LimitReader
	// returns MaxAttachmentSize+1 bytes, the object exceeded the cap.
	written, err := io.Copy(tmp, io.LimitReader(r, objectstore.MaxAttachmentSize+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return objectstore.Object{}, fmt.Errorf("localfs: write %q: %w", key, err)
	}
	if written > objectstore.MaxAttachmentSize {
		_ = os.Remove(tmpName)
		return objectstore.Object{}, &objectstore.ErrTooLarge{Size: written, Max: objectstore.MaxAttachmentSize}
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return objectstore.Object{}, fmt.Errorf("localfs: finalize %q: %w", key, err)
	}

	return objectstore.Object{
		Key:        key,
		Name:       name,
		Size:       written,
		MIMEType:   mimeType,
		UploadedAt: time.Now().UTC(),
	}, nil
}

// Get streams an object back. The caller must close the returned reader.
func (s *Store) Get(_ context.Context, key string) (io.ReadCloser, objectstore.Object, error) {
	target, err := s.resolve(key)
	if err != nil {
		return nil, objectstore.Object{}, err
	}
	f, err := os.Open(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, objectstore.Object{}, objectstore.ErrObjectNotFound
	}
	if err != nil {
		return nil, objectstore.Object{}, fmt.Errorf("localfs: open %q: %w", key, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, objectstore.Object{}, fmt.Errorf("localfs: stat %q: %w", key, err)
	}
	obj := objectstore.Object{
		Key:        key,
		Name:       filepath.Base(key),
		Size:       st.Size(),
		UploadedAt: st.ModTime().UTC(),
	}
	return f, obj, nil
}

// Remove deletes an object. Missing objects are not an error (idempotent).
func (s *Store) Remove(_ context.Context, key string) error {
	target, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("localfs: remove %q: %w", key, err)
	}
	return nil
}

// URL returns the HTTP-relative path the API serves the object under. The
// handler streams bytes from Get; this string is what gets stored on the
// supplier document as attachment.url and works same-origin in dev (Vite
// proxy) and against the sidecar in the Tauri build.
func (s *Store) URL(_ context.Context, key string) (string, error) {
	if _, err := s.resolve(key); err != nil {
		return "", err
	}
	return "/api/v1/attachments/" + key, nil
}

// resolve joins key under root and guarantees the result stays inside root
// (defense against path traversal in a key). Keys are normally generated
// server-side, but this never trusts a caller-supplied key.
func (s *Store) resolve(key string) (string, error) {
	key = filepath.ToSlash(strings.TrimSpace(key))
	if key == "" || key == "." || strings.Contains(key, "..") {
		return "", fmt.Errorf("localfs: %w %q", objectstore.ErrInvalidKey, key)
	}
	// Control characters (NUL, CR, tab…) are never legal in an object key
	// or filesystem path: handing one to os.Open fails deep in syscall
	// code (EINVAL) which the HTTP layer can only report as 500. Reject up
	// front as a bad key so it classifies as 400. S3-compatible stores
	// forbid the same characters, so the future S3 adapter keeps this check.
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("localfs: %w %q contains a control character", objectstore.ErrInvalidKey, key)
		}
	}
	target := filepath.Join(s.root, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("localfs: %w %q escapes storage root", objectstore.ErrInvalidKey, key)
	}
	return target, nil
}
