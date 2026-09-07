// Package objectfactory wires an objectstore.Store for the build-time tier.
// It is one of the ONLY places allowed to import a concrete object-storage
// adapter (alongside the adapters themselves) — business/HTTP code depends
// on the objectstore.Store port, never on localfs/MinIO/S3 directly.
//
// A nil store with a nil error means attachments are disabled for this run
// (e.g. ephemeral in-memory mode with no data directory); HTTP handlers
// answer 501 in that case.
package objectfactory

import (
	"path/filepath"

	"github.com/supplider/supplider/backend/internal/objectstore"
	"github.com/supplider/supplider/backend/internal/objectstore/localfs"
)

// Config carries adapter selection parameters.
type Config struct {
	// DataDir is the tier data directory; attachments live under
	// <DataDir>/attachments on the local-FS tiers. Empty disables storage.
	DataDir string
}

// Open builds the tier-appropriate object store via the tag-selected
// newTierStore function.
func Open(cfg Config) (objectstore.Store, error) {
	return newTierObjectStore(cfg)
}

// openLocalFS is the shared local-filesystem wiring used by the personal
// and small-business tiers (and the tag-less developer build). Returns
// (nil, nil) when no data directory is configured.
func openLocalFS(dataDir string) (objectstore.Store, error) {
	if dataDir == "" {
		return nil, nil
	}
	st, err := localfs.New(filepath.Join(dataDir, "attachments"))
	if err != nil {
		return nil, err
	}
	return st, nil
}
