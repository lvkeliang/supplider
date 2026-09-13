//go:build small_business

package vectorfactory

import (
	"github.com/supplider/supplider/backend/internal/vectorstore"
	"github.com/supplider/supplider/backend/internal/vectorstore/memory"
)

// newTierVectorStore wires small_business-tier vectors.
//
// MVP ROADMAP: embedded Qdrant behind the same vectorstore.Store port. Until
// then the in-memory reference store keeps semantic search functional (the
// index is primed via POST /ai/index).
func newTierVectorStore(_ Config) (vectorstore.Store, error) {
	// TODO: qdrant-embedded adapter.
	return memory.New(), nil
}
