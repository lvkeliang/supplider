package sqlite_test

// CheckSnapshot backs the in-app restore upload: it must accept a healthy
// Supplider snapshot read-only and reject garbage, a corrupt database and
// an unrelated healthy SQLite file.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
	_ "modernc.org/sqlite"
)

func TestCheckSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := sqlite.Open(filepath.Join(dir, "supplider.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, &domain.Supplier{
		ID:        "sup_chk_0001",
		Status:    domain.StatusActive,
		BasicInfo: domain.BasicInfo{CompanyName: "恢复校验有限公司"},
	}); err != nil {
		t.Fatal(err)
	}

	snap := filepath.Join(dir, "snapshot.db")
	if err := store.SnapshotTo(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Healthy snapshot passes; check opened it read-only (no WAL sidecar).
	if err := sqlite.CheckSnapshot(snap); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if _, err := os.Stat(snap + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("read-only check must not create a WAL file, got err=%v", err)
	}

	// Garbage bytes fail.
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.CheckSnapshot(garbage); err == nil {
		t.Fatal("garbage file must fail validation")
	}

	// A healthy SQLite database without the Supplider schema fails too —
	// restoring an arbitrary .db must never be accepted.
	foreign := filepath.Join(dir, "foreign.db")
	if err := os.WriteFile(foreign, foreignSQLiteDB, 0o600); err != nil {
		t.Fatal(err)
	}
	err = sqlite.CheckSnapshot(foreign)
	if err == nil || !strings.Contains(err.Error(), "suppliers") {
		t.Fatalf("foreign schema must be rejected with a schema error, got %v", err)
	}
}

// foreignSQLiteDB is a healthy SQLite database without the Supplider
// schema, generated once via the registered driver.
var foreignSQLiteDB = buildForeignDB()

func buildForeignDB() []byte {
	dir, _ := os.MkdirTemp("", "foreign-db-")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "f.db")
	// DELETE journaling keeps everything in the main file (no -wal to read).
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(DELETE)")
	if err != nil {
		panic(err)
	}
	if _, err := db.Exec(`CREATE TABLE other_thing(id TEXT)`); err != nil {
		panic(err)
	}
	if err := db.Close(); err != nil {
		panic(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return b
}
