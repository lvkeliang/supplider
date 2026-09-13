// Package semantic implements 语义搜索 (TR-19-E): a natural-language query is
// embedded by the AI gateway and matched against pre-indexed supplier vectors
// by cosine similarity. It bridges the two ports — aigateway.Gateway (Embed)
// and vectorstore.Store (cosine ranking) — so it depends on no concrete
// provider or index implementation.
//
// The index is populated via Rebuild/Index (called from POST /ai/index after
// a supplier is created or edited). No embedding model configured → the
// gateway's Embed returns aigateway.ErrAIDisabled and the whole feature stays
// hidden (AI 原生但可降级); keyword + structured filter search remain the
// non-AI path.
package semantic

import (
	"context"
	"fmt"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/vectorstore"
)

// Doc is one supplier's indexable text. Callers build Text with
// search.DocumentText so semantic and keyword search share the same corpus.
type Doc struct {
	ID   string
	Text string
}

// embedBatch caps how many texts go into one Embed call. OpenAI-style
// /embeddings endpoints reject oversized batches; chunking also keeps a
// partial failure from discarding a whole library.
const embedBatch = 64

// Searcher coordinates embedding + vector similarity. Tier-agnostic: it holds
// only interface values and is safe for concurrent use (the gateway and index
// are each concurrency-safe).
type Searcher struct {
	gw    aigateway.Gateway
	index vectorstore.Store
}

// New wires a searcher over the given gateway and vector index.
func New(gw aigateway.Gateway, index vectorstore.Store) *Searcher {
	return &Searcher{gw: gw, index: index}
}

// Index embeds docs and upserts their vectors, in batches of embedBatch.
// Docs with empty/whitespace-only Text are skipped. Returned embeddings are
// zipped positionally against the input — the OpenAI contract returns data
// items in input order. It reports how many docs were actually indexed.
func (s *Searcher) Index(ctx context.Context, docs []Doc) (int, error) {
	texts := make([]string, 0, len(docs))
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		if strings.TrimSpace(d.Text) == "" {
			continue
		}
		texts = append(texts, d.Text)
		ids = append(ids, d.ID)
	}
	if len(texts) == 0 {
		return 0, nil
	}

	indexed := 0
	for start := 0; start < len(texts); start += embedBatch {
		end := start + embedBatch
		if end > len(texts) {
			end = len(texts)
		}
		items, err := s.gw.Embed(ctx, texts[start:end])
		if err != nil {
			return indexed, err
		}
		if len(items) != end-start {
			return indexed, fmt.Errorf("semantic: embed returned %d vectors for %d texts", len(items), end-start)
		}
		for i, it := range items {
			if len(it.Vector) == 0 {
				continue
			}
			if err := s.index.Upsert(ctx, ids[start+i], it.Vector); err != nil {
				return indexed, err
			}
			indexed++
		}
	}
	return indexed, nil
}

// Rebuild clears the index then re-indexes docs. A full rebuild is the
// correctness primitive for the non-persistent index: stale vectors from
// archived/deleted suppliers are dropped rather than drifting out of sync.
func (s *Searcher) Rebuild(ctx context.Context, docs []Doc) (int, error) {
	if err := s.index.Clear(ctx); err != nil {
		return 0, err
	}
	return s.Index(ctx, docs)
}

// Search embeds a query and returns up to topK supplier ids ranked by
// descending cosine similarity. An empty query returns nil (no call to the
// provider).
func (s *Searcher) Search(ctx context.Context, query string, topK int) ([]vectorstore.Scored, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	items, err := s.gw.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items[0].Vector) == 0 {
		return nil, nil
	}
	return s.index.Search(ctx, items[0].Vector, topK)
}

// Len reports how many vectors are currently indexed.
func (s *Searcher) Len() int { return s.index.Len() }
