package mcp

// Per-message robustness: one malformed/oversized/panicking JSON-RPC message
// must never kill the stdio server — an agent's MCP connection stays alive
// and gets a proper JSON-RPC error for the offending message.

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func newRobustServer() *Server {
	return New(supplier.NewService(memory.New()), io.Discard)
}

// TestPanicInDispatchBecomesInternalError proves the guard directly: an
// injected dispatcher that panics is translated to an errInternal response
// instead of unwinding into Serve and killing the process.
func TestPanicInDispatchBecomesInternalError(t *testing.T) {
	srv := newRobustServer()
	panicDispatch := func(context.Context, string, json.RawMessage) (any, *rpcError) {
		panic("boom: nil map write")
	}

	_, _, rerr, isNotification := srv.handleLineDispatch(
		context.Background(),
		`{"jsonrpc":"2.0","id":7,"method":"future_tool"}`,
		panicDispatch,
	)
	if isNotification {
		t.Error("panicking request must not be classified as a notification")
	}
	if rerr == nil || rerr.Code != errInternal {
		t.Fatalf("panic result = %v, want errInternal rpcError", rerr)
	}
	if !strings.Contains(rerr.Message, "boom") {
		t.Errorf("internal error should carry the panic value, got %q", rerr.Message)
	}
}

// TestServeSurvivesBadAndOversizedMessages drives the real Serve loop with
// an unparseable line, an over-long line, and a final healthy ping: every
// request is answered and the connection survives to the last message.
func TestServeSurvivesBadAndOversizedMessages(t *testing.T) {
	srv := newRobustServer()

	var b strings.Builder
	b.WriteString("this is not json\n")
	b.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_supplier","arguments":{}}}` + "\n")
	b.WriteString(strings.Repeat("x", maxMessageBytes+1024) + "\n")
	b.WriteString(`{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n")

	var out strings.Builder
	if err := srv.Serve(context.Background(), strings.NewReader(b.String()), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var resps []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("non-JSON response line: %q", line)
		}
		resps = append(resps, m)
	}
	if len(resps) != 4 {
		t.Fatalf("got %d responses, want 4 (all three bad messages + ping)", len(resps))
	}

	// Parse errors are required by JSON-RPC to answer with id: null.
	if errObj, _ := resps[0]["error"].(map[string]any); errObj == nil ||
		errObj["code"].(float64) != errParseError || resps[0]["id"] != nil {
		t.Errorf("parse error = %+v, want code %d with null id", resps[0], errParseError)
	}

	// Missing-arg tool call is a tool-level error, connection unaffected.
	if _, ok := resps[1]["result"]; !ok {
		t.Errorf("tool call should still be answered after a parse error: %+v", resps[1])
	}

	// Oversized line → invalid request, answered, loop continues.
	if errObj, _ := resps[2]["error"].(map[string]any); errObj == nil ||
		errObj["code"].(float64) != errInvalidReq {
		t.Errorf("oversize response = %+v, want code %d", resps[2], errInvalidReq)
	}

	// Final ping after all the bad input must still succeed.
	if resps[3]["result"] == nil {
		t.Errorf("final ping did not succeed: %+v", resps[3])
	}
}
