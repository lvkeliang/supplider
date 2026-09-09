package httpapi

// Malformed-body classification for the admin visibility policy and the
// other optional-body action endpoints. These endpoints accept EITHER a JSON
// body OR query parameters (CLI convenience): an empty body must keep
// working, but a present-but-broken body is a 400 — never silently applied Go
// zero values. The policy case was actively dangerous: {"max_level":"x"}
// failed to decode into *int AFTER the allocator zeroed it, persisting the
// strictest policy (0) and immediately running an enforce sweep.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func currentPolicyConfigured(t *testing.T, s *Server) (maxLevel int, configured bool) {
	t.Helper()
	w := do(t, s, http.MethodGet, "/api/v1/visibility/policy", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET policy: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Policy struct {
			MaxLevel int `json:"max_level"`
		} `json:"policy"`
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	return resp.Policy.MaxLevel, resp.Configured
}

func TestSaveVisibilityPolicyRejectsMalformedBody(t *testing.T) {
	s := newTestServer(t)

	if _, configured := currentPolicyConfigured(t, s); configured {
		t.Fatal("fresh server should have no persisted policy")
	}

	for _, body := range []string{
		`{"max_level":"x"}`,    // wrong type: must not become the strictest level 0
		`{"max_level":{}}`,     // object type
		`{not json`,            // syntactically broken
		`{"max_level":1} junk`, // trailing garbage
	} {
		w := do(t, s, http.MethodPut, "/api/v1/visibility/policy", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400 (resp=%s)", body, w.Code, w.Body.String())
		}
	}

	// Nothing may have been persisted despite the rejected requests.
	if level, configured := currentPolicyConfigured(t, s); configured {
		t.Errorf("rejected requests persisted a policy (max_level=%d)", level)
	}
}

func TestSaveVisibilityPolicyQueryFallbackStillWorks(t *testing.T) {
	s := newTestServer(t)

	// Empty body + query params is the CLI path and must keep working.
	w := do(t, s, http.MethodPut, "/api/v1/visibility/policy?max_level=1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("empty body + query: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if level, configured := currentPolicyConfigured(t, s); !configured || level != 1 {
		t.Errorf("policy = (level=%d, configured=%v), want (1, true)", level, configured)
	}

	// A present valid body with a garbage query buffer_days must 400.
	w = do(t, s, http.MethodPut, "/api/v1/visibility/policy?buffer_days=soon",
		`{"max_level":1}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("buffer_days=soon: got %d, want 400", w.Code)
	}
}

// TestOptionalBodyEndpointsRejectBrokenJSON pins the uniform rule across the
// action endpoints that otherwise tolerate an empty body.
func TestOptionalBodyEndpointsRejectBrokenJSON(t *testing.T) {
	s := newTestServer(t)
	createSupplier(t, s, "杭州可选体测试有限公司")
	list := do(t, s, http.MethodGet, "/api/v1/suppliers?limit=1", "")
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("seed supplier missing: %d %s", list.Code, list.Body.String())
	}
	id := page.Items[0].ID

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/suppliers/" + id + "/watch"},
		{http.MethodPost, "/api/v1/suppliers/" + id + "/blacklist"},
		{http.MethodPost, "/api/v1/suppliers/" + id + "/merge"},
		{http.MethodPost, "/api/v1/suppliers/" + id + "/risk-review"},
		{http.MethodPost, "/api/v1/suppliers/" + id + "/appeal-visibility"},
		{http.MethodPut, "/api/v1/preferences/local"},
	} {
		w := do(t, s, tc.method, tc.path, `{broken`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s with broken JSON: got %d, want 400 (body=%s)",
				tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// The same endpoints still accept an empty body (toggle/query defaults).
	if w := do(t, s, http.MethodPost, "/api/v1/suppliers/"+id+"/watch", ""); w.Code != http.StatusOK {
		t.Errorf("empty watch body: got %d, want 200", w.Code)
	}
}
