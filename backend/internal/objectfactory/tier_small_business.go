//go:build small_business

package objectfactory

import (
	"github.com/supplider/supplider/backend/internal/objectstore"
)

// newTierObjectStore wires small-business object storage.
//
// ROADMAP: MinIO (S3-compatible) behind the same port. Until the MinIO
// adapter lands, the Docker Compose tier also writes to a mounted local
// volume via the local-FS adapter so attachments work end-to-end.
func newTierObjectStore(cfg Config) (objectstore.Store, error) {
	return openLocalFS(cfg.DataDir)
}
