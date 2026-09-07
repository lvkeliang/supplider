package localfs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/objectstore"
	"github.com/supplider/supplider/backend/internal/objectstore/localfs"
)

func openStore(t *testing.T) *localfs.Store {
	t.Helper()
	st, err := localfs.New(filepath.Join(t.TempDir(), "attachments"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return st
}

func TestPutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	content := []byte("合同扫描件内容：supplider attachment test 附件")
	key := "sup_2026_000001/1694000000_合同.pdf"

	obj, err := st.Put(ctx, key, "合同.pdf", "application/pdf", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", obj.Size, len(content))
	}

	rc, got, err := st.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if !bytes.Equal(data, content) {
		t.Errorf("content mismatch: got %q", data)
	}
	if got.Size != int64(len(content)) {
		t.Errorf("Get Size = %d", got.Size)
	}

	url, err := st.URL(ctx, key)
	if err != nil || url != "/api/v1/attachments/"+key {
		t.Errorf("URL = %q, %v", url, err)
	}
}

func TestPutRejectsOversize(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	// Declared over limit — must be rejected before writing.
	// Declared size over the limit is rejected before writing (tiny body).
	_, err := st.Put(ctx, "sup_x/1_big.bin", "big.bin", "application/octet-stream",
		strings.NewReader("x"), objectstore.MaxAttachmentSize+1)
	var tooLarge *objectstore.ErrTooLarge
	if err == nil {
		t.Fatal("expected error for over-limit declared size")
	}
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ErrTooLarge, got %T %v", err, err)
	}

	// Lying size (declared 0 but the stream overruns) must ALSO be rejected —
	// the limited reader catches it even when Content-Length is wrong.
	over := io.LimitReader(zeroReader{}, objectstore.MaxAttachmentSize+10)
	_, err = st.Put(ctx, "sup_x/2_liar.bin", "liar.bin", "", over, 0)
	if !errors.As(err, &tooLarge) {
		t.Fatalf("stream overrun should yield *ErrTooLarge, got %T %v", err, err)
	}
}

// zeroReader is an infinite zero-byte reader (no allocation).
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestPathTraversalRejected(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	for _, bad := range []string{
		"../../../etc/passwd",
		"sup_x/../../escape.txt",
		"..",
		"",
	} {
		_, err := st.Put(ctx, bad, "x", "", strings.NewReader("x"), 1)
		if err == nil {
			t.Errorf("Put with key %q should fail", bad)
		}
		if _, _, err := st.Get(ctx, bad); err == nil {
			t.Errorf("Get with key %q should fail", bad)
		}
	}
}

func TestGetMissingAndRemove(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	if _, _, err := st.Get(ctx, "sup_x/missing.txt"); err != objectstore.ErrObjectNotFound {
		t.Fatalf("missing Get = %v, want ErrObjectNotFound", err)
	}
	// Remove of a missing key is idempotent (no error).
	if err := st.Remove(ctx, "sup_x/missing.txt"); err != nil {
		t.Errorf("Remove missing = %v, want nil", err)
	}

	if _, err := st.Put(ctx, "sup_x/a.txt", "a.txt", "text/plain", strings.NewReader("hi"), 2); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.Remove(ctx, "sup_x/a.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, _, err := st.Get(ctx, "sup_x/a.txt"); err != objectstore.ErrObjectNotFound {
		t.Fatalf("after Remove Get = %v, want ErrObjectNotFound", err)
	}
}
