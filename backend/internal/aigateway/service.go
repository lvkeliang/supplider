package aigateway

import (
	"context"
	"net/http"
	"sync"
)

// Service is the concrete Gateway: it holds the current provider config and
// lazily builds the right adapter (OpenAI or Anthropic format). Enabled()
// reports whether a usable key+model+base is configured — that single bit
// drives featureflag.WithAIState and hides every AI entry when false.
//
// Config changes are expected to swap the Service wholesale (a fresh
// *Service from New) so no locking is needed around the adapter swap.
type Service struct {
	mu      sync.RWMutex
	config  Config
	adapter adapter
	// usage reports token counts after each successful completion. Assigned
	// once at construction via SetUsageSink; read under mu so a hot swap (a
	// fresh Service) never races a Config() read on the old one.
	usage UsageSink
	// cache short-circuits identical completions to save tokens. Optional;
	// wired for production via SetResponseCache (tests keep it nil).
	cache *ResponseCache
}

// adapter is the internal completion surface both wire formats implement.
type adapter interface {
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Embed(ctx context.Context, texts []string) ([]EmbeddingItem, error)
}

// New builds a Service from a config. A nil/invalid config yields a disabled
// Service whose Complete/Embed return ErrAIDisabled.
func New(cfg Config, client *http.Client) *Service {
	s := &Service{config: cfg}
	if cfg.Valid() {
		switch cfg.Format {
		case FormatAnthropic:
			s.adapter = NewAnthropicAdapter(cfg, client)
		default:
			s.adapter = NewOpenAIAdapter(cfg, client)
		}
	}
	return s
}

// Enabled reports whether a provider is configured and usable.
func (s *Service) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.adapter != nil
}

// CanEmbed reports whether the configured provider can compute vectors for
// semantic search (TR-19-E). It is stricter than Enabled(): an embedding
// model must be set AND the provider must expose an /embeddings endpoint
// (OpenAI-format only — the Anthropic Messages API has no embeddings route).
// A configured-but-offline provider still reports true; the call itself
// surfaces the connectivity error.
func (s *Service) CanEmbed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.adapter == nil {
		return false
	}
	if s.config.EmbeddingModel == "" {
		return false
	}
	return s.config.Format != FormatAnthropic
}

// SetUsageSink attaches a token-usage recorder (nil disables). Call once
// before the service is used — recording is best-effort and swallows errors.
func (s *Service) SetUsageSink(sink UsageSink) {
	s.mu.Lock()
	s.usage = sink
	s.mu.Unlock()
}

func (s *Service) reportUsage(ctx context.Context, task string, in, out int) {
	if in == 0 && out == 0 {
		return
	}
	s.mu.RLock()
	u := s.usage
	s.mu.RUnlock()
	if u != nil {
		u.Record(ctx, task, in, out)
	}
}

// SetResponseCache attaches a bounded response cache (nil disables). Identical
// completions then short-circuit the provider instead of re-billing tokens.
func (s *Service) SetResponseCache(c *ResponseCache) {
	s.mu.Lock()
	s.cache = c
	s.mu.Unlock()
}

// Config returns the redacted config (key masked) for display.
func (s *Service) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Redacted()
}

func (s *Service) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	s.mu.RLock()
	a := s.adapter
	cache := s.cache
	s.mu.RUnlock()
	if a == nil {
		return ChatResponse{}, ErrAIDisabled
	}

	// Vision (image) calls are never cached — image bytes are large and not
	// fingerprinted, so a hashed key could collide across different images.
	hasImage := false
	for _, m := range req.Messages {
		if m.ImageBase64 != "" {
			hasImage = true
			break
		}
	}

	if cache != nil && !hasImage {
		// Config is immutable per Service (a swap builds a fresh Service), so
		// reading it here without the lock is safe.
		key := fingerprint(req.Task, s.config.Model, s.config.BaseURL, req.JSONMode, req.Messages)
		if resp, ok := cache.Get(key); ok {
			return resp, nil // cache hit — no provider call, no token charge
		}
		resp, err := a.Complete(ctx, req)
		if err == nil {
			cache.Put(key, resp)
			s.reportUsage(ctx, req.Task, resp.TokensIn, resp.TokensOut)
		}
		return resp, err
	}

	resp, err := a.Complete(ctx, req)
	if err == nil {
		s.reportUsage(ctx, req.Task, resp.TokensIn, resp.TokensOut)
	}
	return resp, err
}

func (s *Service) Embed(ctx context.Context, texts []string) ([]EmbeddingItem, error) {
	s.mu.RLock()
	a := s.adapter
	s.mu.RUnlock()
	if a == nil {
		return nil, ErrAIDisabled
	}
	return a.Embed(ctx, texts)
}
