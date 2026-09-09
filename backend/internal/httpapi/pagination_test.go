package httpapi

// HTTP-level guards for keyset pagination. The cursor is an opaque token
// the client echoes verbatim; a stale link, a hand-edited URL, or a garbled
// CLI argument must surface as a 4xx (client error), never a 500, while the
// tokens the server itself hands out must page the whole result set.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/featureflag"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	svc := supplier.NewService(memory.New())
	return New(svc, featureflag.Default().WithAIState(false))
}

func do(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, r)
	return w
}

func createSupplier(t *testing.T, s *Server, name string) {
	t.Helper()
	body := `{"basic_info":{"company_name":"` + name +
		`","region":{"province":"浙江","city":"杭州"}}}`
	w := do(t, s, http.MethodPost, "/api/v1/suppliers", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %s: status %d body=%s", name, w.Code, w.Body.String())
	}
}

func TestListInvalidCursorIsBadRequestNot500(t *testing.T) {
	s := newTestServer(t)
	createSupplier(t, s, "测试有限公司")

	// Every value is supplied as it would arrive on the wire; query values
	// are percent-decoded by the server before reaching the cursor codec.
	for _, cursor := range []string{
		"!!!notbase64!!!", // illegal base64
		"bm90LXJlYWw=",    // valid base64, not a cursor JSON
		"null",
		"%00garbage",
		"%ff%fe",
	} {
		w := do(t, s, http.MethodGet, "/api/v1/suppliers?cursor="+cursor, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("cursor %q: got %d, want 400 (body=%s)", cursor, w.Code, w.Body.String())
		}
	}

	// No cursor is a normal first page.
	if w := do(t, s, http.MethodGet, "/api/v1/suppliers", ""); w.Code != http.StatusOK {
		t.Errorf("empty cursor: got %d, want 200", w.Code)
	}
}

func TestListRealCursorPagesEntireResultSet(t *testing.T) {
	s := newTestServer(t)
	const n = 3
	for i := 0; i < n; i++ {
		createSupplier(t, s, "公司"+string(rune('甲'+i))+"有限公司")
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		path := "/api/v1/suppliers?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := do(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("page %d: status %d body=%s", pages, w.Code, w.Body.String())
		}
		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode page %d: %v", pages, err)
		}
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("supplier %s returned twice — cursor not advancing", it.ID)
			}
			seen[it.ID] = true
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > n+1 {
			t.Fatalf("pagination did not terminate after %d pages", pages)
		}
	}
	if len(seen) != n {
		t.Fatalf("paged %d unique suppliers, want %d", len(seen), n)
	}
}
