package aigateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAIAdapter speaks the OpenAI Chat Completions wire format, which is what
// DeepSeek / Qwen(通义) / GLM / Moonshot and local Ollama all implement. It is
// deliberately self-contained (net/http only, no SDK) so the personal build
// stays zero-dependency.
type OpenAIAdapter struct {
	baseURL        string
	apiKey         string
	model          string
	embeddingModel string
	maxTokens      int
	client         *http.Client
}

// NewOpenAIAdapter builds an adapter against a base URL like
// https://api.deepseek.com or https://api.deepseek.com/v1.
func NewOpenAIAdapter(cfg Config, client *http.Client) *OpenAIAdapter {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &OpenAIAdapter{
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		embeddingModel: cfg.EmbeddingModel,
		maxTokens:      cfg.MaxTokens,
		client:         client,
	}
}

// openaiChatMessage is one message in the OpenAI body. system goes into the
// messages array as the first turn (unlike Anthropic's top-level field).
// Content is either a string (text-only) or []openaiContentPart (vision).
type openaiChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openaiContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openaiImageURL `json:"image_url,omitempty"`
}

type openaiImageURL struct {
	URL string `json:"url"`
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
		om := openaiChatMessage{Role: m.Role, Content: m.Content}
		if m.ImageBase64 != "" {
			om.Content = []openaiContentPart{
				{Type: "text", Text: m.Content},
				{Type: "image_url", ImageURL: &openaiImageURL{URL: imageDataURI(m.ImageMIME, m.ImageBase64)}},
			}
		}
		msgs = append(msgs, om)
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
// Embed computes vectors via the OpenAI /embeddings endpoint. It requires a
// configured EmbeddingModel; without one it returns ErrAIDisabled so semantic
// search stays hidden (AI 原生但可降级).
func (a *OpenAIAdapter) Embed(ctx context.Context, texts []string) ([]EmbeddingItem, error) {
	if a.embeddingModel == "" {
		return nil, ErrAIDisabled
	}
	if len(texts) == 0 {
		return nil, nil
	}

	payload, err := json.Marshal(map[string]any{
		"model": a.embeddingModel,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, providerError("openai", resp.StatusCode, raw)
	}

	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("aigateway: decode openai embeddings: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("aigateway: openai api error: %s", parsed.Error.Message)
	}

	out := make([]EmbeddingItem, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		out = append(out, EmbeddingItem{ID: strconv.Itoa(d.Index), Vector: d.Embedding})
	}
	return out, nil
}
