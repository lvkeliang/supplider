//go:build personal

package vectorfactory

import (
	"fmt"
	"path/filepath"

	"github.com/supplider/supplider/backend/internal/vectorstore"
	"github.com/supplider/supplider/backend/internal/vectorstore/memory"
	"github.com/supplider/supplider/backend/internal/vectorstore/sqlite"
)

// newTierVectorStore wires personal-tier vectors: a SQLite table in its own
// file (<DataDir>/vectors.db) so the semantic index survives a restart without
// re-embedding. Empty DataDir falls back to the ephemeral in-memory reference
// store (CLI one-shots, tests).
func newTierVectorStore(cfg Config) (vectorstore.Store, error) {
	if cfg.DataDir == "" {
		return memory.New(), nil
	}
	st, err := sqlite.Open(filepath.Join(cfg.DataDir, "vectors.db"))
	if err != nil {
		return nil, fmt.Errorf("vectorfactory: open personal sqlite vector store: %w", err)
	}
	return st, nil
}
