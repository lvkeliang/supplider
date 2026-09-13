// Package audit records a cross-supplier lifecycle trail (create / update /
// archive / blacklist / merge / export / visibility), persisted as a capped
// list in the supplier settings store so it survives a restart and travels
// with the library backup. Field-level history is already durable per
// supplier (change_log); this is the global "who did what when" timeline.
//
// Recording is best-effort — a storage failure never surfaces to the caller.
// v1: single actor ("local") for the personal tier; the multi-actor field is
// reserved for 小企业版 用户/部门登陆之后。
package audit

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Entry is one lifecycle action.
type Entry struct {
	Time     time.Time `json:"time"`
	Action   string    `json:"action"` // create|update|archive|restore|blacklist|unblacklist|merge|import|export|visibility
	TargetID string    `json:"target_id,omitempty"`
	Name     string    `json:"name,omitempty"`
	Actor    string    `json:"actor"` // "local" (personal single user)
}

// Store is the persistence surface (supplier.Service implements it).
type Store interface {
	GetSetting(ctx context.Context, key string) (string, error)
	PutSetting(ctx context.Context, key, value string) error
}

// settingKey is the settings key holding the capped trail JSON.
const settingKey = "audit.trail"

// defaultCap bounds the retained trail.
const defaultCap = 200

// Recorder keeps a capped, persisted trail.
type Recorder struct {
	store  Store
	mu     sync.Mutex
	items  []Entry // newest first
	cap    int
	loaded bool // lazy-load guard
}

// New wires a recorder over a settings-capable store.
func New(store Store) *Recorder { return &Recorder{store: store, cap: defaultCap} }

// Record appends a lifecycle action (best-effort persist).
func (r *Recorder) Record(ctx context.Context, action, targetID, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureLoadedLocked(ctx)

	e := Entry{Time: time.Now().UTC(), Action: action, TargetID: targetID, Name: name, Actor: "local"}
	r.items = append([]Entry{e}, r.items...)
	if len(r.items) > r.cap {
		r.items = r.items[:r.cap]
	}
	_ = r.saveLocked(ctx)
}

// List returns up to limit trail entries, newest first.
func (r *Recorder) List(ctx context.Context, limit int) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureLoadedLocked(ctx)
	if limit <= 0 || limit > len(r.items) {
		limit = len(r.items)
	}
	out := make([]Entry, limit)
	copy(out, r.items[:limit])
	return out
}

func (r *Recorder) ensureLoadedLocked(ctx context.Context) {
	if r.store == nil || r.loaded {
		return
	}
	r.loaded = true
	raw, err := r.store.GetSetting(ctx, settingKey)
	if err != nil || raw == "" {
		return
	}
	_ = json.Unmarshal([]byte(raw), &r.items)
}

func (r *Recorder) saveLocked(ctx context.Context) error {
	if r.store == nil {
		return nil
	}
	if len(r.items) > r.cap {
		r.items = r.items[:r.cap]
	}
	raw, err := json.Marshal(r.items)
	if err != nil {
		return err
	}
	return r.store.PutSetting(ctx, settingKey, string(raw))
}
