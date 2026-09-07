//go:build enterprise

package storefactory

import (
	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
)

// newTierStore wires enterprise-tier storage.
//
// MVP ROADMAP: sharded MongoDB (datamodel/mongo) behind the same
// datamodel.SupplierStore interface, running the same contract suite.
func newTierStore(_ Config) (datamodel.SupplierStore, error) {
	// TODO: datamodel/mongo adapter (sharded) + contract suite.
	return memory.New(), nil
}
