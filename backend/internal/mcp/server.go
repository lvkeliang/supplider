// Package mcp exposes the Supplider supplier platform over the Model
// Context Protocol (MCP) as a stdio JSON-RPC 2.0 server. It lets any MCP
// host (Claude Desktop, Cursor, other AI agents) search the local supplier
// database, read dossiers, run the non-AI risk/expiry scans and add
// suppliers — the same business operations the CLI and HTTP API expose.
//
// Like httpapi, this package is tier-agnostic business code: it depends
// only on *supplier.Service and never on a concrete store. The stdio entry
// point (cmd/srm-mcp) opens the tier store via storefactory and serves.
//
// The protocol is implemented by hand (no SDK dependency) to keep the
// personal tier a small, pure-Go, dependency-free binary: MCP over stdio is
// newline-delimited JSON-RPC with a handful of methods. Logs go to stderr;
// stdout carries only protocol messages.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"runtime/debug"

	"github.com/supplider/supplider/backend/internal/supplier"
)

// ProtocolVersion is the MCP protocol revision this server speaks.
const ProtocolVersion = "2024-11-05"

// Server is the MCP stdio server.
type Server struct {
	svc    *supplier.Service
	logger *log.Logger
}

// New builds a server backed by the supplier service. Logs are written to
// logSink (pass os.Stderr in the command; stdout is reserved for JSON-RPC).
func New(svc *supplier.Service, logSink io.Writer) *Server {
	if logSink == nil {
		logSink = io.Discard
	}
	return &Server{svc: svc, logger: log.New(logSink, "mcp: ", log.LstdFlags)}
}

// ---- JSON-RPC 2.0 wire types ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC standard error codes.
const (
	errParseError     = -32700
	errInvalidReq     = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// maxMessageBytes bounds one JSON-RPC line. MCP messages are small; this is
// well above any add_supplier payload. An over-long line is rejected per
// message (the connection keeps serving) instead of killing the process.
const maxMessageBytes = 4 * 1024 * 1024

// Serve reads JSON-RPC messages (one object per line) from r and writes
// responses to w until stdin closes (EOF) or ctx is cancelled. Messages
// without an id are notifications and get no response.
//
// Every message is isolated: a panic inside a tool handler (or an oversized
// line) is reported as a JSON-RPC internal error and the server keeps
// running — an agent's MCP connection must never die because one tool call
// hit unexpected data.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	enc := json.NewEncoder(w)

	for {
		line, readErr := reader.ReadString('\n')
		if len(bytesTrimSpace([]byte(line))) > 0 {
			id, result, rerr, isNotification := s.handleLine(ctx, line)
			// Per the spec, only a successfully-parsed notification (method
			// present, no id) stays silent; parse errors answer id: null.
			if !isNotification {
				writeResp(enc, id, result, rerr)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return fmt.Errorf("mcp: read stdin: %w", readErr)
		}
	}
}

// dispatcherFunc routes a parsed method (the production Server wires this
// to dispatch; tests inject a panicking one to prove the guard).
type dispatcherFunc func(ctx context.Context, method string, params json.RawMessage) (any, *rpcError)

// handleLine parses and dispatches ONE JSON-RPC line with a panic guard.
// isNotification is true only for a successfully-parsed id-less message.
// Malformed protocol gets its null/id response; a handler panic becomes a
// JSON-RPC internal error so the stdio loop survives it.
func (s *Server) handleLine(ctx context.Context, line string) (json.RawMessage, any, *rpcError, bool) {
	return s.handleLineDispatch(ctx, line, s.dispatch)
}

func (s *Server) handleLineDispatch(ctx context.Context, line string, dispatch dispatcherFunc) (id json.RawMessage, result any, rerr *rpcError, isNotification bool) {
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Printf("panic in tool dispatch: %v\n%s", rec, debug.Stack())
			id, result, rerr, isNotification = nil, nil,
				&rpcError{Code: errInternal, Message: fmt.Sprintf("internal error: %v", rec)}, false
		}
	}()

	trimmed := bytesTrimSpace([]byte(line))
	if len(trimmed) > maxMessageBytes {
		return nil, nil, &rpcError{Code: errInvalidReq, Message: "message too large (max 4 MiB per line)"}, false
	}

	var req rpcRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return nil, nil, &rpcError{Code: errParseError, Message: "parse error: " + err.Error()}, false
	}
	if req.JSONRPC != "2.0" && req.JSONRPC != "" {
		return req.ID, nil, &rpcError{Code: errInvalidReq, Message: "only JSON-RPC 2.0 is supported"}, false
	}
	// Notifications carry no id — per the spec they get no response.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		isNotification = true
	}
	result, rerr = dispatch(ctx, req.Method, req.Params)
	return req.ID, result, rerr, isNotification
}

func writeResp(enc *json.Encoder, id json.RawMessage, result any, rerr *rpcError) {
	if id == nil {
		id = json.RawMessage("null")
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rerr}
	if err := enc.Encode(resp); err != nil {
		// Nothing useful we can do if the pipe is broken.
		return
	}
}

// dispatch routes one JSON-RPC method to a handler.
func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.initialize(), nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		return s.callTool(ctx, params)
	case "resources/list":
		return map[string]any{"resources": staticResources()}, nil
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": resourceTemplates()}, nil
	case "resources/read":
		return s.readResource(ctx, params)
	case "prompts/list":
		return map[string]any{"prompts": promptDefs()}, nil
	case "prompts/get":
		return s.getPrompt(ctx, params)
	case "notifications/initialized", "notifications/cancelled", "$/cancelRequest":
		// Lifecycle/cancel notifications: nothing to do (handled as
		// notifications by Serve before reaching here, but listed for
		// clarity if a host ever sends them with an id).
		return map[string]any{}, nil
	default:
		return nil, &rpcError{Code: errMethodNotFound, Message: "method not found: " + method}
	}
}

// initialize advertises the server's capabilities.
func (s *Server) initialize() map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
			"prompts":   map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "supplider-mcp",
			"version": "0.1.0",
		},
	}
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r' || b[len(b)-1] == '\n') {
		b = b[:len(b)-1]
	}
	return b
}
