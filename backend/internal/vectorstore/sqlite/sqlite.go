// Package sqlite is the persistent personal-tier vectorstore.Store adapter:
// vectors live in a single SQLite table so the semantic index survives a
// restart (no re-embed on every launch — which would burn tokens). It mirrors
// the memory reference adapter exactly: brute-force cosine ranking over all
// vectors, which is plenty at personal scale (<10k suppliers); Qdrant/Milvus
// replace it at small_business/enterprise behind the same port.
//
// The driver is modernc.org/sqlite (pure Go, cgo-free) — the same driver the
// datamodel/sqlite adapter uses, so the personal binary stays a single
// cross-compilable ~15MB artifact.
//
// A vector is stored as a little-endian float32 byte blob; the dimension is
// len(blob)/4. The table lives in its OWN file (<DataDir>/vectors.db) so the
// supplier store's schema and backup are untouched — the index is derived
// data, rebuildable via POST /ai/index, so it is intentionally not part of
// the supplier-library backup.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers as "sqlite"

	"github.com/supplider/supplider/backend/internal/vectorstore"
)

// Store is a SQLite-backed vectorstore.Store.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating on first use) the SQLite database at path and runs the
// migration. path ":memory:" (or "") yields an ephemeral in-memory DB pinned
// to one connection.
func Open(path string) (*Store, error) {
	if path == "" {
		path = ":memory:"
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" + // writers wait up to 5s instead of SQLITE_BUSY
		"&_pragma=synchronous(NORMAL)"
	if path != ":memory:" {
		dsn += "&_pragma=journal_mode(WAL)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("vectorstore/sqlite: open %q: %w", path, err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1) // each :memory: connection is its own DB
	}
	st := &Store{db: db, path: path}
	if err := st.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

// Path reports the opened database file (":memory:" for ephemeral stores).
func (s *Store) Path() string { return s.path }

// Close releases the connection/file.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vectors (
	    id  TEXT PRIMARY KEY,
	    vec BLOB NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("vectorstore/sqlite: migrate: %w", err)
	}
	return nil
}

// Upsert implements vectorstore.Store.
func (s *Store) Upsert(_ context.Context, id string, vector []float32) error {
	_, err := s.db.Exec(
		`INSERT INTO vectors(id, vec) VALUES(?, ?)
		 ON CONFLICT(id) DO UPDATE SET vec = excluded.vec`,
		id, encodeVector(vector),
	)
	if err != nil {
		return fmt.Errorf("vectorstore/sqlite: upsert %q: %w", id, err)
	}
	return nil
}

// Delete implements vectorstore.Store (no-op if absent).
func (s *Store) Delete(_ context.Context, id string) error {
	_, err := s.db.Exec(`DELETE FROM vectors WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("vectorstore/sqlite: delete %q: %w", id, err)
	}
	return nil
}

// Clear implements vectorstore.Store.
func (s *Store) Clear(_ context.Context) error {
	_, err := s.db.Exec(`DELETE FROM vectors`)
	if err != nil {
		return fmt.Errorf("vectorstore/sqlite: clear: %w", err)
	}
	return nil
}

// Len reports how many vectors are stored. It reads the count directly; the
// port signature has no context/error, so a query failure reports 0.
func (s *Store) Len() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM vectors`).Scan(&n)
	return n
}

// Search ranks every stored vector by cosine similarity to the query and
// returns the topK (same brute-force behaviour as the memory reference).
func (s *Store) Search(_ context.Context, vector []float32, topK int) ([]vectorstore.Scored, error) {
	rows, err := s.db.Query(`SELECT id, vec FROM vectors`)
	if err != nil {
		return nil, fmt.Errorf("vectorstore/sqlite: search: %w", err)
	}
	defer rows.Close()

	type pair struct {
		id    string
		score float64
	}
	scored := make([]pair, 0, 256)
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("vectorstore/sqlite: scan: %w", err)
		}
		sim, err := vectorstore.Cosine(vector, decodeVector(blob))
		if err != nil {
			continue // dimension mismatch: not comparable
		}
		scored = append(scored, pair{id: id, score: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vectorstore/sqlite: rows: %w", err)
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if topK > 0 && len(scored) > topK {
		scored = scored[:topK]
	}
	out := make([]vectorstore.Scored, len(scored))
	for i, p := range scored {
		out[i] = vectorstore.Scored{ID: p.id, Score: p.score}
	}
	return out, nil
}

// encodeVector packs []float32 into a little-endian byte blob.
func encodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// decodeVector unpacks a little-endian byte blob into []float32.
func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
