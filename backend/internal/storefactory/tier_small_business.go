//go:build small_business

package storefactory

import (
	"fmt"
	"path/filepath"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
)

// newTierStore wires small_business-tier storage.
//
// PRD 技术约束: SQLite 3.45+ JSONB backs 个人版 AND 小企业版 (the Docker
// Compose monolith mounts a volume for the .db file); MongoDB is the
// enterprise-tier adapter landing later behind the same interface.
// Empty DataDir falls back to ephemeral in-memory storage.
func newTierStore(cfg Config) (datamodel.SupplierStore, error) {
	if cfg.DataDir == "" {
		return memory.New(), nil
	}
	st, err := sqlite.Open(filepath.Join(cfg.DataDir, "supplider.db"))
	if err != nil {
		return nil, fmt.Errorf("storefactory: open small-business sqlite store: %w", err)
	}
	return st, nil
}
