package httpapi

// An unrecognized min_qual_level must be rejected, not silently disable the
// hard qualification filter (QualRank returns 0 for unknown input, which
// would otherwise return every supplier including unqualified ones).

import (
	"net/http"
	"net/url"
	"testing"
)

func urlEncode(s string) string { return url.QueryEscape(s) }

func TestListRejectsUnknownQualLevel(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{
		"/api/v1/suppliers?min_qual_level=" + urlEncode("肆级"),
		"/api/v1/suppliers?min_qual_level=1%E7%BA%A7", // "1级"
		"/api/v1/suppliers?min_qual_level=totally-wrong",
	} {
		w := do(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400 (body=%s)", path, w.Code, w.Body.String())
		}
	}
}

func TestListAcceptsKnownQualLevel(t *testing.T) {
	s := newTestServer(t)
	for _, lvl := range []string{"特级", "一级", "二级", "三级", "甲级", "乙级", "丙级"} {
		w := do(t, s, http.MethodGet, "/api/v1/suppliers?min_qual_level="+urlEncode(lvl), "")
		if w.Code != http.StatusOK {
			t.Errorf("min_qual_level=%s: got %d, want 200 (%s)", lvl, w.Code, w.Body.String())
		}
	}
	// Empty means no qual filter and stays valid.
	if w := do(t, s, http.MethodGet, "/api/v1/suppliers", ""); w.Code != http.StatusOK {
		t.Errorf("no filter: got %d", w.Code)
	}
}

func TestExportRejectsUnknownQualLevel(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodGet, "/api/v1/export?format=json&min_qual_level="+urlEncode("肆级"), "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("export bad level: got %d, want 400", w.Code)
	}
}
