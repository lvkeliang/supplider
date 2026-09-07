//go:build !personal && !small_business && !enterprise

package objectfactory

import (
	"github.com/supplider/supplider/backend/internal/objectstore"
)

// newTierObjectStore defaults to local-FS storage for tag-less dev builds
// (disabled when no data directory is set).
func newTierObjectStore(cfg Config) (objectstore.Store, error) {
	return openLocalFS(cfg.DataDir)
}
