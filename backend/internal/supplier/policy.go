// Persisted visibility policy (可见性策略配置). The admin-set maximum
// visibility level and buffer window live in the store's settings table so
// they (a) survive restarts and travel with the database backup, (b) are
// shared by every process that opens the same library — the Tauri sidecar,
// the MCP stdio binary and (on higher tiers) multiple API instances — and
// (c) drive the sidecar's automatic daily disposition sweep without anyone
// having to pass --max-level on every call.
//
// Business code still never reads tier config: the wiring layer passes a
// FALLBACK policy (derived from the feature matrix) that applies until an
// admin saves an explicit one.
package supplier

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/supplider/supplider/backend/internal/datamodel"
)

// SettingVisibilityPolicy is the settings key under which the admin
// visibility policy is persisted.
const SettingVisibilityPolicy = "visibility_policy"

// LoadVisibilityPolicy returns the admin-configured visibility policy.
// `configured` reports whether an explicit policy was found in the store;
// when false the normalized fallback (wiring-layer default from the tier
// feature matrix) is returned. A corrupt stored value is treated as
// "not configured" rather than wedging enforcement.
func (s *Service) LoadVisibilityPolicy(ctx context.Context, fallback VisibilityPolicy) (policy VisibilityPolicy, configured bool, err error) {
	raw, err := s.store.GetSetting(ctx, SettingVisibilityPolicy)
	if errors.Is(err, datamodel.ErrNotFound) {
		return fallback.normalized(), false, nil
	}
	if err != nil {
		return fallback.normalized(), false, err
	}
	var stored VisibilityPolicy
	if jsonErr := json.Unmarshal([]byte(raw), &stored); jsonErr != nil {
		return fallback.normalized(), false, nil
	}
	return stored.normalized(), true, nil
}

// SaveVisibilityPolicy validates, normalizes and persists the admin
// visibility policy, returning the stored value.
func (s *Service) SaveVisibilityPolicy(ctx context.Context, p VisibilityPolicy) (VisibilityPolicy, error) {
	p = p.normalized()
	raw, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	if err := s.store.PutSetting(ctx, SettingVisibilityPolicy, string(raw)); err != nil {
		return p, err
	}
	return p, nil
}
