package sqlite

// Restore-time validation of a backup database snapshot. Kept in the
// adapter package (not the driver-free backup package): it opens the
// candidate file read-only and proves it is a healthy Supplider database
// BEFORE a restore is allowed to stage.

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
)

// CheckSnapshot opens path read-only, runs SQLite's integrity check and
// confirms the core schema exists. It never writes (mode=ro), so it creates
// no WAL/SHM sidecars next to the staged file. Used by in-app restore.
func CheckSnapshot(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("sqlite: snapshot path: %w", err)
	}
	slash := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" { // file:///C:/... URI form
		slash = "/" + slash
	}
	u := &url.URL{Scheme: "file", Path: slash, RawQuery: "mode=ro"}
	// The modernc driver applies pragmas via DNS _pragma keys.
	dsn := u.String() + "&_pragma=query_only(ON)&_pragma=foreign_keys(ON)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("sqlite: open snapshot: %w", err)
	}
	defer db.Close()

	ctx := context.Background()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("sqlite: integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("sqlite: integrity check reported %q", result)
	}

	// Must be a Supplider database, not an arbitrary healthy SQLite file.
	var name string
	err = db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='suppliers'`).Scan(&name)
	if err == sql.ErrNoRows {
		return fmt.Errorf("sqlite: snapshot has no suppliers table (not a Supplider backup)")
	}
	if err != nil {
		return fmt.Errorf("sqlite: reading snapshot schema: %w", err)
	}
	return nil
}
