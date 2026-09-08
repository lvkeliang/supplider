package sqlite

import (
	"context"
	"fmt"
	"strings"
)

// SnapshotTo writes a consistent, standalone copy of the database to
// destPath using SQLite's VACUUM INTO. Unlike a raw file copy it is safe
// on a LIVE database (WAL mode, readers/writers concurrent): the snapshot
// contains every committed transaction up to the call and needs no
// -wal/-shm sidecars. Used by the backup/export feature to zip the library
// while the sidecar is running. destPath must not already exist.
func (s *Store) SnapshotTo(_ context.Context, destPath string) error {
	// VACUUM INTO takes a quoted filename literal; the path is one we
	// created ourselves under the OS temp dir, but double single quotes
	// defensively.
	quoted := "'" + strings.ReplaceAll(destPath, "'", "''") + "'"
	if _, err := s.db.ExecContext(context.Background(), "VACUUM INTO "+quoted); err != nil {
		return fmt.Errorf("sqlite: snapshot to %s: %w", destPath, err)
	}
	return nil
}
