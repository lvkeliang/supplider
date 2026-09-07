// Package search defines the full-text search port.
//
// Adapters (all behind the same interface):
//   - SQLite FTS5 (personal MVP transition option)
//   - embedded Meilisearch (personal target)
//   - Elasticsearch (enterprise option)
//
// Chinese tokenization, pinyin and fuzzy matching live inside adapters;
// business code only sees Document / Query / Result.
package search

import "context"

// Document is the searchable projection of a supplier (plus other entities
// later). Kept denormalized on purpose.
type Document struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	CreditCode string            `json:"credit_code,omitempty"`
	Region     string            `json:"region,omitempty"` // "浙江 杭州 余杭区"
	Categories []string          `json:"categories,omitempty"`
	Keywords   []string          `json:"keywords,omitempty"` // pinyin/synonyms injected by adapter
	Tags       map[string]string `json:"tags,omitempty"`     // structured filter facets
}

// Query is a search request: text + hard filters (资质/地域 are hard filters,
// per PRD) + performance-bounded limits.
type Query struct {
	Text        string
	Province    string
	City        string
	Categories  []string
	MinQualRank int
	Limit       int
	Offset      int
}

// Result is one hit. Snippet contains the highlighted match fragment.
type Result struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

// Index is the search-engine port.
type Index interface {
	// Upsert adds or replaces documents.
	Upsert(ctx context.Context, docs ...Document) error
	// Remove deletes documents by id.
	Remove(ctx context.Context, ids ...string) error
	// Search runs a query; implementations MUST return within the 3s
	// budget — partial results with a timeout flag are preferred over
	// slow full results (red line: search <200ms p50, 3s hard timeout).
	Search(ctx context.Context, q Query) ([]Result, error)
	// Close releases resources.
	Close() error
}
