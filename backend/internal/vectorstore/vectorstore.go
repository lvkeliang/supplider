// Package vectorstore is the vector-similarity port (接口先行，实现可换).
// Semantic search (TR-19-E) stores one embedding per supplier and ranks a
// query embedding by cosine similarity.
//
// Implementations: memory (reference, personal, zero-dependency), SQLite
// BLOB table (persistent personal, later), Qdrant embedded / Milvus
// (small_business / enterprise, later). Business code depends on THIS
// interface only.
package vectorstore

import (
	"context"
	"errors"
	"math"
)

// ErrDimensionMismatch is returned when two vectors have different lengths.
var ErrDimensionMismatch = errors.New("vectorstore: dimension mismatch")

// Scored pairs a stored id with its cosine similarity to the query
// (higher = more similar).
type Scored struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// Store is the vector index port. Implementations must be safe for
// concurrent use.
type Store interface {
	// Upsert inserts or replaces the vector for id.
	Upsert(ctx context.Context, id string, vector []float32) error
	// Delete removes the vector for id (no-op if absent).
	Delete(ctx context.Context, id string) error
	// Search returns up to topK ids ranked by descending cosine similarity.
	Search(ctx context.Context, vector []float32, topK int) ([]Scored, error)
	// Clear removes every stored vector. A full re-embed (rebuild) clears
	// first so stale vectors from archived/deleted documents are dropped.
	Clear(ctx context.Context) error
	// Len reports how many vectors are stored.
	Len() int
	// Close releases underlying connections/files (no-op for the in-memory
	// reference store).
	Close() error
}

// Cosine returns the cosine similarity of two equal-length vectors, in
// [-1, 1]. A zero-length vector is treated as similarity 0.
func Cosine(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, ErrDimensionMismatch
	}
	if len(a) == 0 {
		return 0, nil
	}
	var dot, na, nb float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), nil
}
