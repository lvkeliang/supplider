// Package memory is the reference vector store: an in-memory map with
// brute-force cosine ranking. It is the zero-dependency personal-tier
// implementation and the behavioural reference the SQLite/Qdrant/Milvus
// adapters must match. Not persistent — the persistent SQLite adapter is a
// later slice.
package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/supplider/supplider/backend/internal/vectorstore"
)

// Store is a concurrency-safe in-memory vector index.
type Store struct {
	mu   sync.RWMutex
	data map[string][]float32
}

// New returns an empty store.
func New() *Store {
	return &Store{data: map[string][]float32{}}
}

func (s *Store) Upsert(_ context.Context, id string, vector []float32) error {
	v := make([]float32, len(vector))
	copy(v, vector)
	s.mu.Lock()
	s.data[id] = v
	s.mu.Unlock()
	return nil
}

func (s *Store) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	delete(s.data, id)
	s.mu.Unlock()
	return nil
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

func (s *Store) Clear(_ context.Context) error {
	s.mu.Lock()
	s.data = map[string][]float32{}
	s.mu.Unlock()
	return nil
}

// Close is a no-op for the in-memory store.
func (s *Store) Close() error { return nil }

// Search ranks every stored vector by cosine similarity to the query and
// returns the topK. Dimension mismatches are skipped (a stored vector whose
// length differs from the query is simply not comparable).
func (s *Store) Search(_ context.Context, vector []float32, topK int) ([]vectorstore.Scored, error) {
	s.mu.RLock()
	type pair struct {
		id    string
		score float64
	}
	scored := make([]pair, 0, len(s.data))
	for id, v := range s.data {
		sim, err := vectorstore.Cosine(vector, v)
		if err != nil {
			continue
		}
		scored = append(scored, pair{id, sim})
	}
	s.mu.RUnlock()

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
