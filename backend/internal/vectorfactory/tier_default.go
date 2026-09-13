//go:build !personal && !small_business && !enterprise

package vectorfactory

import (
	"github.com/supplider/supplider/backend/internal/vectorstore"
	"github.com/supplider/supplider/backend/internal/vectorstore/memory"
)

// newTierVectorStore defaults to the in-memory reference store for tag-less
// developer builds.
func newTierVectorStore(_ Config) (vectorstore.Store, error) {
	return memory.New(), nil
}
