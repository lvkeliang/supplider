package usage

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// fakeStore is an in-memory settings store for tests.
type fakeStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakeStore() *fakeStore { return &fakeStore{m: map[string]string{}} }

func (f *fakeStore) GetSetting(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.m[key]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}

func (f *fakeStore) PutSetting(_ context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[key] = value
	return nil
}

func TestTrackerAccumulatesPerTask(t *testing.T) {
	store := newFakeStore()
	tr := New(store)
	ctx := context.Background()

	tr.Record(ctx, "summarize", 100, 20)
	tr.Record(ctx, "summarize", 150, 30)
	tr.Record(ctx, "compare", 200, 40)

	sum := tr.Current(ctx)
	if sum.Total.Calls != 3 || sum.Total.TokensIn != 450 || sum.Total.TokensOut != 90 {
		t.Fatalf("total = %+v, want calls=3 in=450 out=90", sum.Total)
	}
	if sum.ByTask["summarize"].Calls != 2 || sum.ByTask["summarize"].TokensIn != 250 {
		t.Fatalf("summarize = %+v", sum.ByTask["summarize"])
	}
	if sum.ByTask["compare"].Calls != 1 || sum.ByTask["compare"].TokensOut != 40 {
		t.Fatalf("compare = %+v", sum.ByTask["compare"])
	}
	if sum.Month == "" {
		t.Fatal("month must be set")
	}
}

func TestTrackerPersistsAcrossInstances(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()

	New(store).Record(ctx, "nl2filter", 300, 60)

	// A fresh Tracker over the same store sees the persisted counters.
	sum := New(store).Current(ctx)
	if sum.Total.Calls != 1 || sum.Total.TokensIn != 300 {
		t.Fatalf("reloaded = %+v, want calls=1 in=300", sum.Total)
	}
}

func TestTrackerSkipsZeroTokens(t *testing.T) {
	store := newFakeStore()
	tr := New(store)
	ctx := context.Background()

	tr.Record(ctx, "ping", 0, 0) // zero usage → no record
	sum := tr.Current(ctx)
	if sum.Total.Calls != 0 {
		t.Fatalf("zero-token call must not be recorded, got calls=%d", sum.Total.Calls)
	}
}

func TestTrackerSurvivesStoreFailure(t *testing.T) {
	var flaky Store = &failingStore{}
	tr := New(flaky)
	ctx := context.Background()
	tr.Record(ctx, "summarize", 10, 5) // must not panic / propagate
}

type failingStore struct{}

func (failingStore) GetSetting(context.Context, string) (string, error) {
	return "", errors.New("failed")
}
func (failingStore) PutSetting(context.Context, string, string) error { return errors.New("failed") }
