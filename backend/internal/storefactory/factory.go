// Package storefactory wires a datamodel.SupplierStore for the build-time
// tier. This is one of the ONLY places allowed to import concrete storage
// adapters (alongside the adapters themselves and feature-flag config) —
// business code never sees a concrete store type.
package storefactory

import (
	"github.com/supplider/supplider/backend/internal/datamodel"
)

// Config carries adapter selection parameters.
type Config struct {
	// DataDir is the personal-tier data directory (SQLite file,
	// attachments). Empty means ephemeral in-memory storage.
	DataDir string
}

// Open builds the tier-appropriate SupplierStore. The concrete adapter is
// chosen by build tags in tier_*.go files.
func Open(cfg Config) (datamodel.SupplierStore, error) {
	return newTierStore(cfg)
}
