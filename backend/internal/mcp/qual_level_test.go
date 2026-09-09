package mcp_test

// An unrecognized min_qual_level must surface as a tool-level error the
// agent can correct, never silently run the search without the hard filter.

import (
	"strings"
	"testing"
)

func TestSearchRejectsUnknownQualLevel(t *testing.T) {
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_suppliers","arguments":{"min_qual_level":"肆级"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_suppliers","arguments":{"min_qual_level":"二级"}}}`,
	}
	resps := runSession(t, lines)
	if len(resps) != 2 {
		t.Fatalf("got %d responses", len(resps))
	}
	if !isToolError(resps[0]) {
		t.Errorf("unknown qual level should be a tool error: %+v", resps[0]["result"])
	}
	if txt := textOf(resps[0]); !strings.Contains(txt, "min_qual_level") {
		t.Errorf("error should name the bad parameter, got %q", txt)
	}
	// A valid level still searches successfully.
	if isToolError(resps[1]) {
		t.Errorf("valid qual level should not error: %+v", resps[1]["result"])
	}
}
