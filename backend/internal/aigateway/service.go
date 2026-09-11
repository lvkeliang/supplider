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

// Config returns the redacted config (key masked) for display.
func (s *Service) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Redacted()
}

func (s *Service) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	s.mu.RLock()
	a := s.adapter
	s.mu.RUnlock()
	if a == nil {
		return ChatResponse{}, ErrAIDisabled
	}
	return a.Complete(ctx, req)
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
