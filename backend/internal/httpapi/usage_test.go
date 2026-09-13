package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GET /api/v1/ai/usage — persists per-month AI token usage across the whole
// call path: PUT /ai/config attaches a usage sink, an AI call records it, and
// GET /ai/usage reads the counters back from the settings store.

func usageUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/embeddings" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":120,"completion_tokens":30}}`))
	}))
}

func TestAIUsageEndpointEmptyWhenUnused(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodGet, "/api/v1/ai/usage", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var sum struct {
		Month string
		Total struct {
			Calls     int `json:"calls"`
			TokensIn  int `json:"tokens_in"`
			TokensOut int `json:"tokens_out"`
		} `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Month == "" || sum.Total.Calls != 0 {
		t.Fatalf("unused usage = %+v", sum)
	}
}

func TestAIUsageRecordsAICall(t *testing.T) {
	upstream := usageUpstream(t)
	defer upstream.Close()

	s := newTestServer(t)

	// Configure AI via the endpoint so the usage sink is attached.
	body := `{"format":"openai","base_url":"` + upstream.URL + `","api_key":"sk-test","model":"deepseek-chat"}`
	if w := do(t, s, http.MethodPut, "/api/v1/ai/config", body); w.Code != http.StatusOK {
		t.Fatalf("config status %d body=%s", w.Code, w.Body.String())
	}

	// A chat call must be recorded.
	id := createSupplierGetID(t, s, "杭州测试有限公司")
	if w := do(t, s, http.MethodPost, "/api/v1/ai/summarize/"+id, ""); w.Code != http.StatusOK {
		t.Fatalf("summarize status %d body=%s", w.Code, w.Body.String())
	}

	w := do(t, s, http.MethodGet, "/api/v1/ai/usage", "")
	var sum struct {
		Month string
		Total struct {
			Calls     int `json:"calls"`
			TokensIn  int `json:"tokens_in"`
			TokensOut int `json:"tokens_out"`
		} `json:"total"`
		ByTask map[string]struct {
			Calls     int `json:"calls"`
			TokensIn  int `json:"tokens_in"`
			TokensOut int `json:"tokens_out"`
		} `json:"by_task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Total.Calls != 1 || sum.Total.TokensIn != 120 || sum.Total.TokensOut != 30 {
		t.Fatalf("usage total = %+v, want calls=1 in=120 out=30", sum.Total)
	}
	if sum.ByTask["summarize"].Calls != 1 {
		t.Fatalf("by_task = %+v, want summarize recorded", sum.ByTask)
	}
}

func TestAIUsageRecordsAcrossMultipleTasks(t *testing.T) {
	upstream := usageUpstream(t)
	defer upstream.Close()

	s := newTestServer(t)
	body := `{"format":"openai","base_url":"` + upstream.URL + `","api_key":"sk-test","model":"deepseek-chat","embedding_model":"text-embedding-3-small"}`
	if w := do(t, s, http.MethodPut, "/api/v1/ai/config", body); w.Code != http.StatusOK {
		t.Fatalf("config status %d body=%s", w.Code, w.Body.String())
	}

	id := createSupplierGetID(t, s, "杭州测试有限公司")
	do(t, s, http.MethodPost, "/api/v1/ai/summarize/"+id, "")
	do(t, s, http.MethodPost, "/api/v1/ai/nl-search", `{"query":"杭州市政"}`)

	w := do(t, s, http.MethodGet, "/api/v1/ai/usage", "")
	var sum struct {
		Total struct {
			Calls int `json:"calls"`
		} `json:"total"`
		ByTask map[string]struct {
			Calls int `json:"calls"`
		} `json:"by_task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Total.Calls != 2 {
		t.Fatalf("total calls = %d, want 2", sum.Total.Calls)
	}
	if sum.ByTask["summarize"].Calls != 1 || sum.ByTask["nl2filter"].Calls != 1 {
		t.Fatalf("by_task = %+v", sum.ByTask)
	}
}
