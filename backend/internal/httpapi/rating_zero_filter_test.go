package httpapi

// An explicit max_rating=0 means "unrated only" and must be distinguished
// from the bound being absent (which returns every rating). Previously both
// the frontend serializer dropped 0 and the adapters treated MaxRating<=0 as
// "no bound", so the unrated-only filter silently returned everything.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestListMaxRatingZeroMeansUnratedOnly(t *testing.T) {
	s := newTestServer(t)

	// Unrated supplier (no performance history → rating 0).
	createSupplier(t, s, "无评分供应商")
	// Rated supplier (one 4.0 overall performance entry).
	rated := `{"basic_info":{"company_name":"有评分供应商","region":{"province":"浙江","city":"杭州"}},` +
		`"performance_history":[{"project":"某项目","score":4}]}`
	if w := do(t, s, http.MethodPost, "/api/v1/suppliers", rated); w.Code != http.StatusCreated {
		t.Fatalf("create rated: %d %s", w.Code, w.Body.String())
	}

	names := func(path string) map[string]bool {
		w := do(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var page struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode: %v", err)
		}
		m := map[string]bool{}
		for _, it := range page.Items {
			m[it.Name] = true
		}
		return m
	}

	unrated := names("/api/v1/suppliers?max_rating=0")
	if !unrated["无评分供应商"] {
		t.Errorf("max_rating=0 should include the unrated supplier, got %v", unrated)
	}
	if unrated["有评分供应商"] {
		t.Errorf("max_rating=0 must exclude the rated supplier, got %v", unrated)
	}

	// Absent bound → both suppliers.
	all := names("/api/v1/suppliers")
	if !all["无评分供应商"] || !all["有评分供应商"] {
		t.Errorf("no bound should return all, got %v", all)
	}
}
