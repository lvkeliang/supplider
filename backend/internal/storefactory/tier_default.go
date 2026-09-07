//go:build !personal && !small_business && !enterprise

package storefactory

import (
	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
)

// newTierStore defaults to in-memory storage for tag-less developer builds.
func newTierStore(_ Config) (datamodel.SupplierStore, error) {
	return memory.New(), nil
}
