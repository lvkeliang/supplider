// Package aigateway defines the AI provider port (highest-level design
// principle: AI-native but degradable — AI 原生但可降级).
//
// Every AI feature MUST have a non-AI fallback. When no API key / local
// model is configured, Enabled() returns false and the UI hides ALL AI
// entry points; core flows remain 100% functional.
//
// Adapters: cloud OpenAI-compatible APIs (DeepSeek / Qwen / GLM),
// local Ollama, hybrid routing — all speak the OpenAI wire format.
package aigateway

import (
	"context"
	"errors"
)

// ErrAIDisabled is returned when the gateway has no configured provider.
// Callers treat it as "silently fall back to the non-AI path".
var ErrAIDisabled = errors.New("aigateway: no AI provider configured (feature hidden)")

// ChatMessage is one turn in a completion request.
type ChatMessage struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// ChatRequest is a routed completion request. Task hints model routing
// (e.g. "extract", "summary", "nl2filter").
type ChatRequest struct {
	Task     string
	Messages []ChatMessage
	JSONMode bool
}

// ChatResponse is the completion result.
type ChatResponse struct {
	Text      string
	Model     string
	TokensIn  int
	TokensOut int
}

// EmbeddingItem pairs an id with its vector.
type EmbeddingItem struct {
	ID     string
	Vector []float32
}

// Gateway is the AI port. Implementations must be safe for concurrent use.
type Gateway interface {
	// Enabled reports whether any provider is usable. When false, the
	// backend skips AI features and the frontend hides AI entry points.
	Enabled() bool
	// Complete runs a chat completion.
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	// Embed computes vectors for bge-m3-style semantic search.
	Embed(ctx context.Context, texts []string) ([]EmbeddingItem, error)
}
