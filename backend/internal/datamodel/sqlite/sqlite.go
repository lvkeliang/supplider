// Package sqlite is the personal / small-business SupplierStore adapter:
// a single SQLite file (default: <DataDir>/supplider.db) that the Tauri
// sidecar opens with zero external dependencies.
//
// The driver is modernc.org/sqlite — a pure-Go, cgo-free SQLite — so the
// personal binary cross-compiles for the Tauri sidecar (Windows/macOS/
// Linux) without a C toolchain and stays a single ~15MB artifact. The
// driver registers itself for database/sql under the name "sqlite".
//
// # Document storage design
//
// Each supplier lives as ONE row:
//
//   - `doc` BLOB holds the full document as SQLite JSONB (via jsonb()),
//     keeping 文档式存储 semantics — free-form custom_fields and nested
//     arrays need no schema migration. Read back with json(doc), which
//     re-serializes the JSONB blob to text for encoding/json.
//   - Every field the structured filters / sorts / keyset cursor touch
//     is DENORMALIZED into a typed column (name, region, rating, qual_rank,
//     owner, visibility, timestamps). That is faster and simpler than
//     json_extract predicates and gives real indexes for the hot paths
//     (region filter, owner visibility, keyset on created_at). json_extract
//     remains available inside the doc column for ad-hoc custom_fields
//     queries; 个人版数据量下不需要 JSON 路径表达式索引。
//
// All query semantics (AND filters, category OR, archive hiding, keyword
// substring, keyset tuple comparison) mirror the memory reference adapter
// and are pinned by the shared datamodel/contract suite — the MongoDB
// enterprise adapter will run the same tests.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers as "sqlite"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/search"
)

// Schema version, bumped when migrateSQL changes.
const schemaVersion = 1

