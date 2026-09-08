package supplier

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/supplider/supplider/backend/internal/datamodel"
)

// SettingLocalPreference stores the user's home region for local-first
// search ranking (本地供应商偏好), e.g. {"province":"浙江","city":"杭州"}.
const SettingLocalPreference = "local_preference"

// LocalPreference is the user's preferred region. A supplier in this
// region ranks first in list/search results (ranked, not filtered —
// suppliers elsewhere still appear afterward).
type LocalPreference struct {
	Province string `json:"province"`
	City     string `json:"city,omitempty"`
}

// Empty reports whether the preference has any effect (a preference
// without a province ranks nothing).
func (p LocalPreference) Empty() bool { return strings.TrimSpace(p.Province) == "" }

// SaveLocalPreference persists the preferred region (trimmed). Province is
// required; callers removing the preference must use ClearLocalPreference.
func (s *Service) SaveLocalPreference(ctx context.Context, p LocalPreference) (LocalPreference, error) {
	p.Province = strings.TrimSpace(p.Province)
	p.City = strings.TrimSpace(p.City)
	if p.Province == "" {
		return LocalPreference{}, errors.New("supplier: local preference province is required (use clear to remove it)")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return LocalPreference{}, err
	}
	if err := s.store.PutSetting(ctx, SettingLocalPreference, string(raw)); err != nil {
		return LocalPreference{}, err
	}
	return p, nil
}

// ClearLocalPreference removes the home-region preference (local-first
// ranking off). It writes an empty value rather than deleting the key:
// settings have no delete operation, and Load treats an empty province the
// same as "never configured".
func (s *Service) ClearLocalPreference(ctx context.Context) error {
	return s.store.PutSetting(ctx, SettingLocalPreference, `{"province":""}`)
}

// LoadLocalPreference returns the saved preference; never-set / corrupt
// values collapse to an empty preference (no local ranking).
func (s *Service) LoadLocalPreference(ctx context.Context) (LocalPreference, error) {
	raw, err := s.store.GetSetting(ctx, SettingLocalPreference)
	if err != nil {
		if errors.Is(err, datamodel.ErrNotFound) {
			return LocalPreference{}, nil
		}
		return LocalPreference{}, err
	}
	var p LocalPreference
	if json.Unmarshal([]byte(raw), &p) != nil {
		return LocalPreference{}, nil
	}
	p.Province = strings.TrimSpace(p.Province)
	p.City = strings.TrimSpace(p.City)
	return p, nil
}

// withLocalPreference auto-fills the query's prefer-region from the
// persisted home-region preference. Ranking only — it never filters
// suppliers out. Explicit query hints win over the saved preference, and
// NoLocalPreference (the UI/CLI "本地优先" switch) disables the fill for
// one query. The setting is a single-row primary-key lookup per page.
func (s *Service) withLocalPreference(ctx context.Context, q datamodel.Query) datamodel.Query {
	if q.NoLocalPreference || q.Filter.PreferProvince != "" {
		return q
	}
	pref, err := s.LoadLocalPreference(ctx)
	if err != nil || pref.Empty() {
		return q
	}
	q.Filter.PreferProvince = pref.Province
	q.Filter.PreferCity = pref.City
	return q
}
