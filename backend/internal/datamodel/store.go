package datamodel

import (
	"context"

	"github.com/supplider/supplider/backend/internal/domain"
)

// MaxPageSize is the hard performance red line: list endpoints never return
// more than 100 documents in one call.
const MaxPageSize = 100

// DefaultPageSize applies when the caller does not specify a limit.
const DefaultPageSize = 20

// SupplierStore is the persistence port for supplier documents.
//
// Implementations: memory (now), SQLite JSONB (personal, next), MongoDB
// (enterprise, later). Business code and HTTP/CLI handlers depend on THIS
// interface only — grep of business packages must never show a database
// driver import.
type SupplierStore interface {
	// Put inserts or fully replaces a supplier document (upsert by ID).
	// Implementations set no domain fields; the service layer owns
	// ID generation, timestamps and change_log.
	Put(ctx context.Context, s *domain.Supplier) error

	// Get fetches one document by ID. Archived documents ARE returned
	// (history must remain accessible); List hides them by default.
	// Returns ErrNotFound when the id does not exist at all.
	Get(ctx context.Context, id string) (*domain.Supplier, error)

	// Delete archives a supplier (archive semantics — the document is
	// retained with status=archived and ArchivedAt set).
	Delete(ctx context.Context, id string) error

	// List returns a keyset-paginated, filtered page. Queries never use
	// OFFSET: deep pagination walks the opaque Cursor instead.
	List(ctx context.Context, q Query) (Page[*domain.Supplier], error)

	// GetSetting reads one admin/config value by key. Settings are small
	// named JSON/text values that belong with the supplier data (policy,
	// admin configuration) so they travel with the same backup/migration
	// path. Returns ErrNotFound when the key has never been written.
	GetSetting(ctx context.Context, key string) (string, error)

	// PutSetting upserts one admin/config value by key.
	PutSetting(ctx context.Context, key, value string) error

	// Ping verifies the backing store is reachable (used by /readyz).
	Ping(ctx context.Context) error

	// Close releases underlying connections/files.
	Close() error
}

// Query is one list request: filter + sort + keyset page.
type Query struct {
	Filter SupplierFilter
	Sort   Sort
	// Limit is capped at MaxPageSize by Normalize.
	Limit int
	// Cursor is the opaque NextCursor from the previous page; empty = first.
	Cursor string
}

// SupplierFilter is the structured, multi-dimensional filter (条件筛选).
// All conditions combine with AND; within a slice field (Categories) the
// semantics are OR (supplier matches if it carries ANY listed category).
type SupplierFilter struct {
	// Region (地域): each non-empty level must match exactly.
	Province string
	City     string
	District string

	// Categories (品类): supplier matches if it carries ANY of these.
	Categories []string

	// MinQualRank (资质等级): supplier needs at least one qualification
	// at this rank or higher. Compare with domain.QualRank("二级").
	MinQualRank int

	// Rating range (评分); 0 means "no bound".
	MinRating float64
	MaxRating float64

	// OwnerID restricts to suppliers owned by this user.
	OwnerID string

	// VisibilityMax restricts to visibility level <= this value
	// (used by the five-level visibility system later).
	VisibilityMax *int

	// Status filters by lifecycle status; empty means live records only
	// (i.e. status != archived, unless IncludeArchived is set).
	Status string

	// IncludeArchived includes archived documents in results.
	IncludeArchived bool

	// Keyword is a free-text hint. Structured stores may ignore it
	// (full-text search belongs to the search.Index port); adapters that
	// lack a separate index — e.g. memory — may do simple substring
	// matching so the CLI works without a search engine.
	Keyword string
}

// SortField enumerates sortable keys.
type SortField string

const (
	SortCreatedAt SortField = "created_at"
	SortUpdatedAt SortField = "updated_at"
	SortRating    SortField = "rating"
	SortName      SortField = "name"
)

type SortOrder string

const (
	OrderDesc SortOrder = "desc"
	OrderAsc  SortOrder = "asc"
)

// Sort specifies one sort key; keyset pagination ties the cursor to it.
type Sort struct {
	Field SortField
	Order SortOrder
}

// Page is one paginated result. NextCursor is empty on the final page.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Normalize clamps Limit into [1, MaxPageSize] and defaults sort.
func (q *Query) Normalize() {
	if q.Limit <= 0 {
		q.Limit = DefaultPageSize
	}
	if q.Limit > MaxPageSize {
		q.Limit = MaxPageSize
	}
	if q.Sort.Field == "" {
		q.Sort.Field = SortCreatedAt
	}
	if q.Sort.Order != OrderAsc {
		q.Sort.Order = OrderDesc
	}
}

// Cursor is the keyset position (opaque to callers). It encodes the sort
// key value + ID of the last returned document; adapters compare tuples.
type Cursor struct {
	// SortKey is the comparable sort value as RFC3339 time or numeric
	// string, depending on Sort.Field.
	SortKey string `json:"k"`
	ID      string `json:"i"`
}