// Store is a SQLite-backed datamodel.SupplierStore.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating on first use) the SQLite database at path and runs
// migrations. path ":memory:" (or "") yields an ephemeral in-memory DB
// intended for tests / ephemeral sandboxes; such a store is pinned to one
// connection so the in-memory page cache is shared.
func Open(path string) (*Store, error) {
	if path == "" {
		path = ":memory:"
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" + // writers wait up to 5s instead of SQLITE_BUSY
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)" // safe under WAL, much faster fsync
	if path != ":memory:" {
		dsn += "&_pragma=journal_mode(WAL)" // readers + one writer concurrently
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", path, err)
	}
	if path == ":memory:" {
		// Each connection to :memory: gets its OWN database; pin the pool.
		db.SetMaxOpenConns(1)
	}
	st := &Store{db: db, path: path}
	if err := st.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

// Path reports the opened database file (":memory:" for ephemeral stores).
func (s *Store) Path() string { return s.path }

// Ping implements datamodel.SupplierStore.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close implements datamodel.SupplierStore.
func (s *Store) Close() error { return s.db.Close() }

// migrate creates the schema once. The design is append-only going forward
// (new statements get a PRAGMA user_version guard); v1 is the initial shape.
func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS suppliers (
    id          TEXT PRIMARY KEY,
    owner       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT '',
    visibility  INTEGER NOT NULL DEFAULT 0,
    name        TEXT NOT NULL DEFAULT '',
    province    TEXT NOT NULL DEFAULT '',
    city        TEXT NOT NULL DEFAULT '',
    district    TEXT NOT NULL DEFAULT '',
    rating      REAL NOT NULL DEFAULT 0,
    qual_rank   INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    archived_at TEXT,
    search_text TEXT NOT NULL DEFAULT '',
    doc         BLOB NOT NULL
);

-- Keyset pagination walks these (sort key + id tiebreaker, both directions).
CREATE INDEX IF NOT EXISTS idx_suppliers_created ON suppliers(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_suppliers_updated ON suppliers(updated_at DESC, id DESC);

-- Hot structured filters (条件筛选).
CREATE INDEX IF NOT EXISTS idx_suppliers_region  ON suppliers(province, city, district);
CREATE INDEX IF NOT EXISTS idx_suppliers_owner   ON suppliers(owner);
CREATE INDEX IF NOT EXISTS idx_suppliers_status  ON suppliers(status);
CREATE INDEX IF NOT EXISTS idx_suppliers_rating  ON suppliers(rating);

-- Category tags are a many-to-many side table (OR semantics via EXISTS).
CREATE TABLE IF NOT EXISTS supplier_categories (
    supplier_id TEXT NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    category    TEXT NOT NULL,
    PRIMARY KEY (supplier_id, category)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_sc_category ON supplier_categories(category);

-- Admin/config settings (visibility policy, future admin console values):
-- small named JSON/text values that must travel with the supplier database
-- through backup and migration. Additive IF NOT EXISTS migrates old files
-- on the next open without a schema-version bump.
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) WITHOUT ROWID;

-- In-app notifications for followed suppliers (关注/变更推送). Separate
-- from supplier documents so archiving/merging never loses feed history.
-- Additive IF NOT EXISTS migrates old files without a version bump (same
-- approach as settings). The partial unique index makes dedup keys
-- collapse on insert; discrete events store '' and always insert.
CREATE TABLE IF NOT EXISTS notifications (
    id            TEXT PRIMARY KEY,
    supplier_id   TEXT NOT NULL DEFAULT '',
    supplier_name TEXT NOT NULL DEFAULT '',
    type          TEXT NOT NULL,
    severity      TEXT NOT NULL DEFAULT 'info',
    title         TEXT NOT NULL,
    body          TEXT NOT NULL DEFAULT '',
    dedup_key     TEXT NOT NULL DEFAULT '',
    is_read       INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_unread  ON notifications(is_read, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_dedup
    ON notifications(dedup_key) WHERE dedup_key <> '';
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("sqlite: migrate: %w", err)
	}
	// FTS5 trigram index (Chinese substring + pinyin + synonyms). Separate
	// DDL so virtual-table syntax stays grouped in fts.go.
	if _, err := s.db.ExecContext(ctx, ftsSchema); err != nil {
		return fmt.Errorf("sqlite: migrate fts: %w", err)
	}
	if err := s.reindexFTS(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `PRAGMA user_version = `+strconv.Itoa(schemaVersion))
	return err
}

// Put implements datamodel.SupplierStore (upsert by ID, document semantics).
func (s *Store) Put(ctx context.Context, doc *domain.Supplier) error {
	if doc == nil || doc.ID == "" {
		return fmt.Errorf("sqlite: put requires a document with an ID")
	}
	payload, err := encode(doc)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var archivedAt any
	if doc.ArchivedAt != nil {
		archivedAt = doc.ArchivedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO suppliers
    (id, owner, status, visibility, name, province, city, district,
     rating, qual_rank, created_at, updated_at, archived_at, search_text, doc)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, jsonb(?))
ON CONFLICT(id) DO UPDATE SET
    owner       = excluded.owner,
    status      = excluded.status,
    visibility  = excluded.visibility,
    name        = excluded.name,
    province    = excluded.province,
    city        = excluded.city,
    district    = excluded.district,
    rating      = excluded.rating,
    qual_rank   = excluded.qual_rank,
    created_at  = excluded.created_at,
    updated_at  = excluded.updated_at,
    archived_at = excluded.archived_at,
    search_text = excluded.search_text,
    doc         = excluded.doc`,
		doc.ID,
		doc.Owner,
		doc.Status,
		doc.Visibility,
		doc.BasicInfo.CompanyName,
		doc.BasicInfo.Region.Province,
		doc.BasicInfo.Region.City,
		doc.BasicInfo.Region.District,
		doc.Rating,
		domain.HighestQualRank(doc),
		doc.CreatedAt.UTC().Format(time.RFC3339Nano),
		doc.UpdatedAt.UTC().Format(time.RFC3339Nano),
		archivedAt,
		buildSearchText(doc),
		payload,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert %s: %w", doc.ID, err)
	}

	// Rebuild the category projection for this document.
	if _, err := tx.ExecContext(ctx, `DELETE FROM supplier_categories WHERE supplier_id = ?`, doc.ID); err != nil {
		return fmt.Errorf("sqlite: clear categories %s: %w", doc.ID, err)
	}
	if len(doc.Categories) > 0 {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT OR IGNORE INTO supplier_categories (supplier_id, category) VALUES (?, ?)`)
		if err != nil {
			return fmt.Errorf("sqlite: prepare categories: %w", err)
		}
		for _, c := range doc.Categories {
			if c = strings.TrimSpace(c); c == "" {
				continue
			}
			if _, err := stmt.ExecContext(ctx, doc.ID, c); err != nil {
				_ = stmt.Close()
				return fmt.Errorf("sqlite: insert category %q: %w", c, err)
			}
		}
		_ = stmt.Close()
	}

	// Full-text index (trigram content + pinyin/synonyms) — same txn as
	// the document upsert, so the index never lags the data.
	if err := syncFTS(ctx, tx, doc); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit %s: %w", doc.ID, err)
	}
	return nil
}

