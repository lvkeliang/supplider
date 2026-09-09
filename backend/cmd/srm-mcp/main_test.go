package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

// srm-mcp defaults to the SAME per-user data directory the Tauri desktop
// app uses (identifier com.supplider.desktop). A regression here would make
// a standalone MCP server silently open a DIFFERENT (empty) library while
// claiming to share the desktop app's data — the core multi-process promise
// in docs/mcp/SETUP.md. The Linux branch reads XDG_DATA_HOME / HOME directly,
// so both forms are deterministic; macOS/Windows route through
// os.UserConfigDir (documented expected paths below).
func TestDefaultAppDataDirLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG branch is Linux-specific")
	}
	const id = "com.supplider.desktop"

	t.Run("honors XDG_DATA_HOME", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/custom/data")
		if got, want := defaultAppDataDir(), filepath.Join("/custom/data", id); got != want {
			t.Errorf("XDG_DATA_HOME set: got %q, want %q", got, want)
		}
	})

	t.Run("falls back to ~/.local/share", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/alice")
		if got, want := defaultAppDataDir(),
			filepath.Join("/home/alice", ".local", "share", id); got != want {
			t.Errorf("XDG unset: got %q, want %q", got, want)
		}
	})
}

// The identifier must stay in sync with tauri.conf.json's "identifier" —
// app_data_dir() is derived from it, so a mismatch splits the data stores.
func TestAppIdentifierMatchesDesktopBundle(t *testing.T) {
	if appIdentifier != "com.supplider.desktop" {
		t.Fatalf("appIdentifier = %q, want com.supplider.desktop (tauri.conf.json identifier)", appIdentifier)
	}
}
