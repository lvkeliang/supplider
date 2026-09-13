package semantic

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/vectorstore/memory"
)

// fakeGateway is a deterministic embedding provider: text maps to a 64-dim
// vector via multiplicative per-rune hashing, so two texts sharing runes get
// a higher cosine similarity than disjoint ones. No network involved.
type fakeGateway struct {
	vectors  map[string][]float32 // optional canned vector per text (nil = derive)
	embedErr error
	calls    int
}

func (f *fakeGateway) Enabled() bool { return true }

func (f *fakeGateway) Complete(context.Context, aigateway.ChatRequest) (aigateway.ChatResponse, error) {
	return aigateway.ChatResponse{}, nil
}

func (f *fakeGateway) Embed(_ context.Context, texts []string) ([]aigateway.EmbeddingItem, error) {
	if f.embedErr != nil {
		return nil, f.embedErr
	}
	f.calls++
	out := make([]aigateway.EmbeddingItem, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = aigateway.EmbeddingItem{ID: strconv.Itoa(i), Vector: v}
			continue
		}
		out[i] = aigateway.EmbeddingItem{ID: strconv.Itoa(i), Vector: derive(t)}
	}
	return out, nil
}

// derive hashes each rune into one of 64 buckets with sign ±1. Overlapping
// runes between query and document raise cosine similarity.
func derive(text string) []float32 {
	v := make([]float32, 64)
	for _, r := range text {
		h := uint32(r) * 2654435761
		idx := h % 64
		if h&(1<<31) != 0 {
			v[idx]++
		} else {
			v[idx]--
		}
	}
	return v
}

func newTestSearcher() (*Searcher, *fakeGateway, *memory.Store) {
	gw := &fakeGateway{vectors: map[string][]float32{}}
	idx := memory.New()
	return New(gw, idx), gw, idx
}

func TestIndexSkipsEmptyTexts(t *testing.T) {
	s, gw, idx := newTestSearcher()
	n, err := s.Index(context.Background(), []Doc{
		{ID: "a", Text: "杭州混凝土"},
		{ID: "b", Text: "   "},
		{ID: "c", Text: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("indexed %d, want 1", n)
	}
	if idx.Len() != 1 {
		t.Fatalf("len %d, want 1", idx.Len())
	}
	if gw.calls != 1 {
		t.Fatalf("embed calls %d, want 1 (empty texts filtered before batching)", gw.calls)
	}
}

func TestIndexPropagatesEmbedError(t *testing.T) {
	s, gw, _ := newTestSearcher()
	gw.embedErr = errors.New("boom")
	_, err := s.Index(context.Background(), []Doc{{ID: "a", Text: "x"}})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestIndexRejectsMismatchedVectorCount(t *testing.T) {
	_, gw, _ := newTestSearcher()
	// Wrap the fake to return one fewer vector than requested, exercising the
	// length guard that would otherwise panic on a short zip.
	s := New(&dropOneGateway{inner: gw}, memory.New())
	_, err := s.Index(context.Background(), []Doc{{ID: "a", Text: "甲"}, {ID: "b", Text: "乙"}})
	if err == nil {
		t.Fatal("want error for mismatched embed count")
	}
}

// dropOneGateway returns n-1 vectors for n texts, exercising the length guard.
type dropOneGateway struct{ inner *fakeGateway }

func (d *dropOneGateway) Enabled() bool { return true }
func (d *dropOneGateway) Complete(ctx context.Context, r aigateway.ChatRequest) (aigateway.ChatResponse, error) {
	return d.inner.Complete(ctx, r)
}
func (d *dropOneGateway) Embed(ctx context.Context, texts []string) ([]aigateway.EmbeddingItem, error) {
	items, err := d.inner.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	return items[:len(items)-1], nil
}

func TestSearchReturnsNilForEmptyQuery(t *testing.T) {
	s, gw, _ := newTestSearcher()
	got, err := s.Search(context.Background(), "   ", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if gw.calls != 0 {
		t.Fatalf("embed calls %d, want 0 (empty query must not hit provider)", gw.calls)
	}
}

func TestSearchRanksMostSimilarFirst(t *testing.T) {
	s, _, _ := newTestSearcher()
	_, err := s.Index(context.Background(), []Doc{
		{ID: "a", Text: "杭州混凝土施工"},
		{ID: "b", Text: "北京贸易建材"},
		{ID: "c", Text: "上海物流运输"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Search(context.Background(), "杭州混凝土", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("results %d, want 3", len(got))
	}
	if got[0].ID != "a" {
		t.Fatalf("top result = %s, want a (results %+v)", got[0].ID, got)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("scores not descending: %+v", got)
	}
}

func TestRebuildDropsStaleVectors(t *testing.T) {
	s, _, idx := newTestSearcher()
	if _, err := s.Index(context.Background(), []Doc{{ID: "a", Text: "杭州混凝土"}, {ID: "b", Text: "北京贸易"}}); err != nil {
		t.Fatal(err)
	}
	// Rebuild with only b: a's vector must be gone.
	if _, err := s.Rebuild(context.Background(), []Doc{{ID: "b", Text: "北京贸易"}}); err != nil {
		t.Fatal(err)
	}
	if idx.Len() != 1 {
		t.Fatalf("len %d, want 1 after rebuild", idx.Len())
	}
	got, err := s.Search(context.Background(), "杭州混凝土", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.ID == "a" {
			t.Fatalf("stale vector for a survived rebuild: %+v", got)
		}
	}
}
