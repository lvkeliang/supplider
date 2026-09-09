package httpapi

// Write-path qualification expiry validation must surface as a client-fault
// 400, not a 500: an expiry containing digits that matches none of the
// accepted date layouts is a typo the caller can correct. Empty, digit-free
// permanent notes (长期有效) and well-formed dates are accepted.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCreateRejectsTypoExpiryAs400(t *testing.T) {
	s := newTestServer(t)
	body := `{"basic_info":{"company_name":"杭州一建","region":{"province":"浙江","city":"杭州"}},"qualifications":[{"type":"建筑工程施工总承包","level":"一级","expiry":"2026/13/01"}]}`
	w := do(t, s, http.MethodPost, "/api/v1/suppliers", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("typo expiry on create: got %d, want 400 (body=%s)", w.Code, w.Body.String())
	}

	// Same shape with a permanent note and then a valid date both succeed.
	for _, exp := range []string{"长期有效", "2027-06-30"} {
		ok := `{"basic_info":{"company_name":"杭州二建` + exp +
			`","region":{"province":"浙江","city":"杭州"}},"qualifications":[{"type":"市政","level":"二级","expiry":"` + exp + `"}]}`
		if w := do(t, s, http.MethodPost, "/api/v1/suppliers", ok); w.Code != http.StatusCreated {
			t.Errorf("expiry %q: got %d, want 201 (body=%s)", exp, w.Code, w.Body.String())
		}
	}
}

func TestUpdateRejectsTypoExpiryAs400(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/suppliers",
		`{"basic_info":{"company_name":"杭州三建","region":{"province":"浙江","city":"杭州"}}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed create: got %d (body=%s)", w.Code, w.Body.String())
	}
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || doc.ID == "" {
		t.Fatalf("decode created id: %v (body=%s)", err, w.Body.String())
	}

	bad := `{"qualifications":[{"type":"建筑工程施工总承包","level":"一级","expiry":"abc2026"}]}`
	if w := do(t, s, http.MethodPatch, "/api/v1/suppliers/"+doc.ID, bad); w.Code != http.StatusBadRequest {
		t.Errorf("typo expiry on update: got %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	good := `{"qualifications":[{"type":"建筑工程施工总承包","level":"一级","expiry":"2027年6月30日"}]}`
	if w := do(t, s, http.MethodPatch, "/api/v1/suppliers/"+doc.ID, good); w.Code != http.StatusOK {
		t.Errorf("Chinese-layout expiry on update: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
}
