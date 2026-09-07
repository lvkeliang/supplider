//go:build personal

package objectfactory

import (
	"github.com/supplider/supplider/backend/internal/objectstore"
)

// newTierObjectStore wires personal-tier object storage: plain files under
// <DataDir>/attachments via the local-FS adapter (zero dependencies).
func newTierObjectStore(cfg Config) (objectstore.Store, error) {
	return openLocalFS(cfg.DataDir)
}
