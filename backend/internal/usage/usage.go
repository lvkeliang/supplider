// Package usage tracks cumulative AI token consumption per calendar month,
// persisted as a small JSON blob in the supplier settings store (so it travels
// with the library backup/migration and survives a restart). It implements the
// aigateway.UsageSink port: after each chat completion the gateway reports
// (task, tokensIn, tokensOut) and the tracker folds it into the running month.
//
// It is best-effort by design — a storage failure never surfaces to the AI
// caller. The GET /ai/usage endpoint reads the current month's counters.
package usage

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Counter is a cumulative token/call record for one month (or one task).
type Counter struct {
	Calls     int `json:"calls"`
	TokensIn  int `json:"tokens_in"`
	TokensOut int `json:"tokens_out"`
}

// Summary is the aggregate for one month (used by GET /ai/usage).
type Summary struct {
	Month  string             `json:"month"` // "2006-01" UTC
	Total  Counter            `json:"total"`
	ByTask map[string]Counter `json:"by_task"`
}

// Store is the persistence surface the tracker needs. supplier.Service and the
// datamodel.SupplierStore both implement it.
type Store interface {
	GetSetting(ctx context.Context, key string) (string, error)
	PutSetting(ctx context.Context, key, value string) error
}

// monthLayout keys the blob; cleanup happens naturally because each month
// reads/writes its own settings key.
const monthLayout = "2006-01"

// trackerBlob is one month's persisted counters.
type trackerBlob struct {
	Month  string             `json:"month"`
	Total  Counter            `json:"total"`
	ByTask map[string]Counter `json:"by_task"`
}

// Tracker implements aigateway.UsageSink, persisting into the settings store.
type Tracker struct {
	mu    sync.Mutex
	store Store
}

// New wires a tracker over a settings-capable store.
func New(store Store) *Tracker { return &Tracker{store: store} }

// Record implements aigateway.UsageSink. It is a no-op on any storage error.
func (t *Tracker) Record(ctx context.Context, task string, tokensIn, tokensOut int) {
	if tokensIn == 0 && tokensOut == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	month := time.Now().UTC().Format(monthLayout)
	key := "ai.usage." + month
	blob := t.load(ctx, key)
	if blob.Month != month {
		blob = trackerBlob{Month: month, ByTask: map[string]Counter{}}
	}
	blob.Total.Calls++
	blob.Total.TokensIn += tokensIn
	blob.Total.TokensOut += tokensOut
	if blob.ByTask == nil {
		blob.ByTask = map[string]Counter{}
	}
	c := blob.ByTask[task]
	c.Calls++
	c.TokensIn += tokensIn
	c.TokensOut += tokensOut
	blob.ByTask[task] = c

	_ = t.save(ctx, key, blob)
}

// Current returns the current UTC month's aggregate (empty counter when none).
func (t *Tracker) Current(ctx context.Context) Summary {
	month := time.Now().UTC().Format(monthLayout)
	blob := t.load(ctx, "ai.usage."+month)
	if blob.Month != month {
		blob = trackerBlob{Month: month, ByTask: map[string]Counter{}}
	}
	if blob.ByTask == nil {
		blob.ByTask = map[string]Counter{}
	}
	return Summary{Month: blob.Month, Total: blob.Total, ByTask: blob.ByTask}
}

func (t *Tracker) load(ctx context.Context, key string) trackerBlob {
	raw, err := t.store.GetSetting(ctx, key)
	if err != nil || raw == "" {
		return trackerBlob{ByTask: map[string]Counter{}}
	}
	var b trackerBlob
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return trackerBlob{ByTask: map[string]Counter{}}
	}
	if b.ByTask == nil {
		b.ByTask = map[string]Counter{}
	}
	return b
}

func (t *Tracker) save(ctx context.Context, key string, b trackerBlob) error {
	if b.ByTask == nil {
		b.ByTask = map[string]Counter{}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return t.store.PutSetting(ctx, key, string(raw))
}
