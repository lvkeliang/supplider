package memory_test

import (
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/contract"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
)

// TestSupplierStoreContract runs the shared adapter conformance suite
// against the in-memory store. The SQLite and (later) MongoDB adapters
// run this identical suite.
func TestSupplierStoreContract(t *testing.T) {
	contract.RunSupplierStoreTests(t, func() datamodel.SupplierStore {
		return memory.New()
	})
}
