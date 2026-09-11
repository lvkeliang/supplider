package aigateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests pin the five OpenAI↔Anthropic wire-format differences that the
// gateway abstracts away, so callers (OCR, NL search, …) stay provider-blind.

func TestOpenAIAdapterComplete(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		// Auth header is Bearer (diff #4).
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		got = req
		// System is a message in the array (diff #1).
		msgs := req["messages"].([]any)
		first := msgs[0].(map[string]any)
		if first["role"] != "system" {
			t.Errorf("first message role = %v, want system", first["role"])
		}
		// JSON mode is requested via response_format.
		if rf, ok := req["response_format"].(map[string]any); !ok || rf["type"] != "json_object" {
			t.Errorf("response_format = %v", req["response_format"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"deepseek-chat","choices":[{"message":{"content":"  {\"ok\":true} "}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	a := NewOpenAIAdapter(Config{BaseURL: srv.URL, APIKey: "test-key", Model: "deepseek-chat"}, nil)
	resp, err := a.Complete(context.Background(), ChatRequest{
		JSONMode: true,
		Messages: []ChatMessage{{Role: "system", Content: "You are a parser."}, {Role: "user", Content: "extract"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != `{"ok":true}` { // trimmed
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.Model != "deepseek-chat" || resp.TokensIn != 5 || resp.TokensOut != 3 {
		t.Errorf("unexpected response: %+v", resp)
	}
	_ = got
}

func TestAnthropicAdapterComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		// Auth is x-api-key + anthropic-version (diff #4).
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Error("anthropic-version missing")
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		// System is top-level (diff #1).
		if req["system"] != "You are a parser." {
			t.Errorf("system = %v", req["system"])
		}
		// max_tokens is always present (diff #2).
		if _, ok := req["max_tokens"]; !ok {
			t.Error("max_tokens missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"claude-x","content":[{"type":"text","text":"hello"},{"type":"text","text":" world"}],"usage":{"input_tokens":7,"output_tokens":4}}`))
	}))
	defer srv.Close()

	a := NewAnthropicAdapter(Config{BaseURL: srv.URL, APIKey: "test-key", Model: "claude-x"}, nil)
	resp, err := a.Complete(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "system", Content: "You are a parser."}, {Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Text concatenates content blocks (diff #3).
	if resp.Text != "hello world" {
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.TokensIn != 7 || resp.TokensOut != 4 {
		t.Errorf("unexpected usage: %+v", resp)
	}
}

func TestAnthropicMaxTokensDefaults(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(strings.Builder)
		_, _ = io.Copy(b, r.Body)
		body = b.String()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"content":[{"type":"text","text":"x"}]}`))
	}))
	defer srv.Close()

	// max_tokens omitted in config → adapter still sends a required value.
	a := NewAnthropicAdapter(Config{BaseURL: srv.URL, APIKey: "k", Model: "m"}, nil)
	if _, err := a.Complete(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.Contains(body, `"max_tokens":4096`) {
		t.Errorf("max_tokens default missing from body: %s", body)
	}
}

func TestDisabledService(t *testing.T) {
	s := New(Config{}, nil)
	if s.Enabled() {
		t.Fatal("empty config must be disabled")
	}
	if _, err := s.Complete(context.Background(), ChatRequest{}); err != ErrAIDisabled {
		t.Errorf("Complete err = %v, want ErrAIDisabled", err)
	}
	if _, err := s.Embed(context.Background(), []string{"x"}); err != ErrAIDisabled {
		t.Errorf("Embed err = %v, want ErrAIDisabled", err)
	}
}

func TestConfigRoundTripAndRedaction(t *testing.T) {
	c := Config{Format: FormatAnthropic, BaseURL: "https://api.anthropic.com", APIKey: "sk-ant-secret-value", Model: "claude-x", MaxTokens: 2000}
	enc, err := EncodeConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeConfig(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != c {
		t.Errorf("round trip mismatch: %+v != %+v", dec, c)
	}

	// Redacted keeps only a prefix and masks the rest; never echoes the key.
	r := c.Redacted()
	if r.APIKey == c.APIKey || strings.Contains(r.APIKey, "secret-value") {
		t.Errorf("Redacted leaked the key: %q", r.APIKey)
	}
	if !strings.HasPrefix(r.APIKey, "sk-a") {
		t.Errorf("Redacted lost the prefix: %q", r.APIKey)
	}

	// Absent config decodes to empty without error.
	if d, err := DecodeConfig(""); err != nil || d != (Config{}) {
		t.Errorf("DecodeConfig(\"\") = %+v, %v", d, err)
	}
}
