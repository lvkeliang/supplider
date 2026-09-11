package aigateway

import (
	"encoding/json"
	"strings"
)

// ProviderFormat is the wire protocol an adapter speaks. Both map to the same
// Gateway port; the concrete adapters translate the shared ChatRequest into
// the provider's native body and back.
type ProviderFormat string

const (
	FormatOpenAI    ProviderFormat = "openai"
	FormatAnthropic ProviderFormat = "anthropic"
)

// Config is the persisted, user-supplied AI provider selection. It is the
// single source of truth for Enabled(): no key, no AI — the whole feature
// surface stays hidden (AI 原生但可降级).
type Config struct {
	// Format selects the adapter. "" defaults to OpenAI-compatible.
	Format ProviderFormat `json:"format"`
	// BaseURL is the API endpoint root; presets fill it in.
	BaseURL string `json:"base_url"`
	// APIKey authenticates the request (never returned in full by GET).
	APIKey string `json:"api_key,omitempty"`
	// Model is the concrete model id (e.g. deepseek-chat, claude-sonnet-5).
	Model string `json:"model"`
	// MaxTokens caps output; 0 means the adapter default.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// Redacted is the config safe to echo to the UI: the key is masked.
func (c Config) Redacted() Config {
	if c.APIKey == "" {
		return c
	}
	k := c.APIKey
	keep := 4
	if len(k) <= keep*2 {
		keep = len(k) / 2
	}
	c.APIKey = k[:keep] + strings.Repeat("•", len(k)-keep)
	return c
}

// Provider is a one-click preset (设置页 快捷填充).
type Provider struct {
	Name    string         `json:"name"`
	Format  ProviderFormat `json:"format"`
	BaseURL string         `json:"base_url"`
	Model   string         `json:"model"`
}

// Presets covers the OpenAI-format family (DeepSeek / 通义 / GLM / Moonshot /
// Ollama) and the Anthropic-format family (Claude / DeepSeek's Anthropic
// endpoint), so users don't hand-enter base URLs.
var Presets = []Provider{
	{Name: "DeepSeek", Format: FormatOpenAI, BaseURL: "https://api.deepseek.com", Model: "deepseek-chat"},
	{Name: "通义千问 (Qwen)", Format: FormatOpenAI, BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Model: "qwen-plus"},
	{Name: "智谱 GLM", Format: FormatOpenAI, BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "glm-4-flash"},
	{Name: "Moonshot (Kimi)", Format: FormatOpenAI, BaseURL: "https://api.moonshot.cn/v1", Model: "moonshot-v1-8k"},
	{Name: "Ollama (本地)", Format: FormatOpenAI, BaseURL: "http://localhost:11434/v1", Model: "llama3.1"},
	{Name: "Claude (Anthropic)", Format: FormatAnthropic, BaseURL: "https://api.anthropic.com", Model: "claude-haiku-4-5-20251001"},
	{Name: "DeepSeek (Anthropic 端点)", Format: FormatAnthropic, BaseURL: "https://api.deepseek.com/anthropic", Model: "deepseek-chat"},
}

// SettingKey is the storage key under which the JSON config is persisted,
// travelling with the library backup/migration (SettingsStore).
const SettingKey = "ai.config"

// Valid reports whether the config has enough to attempt a call.
func (c Config) Valid() bool {
	return c.APIKey != "" && c.Model != "" && c.BaseURL != ""
}

// DecodeConfig unmarshals a persisted config blob. Empty/absent blob yields
// an empty Config — the normal "AI not configured" state. The wiring layer
// maps the store's ErrNotFound to "" before calling this.
func DecodeConfig(raw string) (Config, error) {
	if raw == "" {
		return Config{}, nil
	}
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// EncodeConfig serializes the config for persistence.
func EncodeConfig(c Config) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
