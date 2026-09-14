package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 淘汰-phase 风险预警：手动标记外部信号（被执行人 / 行政处罚）——个人版无
// 企查查/天眼查 API，这是该字段唯一的人工入口；本地规则引擎不覆盖它。

func TestSetRiskFlagsRecordsExternalSignal(t *testing.T) {
	s := newTestServer(t)
	id := createSupplierGetID(t, s, "杭州某某建设有限公司")

	w := do(t, s, http.MethodPost, "/api/v1/suppliers/"+id+"/risk-flags", `{"executed_person":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	doc := getSupplierDoc(t, s, id)
	if !doc.RiskFlags.ExecutedPerson {
		t.Fatal("executed_person must be set after the POST")
	}
	if doc.RiskFlags.AdminPenalty {
		t.Fatal("admin_penalty must stay false (not provided)")
	}
	// A manual change_log entry records the signal + source.
	found := false
	for _, c := range doc.ChangeLog {
		if c.Field == "risk_flags.executed_person" && c.New == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected risk_flags.executed_person change_log entry, got %+v", doc.ChangeLog)
	}
}

func TestSetRiskFlagsClearsSignal(t *testing.T) {
	s := newTestServer(t)
	id := createSupplierGetID(t, s, "杭州某某建设有限公司")
	do(t, s, http.MethodPost, "/api/v1/suppliers/"+id+"/risk-flags", `{"executed_person":true}`)
	do(t, s, http.MethodPost, "/api/v1/suppliers/"+id+"/risk-flags", `{"executed_person":false}`)

	if doc := getSupplierDoc(t, s, id); doc.RiskFlags.ExecutedPerson {
		t.Fatal("executed_person must be cleared after the second POST")
	}
}

func TestSetRiskFlagsRequiresASignal(t *testing.T) {
	s := newTestServer(t)
	id := createSupplierGetID(t, s, "杭州某某建设有限公司")
	w := do(t, s, http.MethodPost, "/api/v1/suppliers/"+id+"/risk-flags", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}

func TestSetRiskFlagsNotFound(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/suppliers/sup_2026_999999/risk-flags", `{"admin_penalty":true}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
}

// getSupplierDoc fetches a supplier and decodes the full document, returning a
// struct with the RiskFlags we need to assert.
func getSupplierDoc(t *testing.T, s *Server, id string) struct {
	RiskFlags struct {
		ExecutedPerson bool `json:"executed_person"`
		AdminPenalty   bool `json:"admin_penalty"`
	} `json:"risk_flags"`
	ChangeLog []struct {
		Field string `json:"field"`
		New   any    `json:"new"`
	} `json:"change_log"`
} {
	t.Helper()
	w := do(t, s, http.MethodGet, "/api/v1/suppliers/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get status %d body=%s", w.Code, w.Body.String())
	}
	var doc struct {
		RiskFlags struct {
			ExecutedPerson bool `json:"executed_person"`
			AdminPenalty   bool `json:"admin_penalty"`
		} `json:"risk_flags"`
		ChangeLog []struct {
			Field string `json:"field"`
			New   any    `json:"new"`
		} `json:"change_log"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
