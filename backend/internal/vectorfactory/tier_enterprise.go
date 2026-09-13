//go:build enterprise

package vectorfactory

import (
	"github.com/supplider/supplider/backend/internal/vectorstore"
	"github.com/supplider/supplider/backend/internal/vectorstore/memory"
)

// newTierVectorStore wires enterprise-tier vectors.
//
// MVP ROADMAP: distributed Milvus behind the same vectorstore.Store port.
// Until then the in-memory reference store keeps semantic search functional.
func newTierVectorStore(_ Config) (vectorstore.Store, error) {
	// TODO: milvus adapter.
	return memory.New(), nil
}
