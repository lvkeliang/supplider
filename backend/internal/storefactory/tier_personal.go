//go:build personal

package storefactory

import (
	"fmt"
	"path/filepath"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
)

// newTierStore wires personal-tier storage: a single SQLite file
// (<DataDir>/supplider.db, JSONB document columns) opened by the Tauri
// sidecar — zero external services, zero cgo (pure-Go modernc driver).
//
// Empty DataDir means an ephemeral sandbox (CLI one-shots, tests): fall
// back to the in-memory reference store.
func newTierStore(cfg Config) (datamodel.SupplierStore, error) {
	if cfg.DataDir == "" {
		return memory.New(), nil
	}
	st, err := sqlite.Open(filepath.Join(cfg.DataDir, "supplider.db"))
	if err != nil {
		return nil, fmt.Errorf("storefactory: open personal sqlite store: %w", err)
	}
	return st, nil
}
