package aigateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnthropicAdapter speaks the Anthropic Messages API, which differs from the
// OpenAI format in five ways the callers must not have to think about:
//
//  1. System prompt is a top-level `system` field (not messages[0]).
//  2. `max_tokens` is REQUIRED (no omitempty fallback).
//  3. Response text lives in `content[]` blocks, not choices[].message.
//  4. Auth is `x-api-key` + `anthropic-version` (not Bearer).
//  5. Streaming SSE shape differs (this adapter does non-streaming; Complete
//     returns the full text).
type AnthropicAdapter struct {
	baseURL   string
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

// Anthropic requires max_tokens; 4096 is a sensible default for our bounded
// tasks when the user did not set one.
const anthropicDefaultMaxTokens = 4096

// NewAnthropicAdapter builds an adapter against a base URL like
// https://api.anthropic.com or DeepSeek's Anthropic-compatible endpoint.
func NewAnthropicAdapter(cfg Config, client *http.Client) *AnthropicAdapter {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = anthropicDefaultMaxTokens
	}
	return &AnthropicAdapter{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		maxTokens: maxTokens,
		client:    client,
	}
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicRequest struct {
	Model     string                 `json:"model"`
	MaxTokens int                    `json:"max_tokens"`
	System    string                 `json:"system,omitempty"`
	Messages  []anthropicContentUser `json:"messages"`
}

type anthropicContentUser struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Model   string                  `json:"model"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete runs one completion. The system message is hoisted out of the
// turn list into the top-level field (Anthropic's shape).
func (a *AnthropicAdapter) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	var system string
	var turns []anthropicContentUser
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = strings.TrimSpace(system + "\n" + m.Content)
			continue
		}
		turns = append(turns, anthropicContentUser{
			Role: m.Role,
			Content: []anthropicContentBlock{{
				Type: "text",
				Text: m.Content,
			}},
		})
	}

	body := anthropicRequest{
		Model:     a.model,
		MaxTokens: a.maxTokens,
		System:    strings.TrimSpace(system),
		Messages:  turns,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ChatResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ChatResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return ChatResponse{}, providerError("anthropic", resp.StatusCode, raw)
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ChatResponse{}, fmt.Errorf("aigateway: decode anthropic response: %w", err)
	}
	if parsed.Error != nil {
		return ChatResponse{}, fmt.Errorf("aigateway: anthropic api error: %s", parsed.Error.Message)
	}

	var text string
	for _, block := range parsed.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	return ChatResponse{
		Text:      strings.TrimSpace(text),
		Model:     parsed.Model,
		TokensIn:  parsed.Usage.InputTokens,
		TokensOut: parsed.Usage.OutputTokens,
	}, nil
}

// Embed returns ErrAIDisabled (see OpenAIAdapter.Embed).
func (a *AnthropicAdapter) Embed(ctx context.Context, texts []string) ([]EmbeddingItem, error) {
	return nil, ErrAIDisabled
}
