package mcp_test

// UI-search-style: nl_search_suppliers tool drives a natural-language query
// through the shared nlsearch.Parse (against a fake LLM upstream seeded in the
// persisted ai.config) and returns matching suppliers.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/mcp"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestNLSearchSuppliersTool(t *testing.T) {
	// Fake OpenAI chat-completions upstream that returns a structured filter.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"province\":\"浙江\",\"city\":\"杭州\",\"category\":\"市政工程\",\"min_qual_level\":\"二级\"}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer upstream.Close()

	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	enc, _ := aigateway.EncodeConfig(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	})
	if err := svc.PutSetting(ctx, aigateway.SettingKey, enc); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, supplier.CreateInput{
		BasicInfo:      domain.BasicInfo{CompanyName: "杭州混凝土工程有限公司", Region: domain.Region{Province: "浙江", City: "杭州"}},
		Categories:     []string{"市政工程"},
		Qualifications: []domain.Qualification{{Type: "建筑工程施工总承包", Level: "二级"}},
	}); err != nil {
		t.Fatal(err)
	}

	srv := mcp.New(svc, io.Discard)
	line := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nl_search_suppliers","arguments":{"query":"杭州市政二级"}}}`
	in := strings.NewReader(line + "\n")
	var out strings.Builder
	if err := srv.Serve(ctx, in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatal(err)
	}
	if isToolError(m) {
		t.Fatalf("tool errored: %s", textOf(m))
	}
	if !strings.Contains(textOf(m), "杭州混凝土") {
		t.Fatalf("nl_search output missing matching supplier:\n%s", textOf(m))
	}
}

func TestNLSearchToolRejectsWhenAIConfigMissing(t *testing.T) {
	svc := supplier.NewService(memory.New())
	srv := mcp.New(svc, io.Discard)
	line := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nl_search_suppliers","arguments":{"query":"杭州市政"}}}`
	in := strings.NewReader(line + "\n")
	var out strings.Builder
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m)
	if !isToolError(m) || !strings.Contains(textOf(m), "AI 模型未配置") {
		t.Fatalf("expected AI-not-configured tool error, got: %s", textOf(m))
	}
}
