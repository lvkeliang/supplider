// Package vectorfactory wires a vectorstore.Store for the build-time tier.
// It is one of the ONLY places allowed to import a concrete vector-store
// adapter (alongside the adapters themselves) — business/HTTP code depends on
// the vectorstore.Store port, never on sqlite/Qdrant/Milvus directly.
package vectorfactory

import (
	"github.com/supplider/supplider/backend/internal/vectorstore"
)

// Config carries adapter selection parameters.
type Config struct {
	// DataDir is the tier data directory; the persistent index lives at
	// <DataDir>/vectors.db on the personal tier. Empty means the ephemeral
	// in-memory reference store.
	DataDir string
}

// Open builds the tier-appropriate vector store via the tag-selected
// newTierVectorStore function.
func Open(cfg Config) (vectorstore.Store, error) {
	return newTierVectorStore(cfg)
}
