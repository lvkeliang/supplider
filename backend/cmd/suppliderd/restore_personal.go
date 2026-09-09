//go:build personal

package main

// Personal-tier wiring for in-app restore/migration. The backup archive
// format is SQLite-specific (supplider.db snapshot + file attachments), so
// the staged-restore endpoints and boot-time apply exist only on this tier;
// small_business/enterprise get nil funcs (endpoints answer 501) and will
// gain their own adapter-specific archive path with MongoDB.

import (
	"io"
	"log"

	"github.com/supplider/supplider/backend/internal/backup"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/httpapi"
)

// applyPendingRestore performs a staged restore BEFORE any database is
// opened. A failure is fatal: opening the store over a half-swapped tree
// could compound the damage, and the rollback copy is on disk for support.
func applyPendingRestore(dataDir string) {
	if dataDir == "" {
		return
	}
	m, err := backup.ApplyPendingRestore(dataDir)
	if err != nil {
		log.Fatalf("applying pending restore: %v", err)
	}
	if !m.CreatedAt.IsZero() {
		log.Printf("restored library from backup created %s (attachments in archive: %d); previous library kept in a restore.rollback-* directory",
			m.CreatedAt.Format("2006-01-02 15:04:05"), m.AttachmentCnt)
	}
}

// restoreFuncs binds the filesystem-side restore operations to dataDir.
func restoreFuncs(dataDir string) *httpapi.RestoreFuncs {
	if dataDir == "" {
		return nil
	}
	return &httpapi.RestoreFuncs{
		Stage: func(r io.Reader) (backup.Manifest, error) {
			return backup.Stage(r, dataDir, sqlite.CheckSnapshot)
		},
		Pending: func() (backup.Manifest, bool, error) {
			return backup.PendingRestore(dataDir)
		},
		Cancel: func() error {
			return backup.CancelRestore(dataDir)
		},
	}
}
