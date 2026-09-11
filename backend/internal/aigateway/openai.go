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

// OpenAIAdapter speaks the OpenAI Chat Completions wire format, which is what
// DeepSeek / Qwen(通义) / GLM / Moonshot and local Ollama all implement. It is
// deliberately self-contained (net/http only, no SDK) so the personal build
// stays zero-dependency.
type OpenAIAdapter struct {
	baseURL   string
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

// NewOpenAIAdapter builds an adapter against a base URL like
// https://api.deepseek.com or https://api.deepseek.com/v1.
func NewOpenAIAdapter(cfg Config, client *http.Client) *OpenAIAdapter {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &OpenAIAdapter{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		client:    client,
	}
}

// openaiChatMessage is one message in the OpenAI body. system goes into the
// messages array as the first turn (unlike Anthropic's top-level field).
type openaiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiChatRequest struct {
	Model          string              `json:"model"`
	Messages       []openaiChatMessage `json:"messages"`
	MaxTokens      int                 `json:"max_tokens,omitempty"`
	Temperature    float64             `json:"temperature,omitempty"`
	ResponseFormat *json.RawMessage    `json:"response_format,omitempty"`
}

type openaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete runs one completion. JSONMode adds response_format json_object —
// callers then json.Unmarshal the returned Text.
func (a *OpenAIAdapter) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	msgs := make([]openaiChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, openaiChatMessage{Role: m.Role, Content: m.Content})
	}
	body := openaiChatRequest{
		Model:       a.model,
		Messages:    msgs,
		MaxTokens:   a.maxTokens,
		Temperature: 0,
	}
	if req.JSONMode {
		rf := json.RawMessage(`{"type":"json_object"}`)
		body.ResponseFormat = &rf
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return ChatResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

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
		return ChatResponse{}, providerError("openai", resp.StatusCode, raw)
	}

	var parsed openaiChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ChatResponse{}, fmt.Errorf("aigateway: decode openai response: %w", err)
	}
	if parsed.Error != nil {
		return ChatResponse{}, fmt.Errorf("aigateway: openai api error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("aigateway: openai returned no choices")
	}

	return ChatResponse{
		Text:      strings.TrimSpace(parsed.Choices[0].Message.Content),
		Model:     parsed.Model,
		TokensIn:  parsed.Usage.PromptTokens,
		TokensOut: parsed.Usage.CompletionTokens,
	}, nil
}

// Embed returns ErrAIDisabled: vector embedding (bge-m3) is a later stage
// (TR-19-E); the port exists so the non-AI path is chosen until then.
func (a *OpenAIAdapter) Embed(ctx context.Context, texts []string) ([]EmbeddingItem, error) {
	return nil, ErrAIDisabled
}
