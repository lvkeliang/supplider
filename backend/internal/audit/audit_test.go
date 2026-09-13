package audit

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakeStore() *fakeStore { return &fakeStore{m: map[string]string{}} }

func (f *fakeStore) GetSetting(_ context.Context, k string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.m[k]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (f *fakeStore) PutSetting(_ context.Context, k, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[k] = v
	return nil
}

func TestRecorderStoresNewestFirst(t *testing.T) {
	st := newFakeStore()
	r := New(st)
	ctx := context.Background()

	r.Record(ctx, "create", "sup_1", "甲公司")
	r.Record(ctx, "blacklist", "sup_1", "甲公司")
	r.Record(ctx, "archive", "sup_2", "乙公司")

	items := r.List(ctx, 0)
	if len(items) != 3 {
		t.Fatalf("len = %d, want 3", len(items))
	}
	if items[0].Action != "archive" || items[0].Name != "乙公司" {
		t.Fatalf("newest first: got %+v", items[0])
	}
	if items[2].Action != "create" {
		t.Fatalf("oldest last: got %+v", items[2])
	}
}

func TestRecorderPersistsAcrossInstances(t *testing.T) {
	st := newFakeStore()
	ctx := context.Background()

	New(st).Record(ctx, "merge", "sup_1", "甲公司")

	// A fresh Recorder over the same store reads the persisted trail.
	items := New(st).List(ctx, 0)
	if len(items) != 1 || items[0].Action != "merge" {
		t.Fatalf("reloaded = %+v", items)
	}
}

func TestRecorderCapsTrail(t *testing.T) {
	st := newFakeStore()
	ctx := context.Background()

	// defaultCap is 200; push more and assert it stays bounded.
	for i := 0; i < defaultCap+30; i++ {
		New(st).Record(ctx, "create", "x", "n") // fresh recorder each time → reloads, demonstrates persistence too
	}
	items := New(st).List(ctx, 0)
	if len(items) != defaultCap {
		t.Fatalf("len = %d, want %d", len(items), defaultCap)
	}
}

func TestRecorderSurvivesStoreFailure(t *testing.T) {
	r := New(failingStore{})
	ctx := context.Background()
	r.Record(ctx, "create", "sup_1", "甲公司") // must not panic
	_ = r.List(ctx, 10)
}

type failingStore struct{}

func (failingStore) GetSetting(context.Context, string) (string, error) {
	return "", errors.New("boom")
}
func (failingStore) PutSetting(context.Context, string, string) error { return errors.New("boom") }
