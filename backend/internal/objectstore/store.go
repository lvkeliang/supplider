// Package objectstore defines the binary-object port (attachments).
//
// Adapters: local filesystem (personal), MinIO (small_business),
// S3-compatible (enterprise). The personal tier writes under the user's
// data directory — no daemon required.
package objectstore

import (
	"context"
	"io"
	"time"
)

// MaxAttachmentSize is the 50MB per-file hard limit (PRD 性能红线).
const MaxAttachmentSize = 50 * 1024 * 1024

// Object is stored-file metadata.
type Object struct {
	Key        string
	Name       string
	Size       int64
	MIMEType   string
	UploadedAt time.Time
}

// Store is the object-storage port (S3-shaped so all backends fit).
type Store interface {
	// Put streams an object in. size may be 0 when unknown;
	// implementations MUST reject size > MaxAttachmentSize.
	Put(ctx context.Context, key, name, mimeType string, r io.Reader, size int64) (Object, error)
	// Get streams an object out.
	Get(ctx context.Context, key string) (io.ReadCloser, Object, error)
	// Remove deletes an object.
	Remove(ctx context.Context, key string) error
	// URL returns a retrievable URL/path (local path on personal tier).
	URL(ctx context.Context, key string) (string, error)
}
