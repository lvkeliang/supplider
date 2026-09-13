package aigateway

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// ResponseCache is a bounded, TTL'd cache of ChatResponses keyed by a
// fingerprint of the request. Identical prompts short-circuit the provider —
// saving tokens and latency on repeat calls / after a transient retry. It is
// best-effort: lookups/stores never error and never block beyond a mutex.
//
// Same-request determinism is safe for our chat features (re-summarizing the
// same archive, re-running the same NL/document query). Vision (image) calls
// are never cached — image bytes are large and not fingerprinted.
type ResponseCache struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	items map[string]cacheEntry
	order []string // FIFO eviction when full
}

type cacheEntry struct {
	expires time.Time
	resp    ChatResponse
}

// NewResponseCache builds a bounded, TTL'd cache. Non-positive max/ttl fall
// back to conservative defaults.
func NewResponseCache(max int, ttl time.Duration) *ResponseCache {
	if max <= 0 {
		max = 256
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &ResponseCache{max: max, ttl: ttl, items: map[string]cacheEntry{}}
}

// Get returns a live cached response (false when absent/expired).
func (c *ResponseCache) Get(key string) (ChatResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return ChatResponse{}, false
	}
	if time.Now().After(e.expires) {
		delete(c.items, key)
		c.removeOrder(key)
		return ChatResponse{}, false
	}
	return e.resp, true
}

// Put stores a response, refreshing an existing entry or evicting the oldest
// when the cache is full.
func (c *ResponseCache) Put(key string, resp ChatResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; exists {
		c.items[key] = cacheEntry{expires: time.Now().Add(c.ttl), resp: resp}
		return
	}
	if len(c.items) >= c.max {
		if len(c.order) > 0 {
			delete(c.items, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.items[key] = cacheEntry{expires: time.Now().Add(c.ttl), resp: resp}
	c.order = append(c.order, key)
}

// Len reports how many entries are currently cached.
func (c *ResponseCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *ResponseCache) removeOrder(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// fingerprint hashes everything that determines a completion's output: task,
// model, base URL, JSON mode and the messages (roles + content). The API key
// is deliberately excluded (it does not change the response, and including it
// would smear a secret into the cache key).
func fingerprint(task, model, base string, jsonMode bool, msgs []ChatMessage) string {
	h := sha256.New()
	_, _ = h.Write([]byte(task))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(model))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(base))
	_, _ = h.Write([]byte{0})
	if jsonMode {
		_, _ = h.Write([]byte{1})
	} else {
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write([]byte{0})
	for _, m := range msgs {
		_, _ = h.Write([]byte(m.Role))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m.Content))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
