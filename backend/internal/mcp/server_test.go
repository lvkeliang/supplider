package mcp_test

// End-to-end test of the MCP stdio server against the in-memory store: it
// drives a full JSON-RPC session (initialize → tools/list → add → search →
// risk queue → resources → prompts → error) through the real Serve loop,
// the same way an MCP host speaks to the stdio binary.

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/mcp"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// runSession feeds newline-delimited JSON-RPC requests to a fresh server and
// returns the raw response lines.
func runSession(t *testing.T, lines []string) []map[string]any {
	t.Helper()
	svc := supplier.NewService(memory.New())
	srv := mcp.New(svc, io.Discard)

	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out strings.Builder
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	resps := make([]map[string]any, 0, len(lines))
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response is not valid JSON: %q (%v)", line, err)
		}
		resps = append(resps, m)
	}
	return resps
}

// textOf extracts the first text block of a tools/call result.
func textOf(resp map[string]any) string {
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	block, _ := content[0].(map[string]any)
	t, _ := block["text"].(string)
	return t
}

func isToolError(resp map[string]any) bool {
	result, _ := resp["result"].(map[string]any)
	isErr, _ := result["isError"].(bool)
	return isErr
}

func TestMCPSession(t *testing.T) {
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		// Clean, fully-populated trade supplier → no shell risk.
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add_supplier","arguments":{"company_name":"杭州正规贸易有限公司","province":"浙江","city":"杭州","legal_person":"李四","registered_capital":"3000万人民币","business_scope":"建材批发","establishment_date":"2012-03-10","supplier_type":"贸易商","contact_phone":"13900000000"}}}`,
		// Risky: bad credit-code checksum + thin profile → shell risk.
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"add_supplier","arguments":{"company_name":"温州空壳贸易有限公司","province":"浙江","city":"温州","credit_code":"91330100MA27X00000"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_suppliers","arguments":{"q":"杭州"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"shell_risk_queue","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"supplider://suppliers"}}`,
		`{"jsonrpc":"2.0","id":8,"method":"prompts/get","params":{"name":"supplier-due-diligence","arguments":{"query":"杭州正规贸易有限公司"}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"get_supplier","arguments":{"id":"sup_does_not_exist"}}}`,
		// Unknown method → JSON-RPC error.
		`{"jsonrpc":"2.0","id":10,"method":"bogus/method"}`,
	}
	resps := runSession(t, lines)
	if len(resps) != 10 {
		t.Fatalf("got %d responses, want 10", len(resps))
	}

	// 1: initialize advertises tools/resources/prompts capabilities.
	init, _ := resps[0]["result"].(map[string]any)
	if init["protocolVersion"] != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}
	caps, _ := init["capabilities"].(map[string]any)
	for _, c := range []string{"tools", "resources", "prompts"} {
		if _, ok := caps[c]; !ok {
			t.Errorf("capability %q not advertised", c)
		}
	}

	// 2: tools/list exposes the expected tool set.
	toolsRaw, _ := json.Marshal(resps[1]["result"])
	for _, want := range []string{"search_suppliers", "get_supplier", "add_supplier",
		"expiring_qualifications", "shell_risk_queue", "supplier_risk", "compare_suppliers"} {
		if !strings.Contains(string(toolsRaw), `"name":"`+want+`"`) {
			t.Errorf("tools/list missing %s", want)
		}
	}

	// 3: clean add succeeds, no risk warning.
	if txt := textOf(resps[2]); !strings.Contains(txt, "已录入供应商") {
		t.Errorf("add clean result = %q", txt)
	}

	// 4: risky add succeeds (never blocked) and warns about shell risk.
	txt4 := textOf(resps[3])
	if !strings.Contains(txt4, "已录入供应商") || !strings.Contains(txt4, "空壳风险") {
		t.Errorf("add risky result = %q", txt4)
	}

	// 5: search by keyword finds the clean supplier.
	if txt := textOf(resps[4]); !strings.Contains(txt, "杭州正规贸易有限公司") {
		t.Errorf("search result = %q", txt)
	}

	// 6: shell-risk queue lists only the risky supplier.
	txt6 := textOf(resps[5])
	if !strings.Contains(txt6, "温州空壳贸易有限公司") || !strings.Contains(txt6, `"count": 1`) {
		t.Errorf("shell queue = %q", txt6)
	}
	if strings.Contains(txt6, "杭州正规贸易有限公司") {
		t.Error("clean supplier wrongly in shell-risk queue")
	}

	// 7: resource read returns the suppliers resource (uri + JSON text).
	resRaw, _ := json.Marshal(resps[6]["result"])
	if !strings.Contains(string(resRaw), "supplider://suppliers") ||
		!strings.Contains(string(resRaw), "杭州正规贸易有限公司") {
		t.Errorf("resource read result = %s", resRaw)
	}

	// 8: prompt renders the due-diligence instruction.
	promptRaw, _ := json.Marshal(resps[7]["result"])
	if !strings.Contains(string(promptRaw), "尽调") {
		t.Errorf("prompt result = %s", promptRaw)
	}

	// 9: missing supplier surfaces as a tool error (isError), not a crash.
	if !isToolError(resps[8]) {
		t.Errorf("missing supplier should be a tool error: %+v", resps[8]["result"])
	}

	// 10: unknown method → JSON-RPC method-not-found error.
	rpcErr, _ := resps[9]["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32601 {
		t.Errorf("unknown method error = %+v", resps[9]["error"])
	}
}

func TestMCPNotificationGetsNoResponse(t *testing.T) {
	// A notification (no id) must NOT produce a response line.
	svc := supplier.NewService(memory.New())
	srv := mcp.New(svc, io.Discard)
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	var out strings.Builder
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("notification produced a response: %q", out.String())
	}
}
