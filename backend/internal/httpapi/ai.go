package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/usage"
)

// AI config endpoints (TR-19-A). The AI entry points across the UI are gated
// on whether a provider is configured — never on a network round-trip — so a
// configured-but-offline key still shows the features (the user can run
// GET /ai/test to see why calls fail). No key, no AI: everything is hidden.

// aiConfigResponse is the GET /ai/config payload.
type aiConfigResponse struct {
	Configured bool                 `json:"configured"`
	Config     aigateway.Config     `json:"config"` // redacted (key masked)
	Presets    []aigateway.Provider `json:"presets"`
}

// aiConfigRequest is the PUT /ai/config body. APIKey empty = clear the
// provider (disable AI).
type aiConfigRequest struct {
	Format         aigateway.ProviderFormat `json:"format"`
	BaseURL        string                   `json:"base_url"`
	APIKey         string                   `json:"api_key"`
	Model          string                   `json:"model"`
	EmbeddingModel string                   `json:"embedding_model"`
	MaxTokens      int                      `json:"max_tokens"`
}

func (r aiConfigRequest) config() aigateway.Config {
	return aigateway.Config{
		Format:         r.Format,
		BaseURL:        r.BaseURL,
		APIKey:         r.APIKey,
		Model:          r.Model,
		EmbeddingModel: r.EmbeddingModel,
		MaxTokens:      r.MaxTokens,
	}
}

// handleGetAIConfig returns the current (redacted) provider config and the
// presets for one-click fill.
func (s *Server) handleGetAIConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentAIConfig()
	writeJSON(w, http.StatusOK, aiConfigResponse{
		Configured: cfg.Valid(),
		Config:     cfg.Redacted(),
		Presets:    aigateway.Presets,
	})
}

// handleSaveAIConfig validates, persists and hot-swaps the AI provider. An
// empty api_key clears the provider and disables AI.
func (s *Server) handleSaveAIConfig(w http.ResponseWriter, r *http.Request) {
	var req aiConfigRequest
	if r.Body != nil {
		if !decodeOptionalJSONBody(w, r, 1<<16, &req) {
			return
		}
	}
	cfg := req.config()

	if cfg.APIKey != "" && (cfg.BaseURL == "" || cfg.Model == "") {
		writeError(w, http.StatusBadRequest, "AI 配置需同时提供 base_url、model 与 api_key（清除配置时留空 api_key）")
		return
	}

	// Persist first; only swap the live gateway after the write succeeds so
	// a failed save never leaves the in-memory gateway out of sync.
	enc, err := aigateway.EncodeConfig(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode ai config: "+err.Error())
		return
	}
	if err := s.Service.PutSetting(r.Context(), aigateway.SettingKey, enc); err != nil {
		writeServiceError(w, err)
		return
	}
	gw := aigateway.New(cfg, s.aiClient)
	gw.SetUsageSink(usage.New(s.Service))
	gw.SetResponseCache(aigateway.NewResponseCache(0, 0))
	s.setGateway(gw)

	writeJSON(w, http.StatusOK, aiConfigResponse{
		Configured: cfg.Valid(),
		Config:     cfg.Redacted(),
		Presets:    aigateway.Presets,
	})
}

// aiTestResponse reports one connectivity probe result.
type aiTestResponse struct {
	OK        bool   `json:"ok"`
	Model     string `json:"model,omitempty"`
	Error     string `json:"error,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

// handleTestAIConfig runs a one-turn "ping" against the configured provider.
// It is the explicit connectivity check the user triggers from settings.
func (s *Server) handleTestAIConfig(w http.ResponseWriter, r *http.Request) {
	s.aiMu.RLock()
	gw := s.gateway
	s.aiMu.RUnlock()

	if gw == nil || !gw.Enabled() {
		writeJSON(w, http.StatusOK, aiTestResponse{OK: false, Error: "未配置 AI 模型"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := gw.Complete(ctx, aigateway.ChatRequest{
		Task:     "ping",
		Messages: []aigateway.ChatMessage{{Role: "user", Content: "回复 ok"}},
	})
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeJSON(w, http.StatusOK, aiTestResponse{OK: false, Error: "未配置 AI 模型"})
			return
		}
		writeJSON(w, http.StatusOK, aiTestResponse{OK: false, Error: err.Error(), LatencyMS: time.Since(start).Milliseconds()})
		return
	}
	writeJSON(w, http.StatusOK, aiTestResponse{
		OK:        true,
		Model:     resp.Model,
		LatencyMS: time.Since(start).Milliseconds(),
	})
}
