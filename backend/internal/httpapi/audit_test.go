package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 操作审计日志: lifecycle mutations record cross-supplier trail; GET /audit
// returns it newest first.

func TestAuditRecordsLifecycleAndLists(t *testing.T) {
	s := newTestServer(t)

	id := createSupplierGetID(t, s, "杭州审计测试有限公司") // records "create"
	// Blacklist then unblacklist then archive → records each.
	if w := do(t, s, http.MethodPost, "/api/v1/suppliers/"+id+"/blacklist", `{"reason":"测试"}`); w.Code != http.StatusOK {
		t.Fatalf("blacklist status %d body=%s", w.Code, w.Body.String())
	}
	if w := do(t, s, http.MethodPost, "/api/v1/suppliers/"+id+"/unblacklist", ""); w.Code != http.StatusOK {
		t.Fatalf("unblacklist status %d body=%s", w.Code, w.Body.String())
	}
	if w := do(t, s, http.MethodDelete, "/api/v1/suppliers/"+id, ""); w.Code != http.StatusNoContent {
		t.Fatalf("archive status %d body=%s", w.Code, w.Body.String())
	}

	w := do(t, s, http.MethodGet, "/api/v1/audit", "")
	if w.Code != http.StatusOK {
		t.Fatalf("audit status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Count int `json:"count"`
		Items []struct {
			Action   string `json:"action"`
			Name     string `json:"name"`
			Actor    string `json:"actor"`
			TargetID string `json:"target_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 4 { // create + blacklist + unblacklist + archive
		t.Fatalf("count = %d, want 4 (items=%+v)", resp.Count, resp.Items)
	}
	// Newest first: archive is the latest action.
	if resp.Items[0].Action != "archive" {
		t.Fatalf("newest = %+v, want archive", resp.Items[0])
	}
	if resp.Items[0].Name != "杭州审计测试有限公司" {
		t.Fatalf("name = %q", resp.Items[0].Name)
	}
	if resp.Items[0].Actor != "local" {
		t.Fatalf("actor = %q, want local", resp.Items[0].Actor)
	}
}
