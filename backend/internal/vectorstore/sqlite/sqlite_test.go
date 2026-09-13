package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRoundTripUpsertSearchDeleteClear(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.Upsert(ctx, "a", []float32{1, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(ctx, "b", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Fatalf("len = %d, want 2", s.Len())
	}

	got, err := s.Search(ctx, []float32{1, 1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a" {
		t.Fatalf("search = %+v, want a first", got)
	}

	// Upsert replaces the vector for an existing id: a becomes orthogonal to
	// the query, so b (positive similarity) must now rank first.
	if err := s.Upsert(ctx, "a", []float32{0, 0, 1}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Search(ctx, []float32{1, 1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != "b" {
		t.Fatalf("after replace, top = %s, want b", got[0].ID)
	}

	if err := s.Delete(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("len after delete = %d, want 1", s.Len())
	}

	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 0 {
		t.Fatalf("len after clear = %d, want 0", s.Len())
	}
}

func TestSearchSkipsDimensionMismatch(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.Upsert(ctx, "a", []float32{1, 0, 0})
	got, err := s.Search(ctx, []float32{1, 0}, 10) // 2-dim query vs 3-dim vector
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("mismatched query must yield no results, got %+v", got)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.db")
	ctx := context.Background()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(ctx, "a", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Len() != 1 {
		t.Fatalf("len after reopen = %d, want 1 (vector must persist)", s2.Len())
	}
	got, err := s2.Search(ctx, []float32{1, 0, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("after reopen search = %+v, want a", got)
	}
}

func TestEncodeDecodeVectorRoundTrip(t *testing.T) {
	in := []float32{1, 0, -0.5, 3.25, -2}
	blob := encodeVector(in)
	out := decodeVector(blob)
	if len(out) != len(in) {
		t.Fatalf("decoded len %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("decoded[%d] = %v, want %v", i, out[i], in[i])
		}
	}
}
