package aigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestResponseCacheGetPut(t *testing.T) {
	c := NewResponseCache(0, 0)
	if _, ok := c.Get("k"); ok {
		t.Fatal("empty cache must miss")
	}
	c.Put("k", ChatResponse{Text: "hello", TokensIn: 5})
	got, ok := c.Get("k")
	if !ok || got.Text != "hello" {
		t.Fatalf("get = %+v, ok=%v", got, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("len = %d", c.Len())
	}
}

func TestResponseCacheEvictsOldest(t *testing.T) {
	c := NewResponseCache(2, 0)
	c.Put("a", ChatResponse{Text: "A"})
	c.Put("b", ChatResponse{Text: "B"})
	c.Put("c", ChatResponse{Text: "C"}) // evicts "a" (FIFO)
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("oldest entry must be evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b should survive")
	}
}

func TestResponseCacheRefreshKeepsOne(t *testing.T) {
	c := NewResponseCache(0, 0)
	c.Put("k", ChatResponse{Text: "1"})
	c.Put("k", ChatResponse{Text: "2"})
	if c.Len() != 1 {
		t.Fatalf("len = %d, want 1", c.Len())
	}
	got, _ := c.Get("k")
	if got.Text != "2" {
		t.Fatalf("refreshed text = %q", got.Text)
	}
}

func TestResponseCacheTTLExpiry(t *testing.T) {
	c := NewResponseCache(0, 1*time.Millisecond)
	c.Put("k", ChatResponse{Text: "x"})
	time.Sleep(10 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expired entry must miss")
	}
}

func TestFingerprintDeterminismAndSensitivity(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: "hello"}}
	k1 := fingerprint("t", "model-a", "https://x", false, msgs)
	k2 := fingerprint("t", "model-a", "https://x", false, msgs)
	if k1 != k2 {
		t.Fatal("identical input must hash identically")
	}
	// Model / base / task / content / JSON-mode changes must invalidate.
	if fingerprint("t2", "model-a", "https://x", false, msgs) == k1 {
		t.Error("task change must invalidate")
	}
	if fingerprint("t", "model-b", "https://x", false, msgs) == k1 {
		t.Error("model change must invalidate")
	}
	if fingerprint("t", "model-a", "https://y", false, msgs) == k1 {
		t.Error("base change must invalidate")
	}
	if fingerprint("t", "model-a", "https://x", true, msgs) == k1 {
		t.Error("json-mode change must invalidate")
	}
	if fingerprint("t", "model-a", "https://x", false, []ChatMessage{{Role: "user", Content: "world"}}) == k1 {
		t.Error("content change must invalidate")
	}
}

// countingUpstream serves OpenAI chat completions and counts calls, so a test
// can prove a cache hit avoids a provider round-trip.
func countingUpstream(t *testing.T) (*httptest.Server, *int, *sync.Mutex) {
	t.Helper()
	var n int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	return srv, &n, &mu
}

func cachedGateway(t *testing.T, upstream *httptest.Server) *Service {
	t.Helper()
	gw := New(Config{Format: FormatOpenAI, BaseURL: upstream.URL, APIKey: "k", Model: "m"}, nil)
	gw.SetResponseCache(NewResponseCache(0, 0))
	return gw
}

func TestServiceCacheShortCircuitsIdenticalCalls(t *testing.T) {
	up, count, mu := countingUpstream(t)
	defer up.Close()
	gw := cachedGateway(t, up)
	ctx := context.Background()
	req := ChatRequest{Task: "summarize", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}

	if _, err := gw.Complete(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := gw.Complete(ctx, req); err != nil { // identical → cached
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if *count != 1 {
		t.Fatalf("provider calls = %d, want 1 (second call must hit cache)", *count)
	}
}

func TestServiceCacheMissOnDifferentPrompt(t *testing.T) {
	up, count, mu := countingUpstream(t)
	defer up.Close()
	gw := cachedGateway(t, up)
	ctx := context.Background()

	_, _ = gw.Complete(ctx, ChatRequest{Task: "summarize", Messages: []ChatMessage{{Role: "user", Content: "a"}}})
	_, _ = gw.Complete(ctx, ChatRequest{Task: "summarize", Messages: []ChatMessage{{Role: "user", Content: "b"}}})

	mu.Lock()
	defer mu.Unlock()
	if *count != 2 {
		t.Fatalf("provider calls = %d, want 2 (different prompts must both hit provider)", *count)
	}
}

func TestServiceCacheBypassesImages(t *testing.T) {
	up, count, mu := countingUpstream(t)
	defer up.Close()
	gw := cachedGateway(t, up)
	ctx := context.Background()

	req := ChatRequest{Task: "ocr", Messages: []ChatMessage{{Role: "user", Content: "p", ImageBase64: "img"}}}
	if _, err := gw.Complete(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := gw.Complete(ctx, req); err != nil { // image call → never cached
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if *count != 2 {
		t.Fatalf("provider calls = %d, want 2 (image calls must not be cached)", *count)
	}
}
