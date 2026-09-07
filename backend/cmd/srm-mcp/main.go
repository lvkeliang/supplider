// Command srm-mcp is the Supplider Model Context Protocol server. An MCP
// host (Claude Desktop, Cursor, another AI agent) spawns it over stdio and
// calls Tools / reads Resources / gets Prompts to search the local supplier
// database, read dossiers, run the non-AI risk/expiry scans and add
// suppliers.
//
// It opens the SAME personal-tier SQLite database the Tauri sidecar uses
// (the app's data directory, keyed by the Tauri identifier
// com.supplider.desktop). WAL mode lets the MCP process read and write
// alongside the running desktop app safely; if the app is closed the MCP
// server still works standalone — no sidecar, no network, no API keys.
//
// Configure an MCP host with:
//
//	{ "command": "srm-mcp", "args": [] }
//
// Override the database location with --data-dir or SRM_DATA_DIR.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/supplider/supplider/backend/internal/mcp"
	"github.com/supplider/supplider/backend/internal/storefactory"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// appIdentifier matches the Tauri bundle identifier (src-tauri/tauri.conf.json),
// so the default data dir is exactly where the desktop app writes.
const appIdentifier = "com.supplider.desktop"

func main() {
	dataDir := flag.String("data-dir", os.Getenv("SRM_DATA_DIR"), "data directory (defaults to the desktop app's data dir)")
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		dir = defaultAppDataDir()
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("srm-mcp: create data dir %q: %v", dir, err)
		}
	}

	store, err := storefactory.Open(storefactory.Config{DataDir: dir})
	if err != nil {
		log.Fatalf("srm-mcp: open store: %v", err)
	}
	defer store.Close()

	svc := supplier.NewService(store)
	server := mcp.New(svc, os.Stderr) // logs to stderr; stdout is JSON-RPC only

	log.Printf("srm-mcp serving MCP over stdio (data dir %q)", dir)
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("srm-mcp: %v", err)
	}
}

// defaultAppDataDir resolves the per-OS application data directory, matching
// Tauri's app_data_dir for the bundle identifier:
//   - Linux:   $XDG_DATA_HOME or ~/.local/share/<id>
//   - macOS:   ~/Library/Application Support/<id>
//   - Windows: %APPDATA%\<id>
func defaultAppDataDir() string {
	if runtime.GOOS == "linux" {
		if base := os.Getenv("XDG_DATA_HOME"); base != "" {
			return filepath.Join(base, appIdentifier)
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", appIdentifier)
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		return filepath.Join(cfg, appIdentifier)
	}
	return ""
}
