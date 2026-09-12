package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// TR-19-A: AI config read/save/test and the feature-flag derivation from a
// configured provider. The default test server has no gateway → AI disabled.

func TestAIConfigDefaultsToUnconfigured(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodGet, "/api/v1/ai/config", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp aiConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Configured {
		t.Fatal("default server must report unconfigured")
	}
	if len(resp.Presets) == 0 {
		t.Fatal("presets missing")
	}

	// /features reports ai_enabled=false without a gateway.
	w = do(t, s, http.MethodGet, "/api/v1/features", "")
	var feats struct {
		AIEnabled bool `json:"ai_enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feats); err != nil {
		t.Fatal(err)
	}
	if feats.AIEnabled {
		t.Fatal("ai_enabled must be false with no gateway")
	}
}

func TestAIConfigSavePersistsAndLightsFeatures(t *testing.T) {
	s := newTestServer(t)

	body := `{"format":"openai","base_url":"http://127.0.0.1:9","api_key":"sk-test","model":"deepseek-chat"}`
	w := do(t, s, http.MethodPut, "/api/v1/ai/config", body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status %d body=%s", w.Code, w.Body.String())
	}

	// The redacted config must not leak the key.
	var resp aiConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Configured {
		t.Fatal("expected configured after save")
	}
	if strings.Contains(resp.Config.APIKey, "sk-test") {
		t.Fatalf("redacted config leaked key: %q", resp.Config.APIKey)
	}

	// GET reflects the saved config (redacted).
	w = do(t, s, http.MethodGet, "/api/v1/ai/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Config.Model != "deepseek-chat" {
		t.Errorf("model = %q", resp.Config.Model)
	}

	// /features now reports ai_enabled=true (derived from the live gateway).
	w = do(t, s, http.MethodGet, "/api/v1/features", "")
	var feats struct {
		AIEnabled bool `json:"ai_enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feats); err != nil {
		t.Fatal(err)
	}
	if !feats.AIEnabled {
		t.Fatal("ai_enabled must be true after saving a valid config")
	}
}

func TestAIConfigRejectsIncomplete(t *testing.T) {
	s := newTestServer(t)
	// api_key set but base_url/model missing → 400.
	w := do(t, s, http.MethodPut, "/api/v1/ai/config", `{"api_key":"sk-test"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}

func TestAIConfigClearDisables(t *testing.T) {
	s := newTestServer(t)
	// Configure, then clear via empty api_key.
	do(t, s, http.MethodPut, "/api/v1/ai/config", `{"base_url":"http://x","api_key":"k","model":"m"}`)
	do(t, s, http.MethodPut, "/api/v1/ai/config", `{"api_key":""}`)

	w := do(t, s, http.MethodGet, "/api/v1/features", "")
	var feats struct {
		AIEnabled bool `json:"ai_enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feats); err != nil {
		t.Fatal(err)
	}
	if feats.AIEnabled {
		t.Fatal("clearing api_key must disable AI")
	}
}

func TestAITestUnconfiguredReportsNotConfigured(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodGet, "/api/v1/ai/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp aiTestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || !strings.Contains(resp.Error, "未配置") {
		t.Fatalf("unconfigured test = %+v", resp)
	}
}

// TestAITestReachesProvider pins the end-to-end probe against a fake OpenAI
// endpoint (connectivity + response mapping through the wired gateway).
func TestAITestReachesProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"deepseek-chat","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	s := newTestServer(t)
	cfg, _ := aigateway.EncodeConfig(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	})
	// Seed the gateway directly (bypassing persistence) so the test is
	// isolated from the store's JSON settings path already covered above.
	s.WithGateway(aigateway.New(mustDecodeAIConfig(t, cfg), nil))

	w := do(t, s, http.MethodGet, "/api/v1/ai/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp aiTestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Model != "deepseek-chat" {
		t.Fatalf("test response = %+v", resp)
	}
}

func mustDecodeAIConfig(t *testing.T, enc string) aigateway.Config {
	t.Helper()
	c, err := aigateway.DecodeConfig(enc)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAIConfigPersistsEmbeddingModel(t *testing.T) {
	s := newTestServer(t)
	body := `{"format":"openai","base_url":"http://127.0.0.1:9","api_key":"k","model":"deepseek-chat","embedding_model":"text-embedding-3-small"}`
	if w := do(t, s, http.MethodPut, "/api/v1/ai/config", body); w.Code != http.StatusOK {
		t.Fatalf("PUT status %d body=%s", w.Code, w.Body.String())
	}
	w := do(t, s, http.MethodGet, "/api/v1/ai/config", "")
	var resp aiConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Config.EmbeddingModel != "text-embedding-3-small" {
		t.Errorf("embedding_model = %q, want persisted", resp.Config.EmbeddingModel)
	}
}