// Get implements datamodel.SupplierStore. Archived documents ARE returned.
func (s *Store) Get(ctx context.Context, id string) (*domain.Supplier, error) {
	var docText string
	err := s.db.QueryRowContext(ctx,
		`SELECT json(doc) FROM suppliers WHERE id = ?`, id).Scan(&docText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, datamodel.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get %s: %w", id, err)
	}
	return decode(docText, id)
}

// Delete implements datamodel.SupplierStore with archive semantics: the row
// is retained with status=archived and ArchivedAt stamped (mirrors the
// memory adapter — the service layer also writes the change_log entry).
func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var docText string
	err = tx.QueryRowContext(ctx, `SELECT json(doc) FROM suppliers WHERE id = ?`, id).Scan(&docText)
	if errors.Is(err, sql.ErrNoRows) {
		return datamodel.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: delete-load %s: %w", id, err)
	}
	doc, err := decode(docText, id)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	doc.Status = domain.StatusArchived
	doc.ArchivedAt = &now
	doc.UpdatedAt = now
	payload, err := encode(doc)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
UPDATE suppliers
   SET status = ?, archived_at = ?, updated_at = ?, doc = jsonb(?)
 WHERE id = ?`,
		domain.StatusArchived,
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		payload, id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: archive %s: %w", id, err)
	}
	return tx.Commit()
}

// GetSetting implements datamodel.SupplierStore.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", datamodel.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get setting %s: %w", key, err)
	}
	return value, nil
}

// PutSetting implements datamodel.SupplierStore (upsert).
func (s *Store) PutSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("sqlite: put setting %s: %w", key, err)
	}
	return nil
}

// List implements datamodel.SupplierStore: filtered, keyset-paginated.
func (s *Store) List(ctx context.Context, q datamodel.Query) (datamodel.Page[*domain.Supplier], error) {
	q.Normalize()
	cur, err := datamodel.DecodeCursor(q.Cursor)
	if err != nil {
		return datamodel.Page[*domain.Supplier]{}, err
	}

	where, args := buildWhere(q.Filter)
	// Local-preference priority expression (local first), or "" when off.
	prioExpr, prioArgs := localPrioExpr(q.Filter)
	if cur.ID != "" {
		pred, cursorArgs, err := keysetPredicate(q.Sort, cur, prioExpr, prioArgs)
		if err != nil {
			return datamodel.Page[*domain.Supplier]{}, err
		}
		where = append(where, pred)
		args = append(args, cursorArgs...)
	}

	col := sortColumn(q.Sort.Field)
	dir := "DESC"
	if q.Sort.Order == datamodel.OrderAsc {
		dir = "ASC"
	}

	sqlStr := `SELECT s.id, json(s.doc) FROM suppliers s`
	if len(where) > 0 {
		sqlStr += "\n WHERE " + strings.Join(where, "\n   AND ")
	}
	// Fetch one extra row to detect a next page (no OFFSET, no COUNT).
	order := fmt.Sprintf("\n ORDER BY s.%s %s, s.id %s\n LIMIT ?", col, dir, dir)
	if prioExpr != "" {
		// Local suppliers (prio DESC) lead every page, then the normal order.
		order = fmt.Sprintf("\n ORDER BY (%s) DESC, s.%s %s, s.id %s\n LIMIT ?",
			prioExpr, col, dir, dir)
		args = append(args, prioArgs...)
	}
	sqlStr += order
	args = append(args, q.Limit+1)

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return datamodel.Page[*domain.Supplier]{}, fmt.Errorf("sqlite: list: %w", err)
	}
	defer rows.Close()

	items := make([]*domain.Supplier, 0, q.Limit)
	for rows.Next() {
		var id, docText string
		if err := rows.Scan(&id, &docText); err != nil {
			return datamodel.Page[*domain.Supplier]{}, fmt.Errorf("sqlite: list scan: %w", err)
		}
		doc, err := decode(docText, id)
		if err != nil {
			return datamodel.Page[*domain.Supplier]{}, err
		}
		items = append(items, doc)
	}
	if err := rows.Err(); err != nil {
		return datamodel.Page[*domain.Supplier]{}, fmt.Errorf("sqlite: list rows: %w", err)
	}

	page := datamodel.Page[*domain.Supplier]{Items: items}
	if len(items) > q.Limit {
		last := items[q.Limit-1]
		page.Items = items[:q.Limit]
		page.NextCursor = datamodel.EncodeCursor(datamodel.Cursor{
			SortKey: sortKeyValue(last, q.Sort.Field),
			ID:      last.ID,
			Prio: datamodel.LocalPrio(last.BasicInfo.Region.Province, last.BasicInfo.Region.City,
				q.Filter.PreferProvince, q.Filter.PreferCity),
		})
	}
	return page, nil
}

// ---------- query building ----------

// buildWhere translates SupplierFilter to SQL predicates + bound args.
// Conditions combine with AND; categories match ANY (EXISTS over the side
// table). Mirrors memory.matchesFilter exactly.
func buildWhere(f datamodel.SupplierFilter) ([]string, []any) {
	var where []string
	var args []any
	add := func(pred string, v ...any) {
		where = append(where, pred)
		args = append(args, v...)
	}

	// Archive handling: explicit Status wins; otherwise archived docs are
	// hidden unless IncludeArchived.
	if f.Status != "" {
		add("s.status = ?", f.Status)
	} else if !f.IncludeArchived {
		add("s.status != ?", domain.StatusArchived)
	}
	if f.WatchedOnly {
		// Follow flag lives in the JSON document (low-frequency filter).
		add("json_extract(s.doc, '$.watched') = 1")
	}

	if f.Province != "" {
		add("s.province = ?", f.Province)
	}
	if f.City != "" {
		add("s.city = ?", f.City)
	}
	if f.District != "" {
		add("s.district = ?", f.District)
	}
	if len(f.Categories) > 0 {
		placeholders := strings.Repeat("?,", len(f.Categories))
		placeholders = strings.TrimSuffix(placeholders, ",")
		add(fmt.Sprintf(
			"EXISTS (SELECT 1 FROM supplier_categories c WHERE c.supplier_id = s.id AND c.category IN (%s))",
			placeholders),
			toAnySlice(f.Categories)...)
	}
	if f.MinQualRank > 0 {
		add("s.qual_rank >= ?", f.MinQualRank)
	}
	if f.MinRating > 0 {
		add("s.rating >= ?", f.MinRating)
	}
	if f.MaxRatingSet {
		add("s.rating <= ?", f.MaxRating)
	}
	if f.OwnerID != "" {
		add("s.owner = ?", f.OwnerID)
	}
	if f.VisibilityMax != nil {
		add("s.visibility <= ?", *f.VisibilityMax)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		// Full-text search: ≥3-rune terms use the FTS5 trigram index
		// (Chinese substring + pinyin + synonyms); shorter terms fall back
		// to LIKE on search_text (trigram cannot index sub-3-char phrases).
		where, args = keywordPredicates(where, args, kw)
	}
	return where, args
}

// keysetPredicate builds the tuple comparison for cursor position:
//
//	desc: (col < key) OR (col = key AND id < curID)
//	asc:  (col > key) OR (col = key AND id > curID)
//
// localPrioExpr returns the SQL for the local-first priority CASE and its
// args, repeated for each occurrence. Returns "" when no preference is set.
// Must match datamodel.LocalPrio exactly.
func localPrioExpr(f datamodel.SupplierFilter) (string, []any) {
	if f.PreferProvince == "" {
		return "", nil
	}
	expr := "(CASE WHEN s.province = ? AND (? = '' OR s.city = ?) THEN 1 ELSE 0 END)"
	args := []any{f.PreferProvince, f.PreferCity, f.PreferCity}
	return expr, args
}

// keysetPredicate builds the WHERE clause selecting rows strictly after the
// cursor tuple. With a local-preference hint the leading key is the local
// priority (always DESC); otherwise it is the two-key (sort column, id).
func keysetPredicate(sort datamodel.Sort, cur datamodel.Cursor, prioExpr string, prioArgs []any) (string, []any, error) {
	col := sortColumn(sort.Field)
	key, err := cursorKeyParam(sort.Field, cur.SortKey)
	if err != nil {
		return "", nil, err
	}
	cmp := "<"
	if sort.Order == datamodel.OrderAsc {
		cmp = ">"
	}
	tail := fmt.Sprintf("(s.%[1]s %[2]s ? OR (s.%[1]s = ? AND s.id %[2]s ?))", col, cmp)
	tailArgs := []any{key, key, cur.ID}

	if prioExpr == "" {
		return tail, tailArgs, nil
	}
	// (prio < cp) OR (prio = cp AND <existing 2-key tail>)
	// Priority is always DESC so later rows have an equal-or-lower priority.
	// Bind order follows textual placeholder occurrence: the CASE repeats
	// (3 args each), so args = prioArgs, cp, prioArgs, cp, then tail.
	args := make([]any, 0, len(prioArgs)*2+2+len(tailArgs))
	args = append(args, prioArgs...)
	args = append(args, cur.Prio)
	args = append(args, prioArgs...)
	args = append(args, cur.Prio)
	args = append(args, tailArgs...)
	pred := fmt.Sprintf("((%s) < ? OR ((%s) = ? AND %s))", prioExpr, prioExpr, tail)
	return pred, args, nil
}

func sortColumn(f datamodel.SortField) string {
	switch f {
	case datamodel.SortUpdatedAt:
		return "updated_at"
	case datamodel.SortRating:
		return "rating"
	case datamodel.SortName:
		return "name"
	default:
		return "created_at"
	}
}

// cursorKeyParam converts the opaque cursor sort key into the SQL bind
// type matching its column: float for rating, text for time/name.
func cursorKeyParam(f datamodel.SortField, key string) (any, error) {
	if f == datamodel.SortRating {
		v, err := strconv.ParseFloat(key, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: bad rating cursor key %q", datamodel.ErrInvalidPagination, key)
		}
		return v, nil
	}
	return key, nil
}

// sortKeyValue renders a document's sort key in the SAME format the memory
// adapter uses for cursors (RFC3339Nano for times, fixed-width for rating),
// so a cursor is interchangeable across adapters in principle.
func sortKeyValue(d *domain.Supplier, f datamodel.SortField) string {
	switch f {
	case datamodel.SortUpdatedAt:
		return d.UpdatedAt.UTC().Format(time.RFC3339Nano)
	case datamodel.SortRating:
		return strconv.FormatFloat(d.Rating, 'f', 6, 64)
	case datamodel.SortName:
		return d.BasicInfo.CompanyName
	default:
		return d.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
}

// ---------- helpers ----------

func encode(d *domain.Supplier) (string, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("sqlite: marshal %s: %w", d.ID, err)
	}
	return string(b), nil
}

func decode(docText, id string) (*domain.Supplier, error) {
	var out domain.Supplier
	if err := json.Unmarshal([]byte(docText), &out); err != nil {
		return nil, fmt.Errorf("sqlite: decode %s: %w", id, err)
	}
	return &out, nil
}

// buildSearchText flattens the keyword-searchable fields into one blob,
// matching memory.matchesKeyword. Chinese text is matched as substrings;
// LOWER() handles ASCII case-insensitivity.
//
// The blob also carries index-time synonym expansions (商砼 ↔ 混凝土…):
// it backs BOTH the LIKE fallback (sub-3-rune terms, which FTS5 trigram
// cannot index) and the FTS5 content column, so synonym lookups work on
// either path — searching "商砼" finds a document that only says
// "混凝土" and vice-versa.
// buildSearchText is the adapter-local alias for the engine-agnostic blob
// builder; FTS content and the search_text column both derive from it, and
// the memory adapter indexes the same text via search.DocumentText.
func buildSearchText(d *domain.Supplier) string {
	return search.DocumentText(d)
}

// escapeLike escapes LIKE metacharacters so keyword input is literal text
// (paired with ESCAPE '\' in the query).
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
