package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cmdAnalyze round-trip against a fake sidecar: upload a requirement txt, the
// server returns extracted requirement + ranked recommendations, and the CLI
// prints them (JSON mode for easy assertion).

func TestCmdAnalyzePrintsRecommendations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ai/doc-search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"requirement":{"requirement":"杭州市政工程","province":"浙江","city":"杭州","category":"市政工程"},
			"results":[{"id":"sup_2026_000001","name":"杭州混凝土工程有限公司","province":"浙江","city":"杭州",
				"top_qual":"二级","rating":4.5,"score":0.93,"reason":"高度匹配 · 地域匹配"}]
		}`))
	}))
	defer srv.Close()

	orig := os.Getenv("SRM_API_ADDR")
	_ = os.Setenv("SRM_API_ADDR", srv.URL)
	defer os.Setenv("SRM_API_ADDR", orig)

	f := filepath.Join(t.TempDir(), "req.txt")
	if err := os.WriteFile(f, []byte("杭州市政工程需求"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture stdout.
	old := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw
	err := cmdAnalyze([]string{f, "--json"})
	_ = pw.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(pr)

	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "杭州混凝土工程有限公司") || !strings.Contains(out, "杭州市政工程") {
		t.Fatalf("analyze output missing requirement/results:\n%s", out)
	}
}

func TestCmdAnalyzeUsageError(t *testing.T) {
	if err := cmdAnalyze(nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no-arg call must return usage error, got %v", err)
	}
}

// TestCmdNLSearchParsesAndLists: a fake sidecar serves /ai/nl-search (returns a
// structured filter) and /api/v1/suppliers (returns a matching summary); the
// CLI prints the parsed filter + the supplier.
func TestCmdNLSearchParsesAndLists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/ai/nl-search":
			w.Write([]byte(`{"keyword":"商砼","province":"浙江","city":"杭州","category":"市政工程","min_qual_level":"二级","min_rating":0}`))
		case "/api/v1/suppliers":
			w.Write([]byte(`{"items":[{"id":"sup_2026_000001","name":"杭州混凝土工程有限公司","province":"浙江","city":"杭州","top_qual":"二级","rating":4.5}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	orig := os.Getenv("SRM_API_ADDR")
	_ = os.Setenv("SRM_API_ADDR", srv.URL)
	defer os.Setenv("SRM_API_ADDR", orig)

	old := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw
	err := cmdNLSearch([]string{"杭州市政二级"})
	_ = pw.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(pr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "杭州混凝土") {
		t.Fatalf("ainl output missing supplier:\n%s", buf.String())
	}
}

func TestCmdNLSearchUsageError(t *testing.T) {
	if err := cmdNLSearch([]string{}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no-arg call must return usage error, got %v", err)
	}
}
