package documentextract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildZip assembles an in-memory ZIP archive from a name → bytes map, so the
// tests need no committed binary fixtures.
func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocxExtractsParagraphText(t *testing.T) {
	data := buildZip(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>杭州市政工程</w:t></w:r></w:p>
    <w:p><w:r><w:t>C30</w:t></w:r><w:t>商品混凝土</w:t><w:r><w:t>约5000m³</w:t></w:r></w:p>
  </w:body>
</w:document>`,
	})

	got, err := FromFile(data, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "杭州市政工程") || !strings.Contains(got, "商品混凝土") || !strings.Contains(got, "5000m³") {
		t.Fatalf("extracted = %q", got)
	}
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected paragraph breaks, got %q", got)
	}
}

func TestXlsxExtractsSharedStrings(t *testing.T) {
	data := buildZip(t, map[string]string{
		"xl/sharedStrings.xml": `<?xml version="1.0"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <si><t>市政工程</t></si>
  <si><r><t>C30</t></r><r><t>混凝土</t></r></si>
</sst>`,
	})

	got, err := FromFile(data, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "市政工程") || !strings.Contains(got, "混凝土") {
		t.Fatalf("extracted = %q", got)
	}
}

func TestFromFileSniffsDocxByMagicBytes(t *testing.T) {
	data := buildZip(t, map[string]string{
		"word/document.xml": `<w:document><w:p><w:t>需求文本</w:t></w:p></w:document>`,
	})
	// No MIME hint: the PK + word/document.xml signature must route to Docx.
	got, err := FromFile(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "需求文本") {
		t.Fatalf("extracted = %q", got)
	}
}

func TestFromFileRejectsPDF(t *testing.T) {
	_, err := FromFile([]byte("%PDF-1.7\nfake"), "application/pdf")
	if err != ErrUnsupported {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestFromFileReturnsImage(t *testing.T) {
	// Minimal PNG signature.
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	if _, err := FromFile(png, ""); err != ErrImage {
		t.Fatalf("err = %v, want ErrImage", err)
	}
	if _, err := FromFile(png, "image/png"); err != ErrImage {
		t.Fatalf("mime dispatch err = %v, want ErrImage", err)
	}
}

func TestFromFilePlainTextPassthrough(t *testing.T) {
	got, err := FromFile([]byte("需要 杭州 市政工程 供应商\n"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if got != "需要 杭州 市政工程 供应商\n" {
		t.Fatalf("got %q", got)
	}
}
