package httpapi

// Status classification for the attachment download route: a traversal or
// malformed key is a 400 (and must never serve a file outside the store),
// a well-formed key with no object is a 404, and neither is ever a 500.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/featureflag"
	"github.com/supplider/supplider/backend/internal/objectstore/localfs"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestAttachmentDownloadStatusClassification(t *testing.T) {
	store, err := localfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("localfs: %v", err)
	}
	svc := supplier.NewService(memory.New())
	s := New(svc, featureflag.Default().WithAIState(false)).WithObjects(store)

	// Percent-encoded traversal reaches the object key (the bare "/.." form
	// is cleaned to a 301 by net/http before reaching the handler, so it is
	// not asserted here). These must be rejected as bad requests, never 500.
	for _, path := range []string{
		"/api/v1/attachments/..%2f..%2f..%2fetc%2fpasswd",
		"/api/v1/attachments/%2e%2e%2fescape.txt",
	} {
		w := do(t, s, http.MethodGet, path, "")
		if w.Code == http.StatusInternalServerError {
			t.Errorf("traversal key %q: must not be 500", path)
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("traversal key %q: got %d, want 400 (body=%q)", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "root:") {
			t.Errorf("traversal key %q leaked host file contents", path)
		}
	}

	// Control characters in the key (percent-encoded NUL reaches the
	// handler decoded) once died in syscall open (EINVAL) → 500; they are
	// now classified as bad keys → 400.
	for _, path := range []string{
		"/api/v1/attachments/a%00b",
		"/api/v1/attachments/sup_x/a%0db.txt",
	} {
		w := do(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("control-char key %q: got %d, want 400 (body=%q)", path, w.Code, w.Body.String())
		}
	}

	// Well-formed key, no stored object → 404, not 400/500.
	w := do(t, s, http.MethodGet, "/api/v1/attachments/sup_x/missing-file.bin", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("missing object: got %d, want 404 (body=%q)", w.Code, w.Body.String())
	}
}

func TestAttachmentDownloadsDisabledWhenNoObjectStore(t *testing.T) {
	// The ephemeral in-memory wiring leaves Objects nil → 501, never 500.
	s := newTestServer(t)
	w := do(t, s, http.MethodGet, "/api/v1/attachments/sup_x/a.bin", "")
	if w.Code != http.StatusNotImplemented {
		t.Errorf("no object store: got %d, want 501", w.Code)
	}
}
