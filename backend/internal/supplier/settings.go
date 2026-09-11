package supplier

import (
	"context"
)

// GetSetting reads one raw admin/config value by key from the underlying
// store. It is the generic escape hatch the wiring layer uses for settings
// that do not belong to supplier lifecycle logic (e.g. the AI provider
// config) but must travel with the same library backup/migration path.
func (s *Service) GetSetting(ctx context.Context, key string) (string, error) {
	return s.store.GetSetting(ctx, key)
}

// PutSetting upserts one raw admin/config value by key.
func (s *Service) PutSetting(ctx context.Context, key, value string) error {
	return s.store.PutSetting(ctx, key, value)
}
