package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	vsmem "github.com/supplider/supplider/backend/internal/vectorstore/memory"
)

// TR-19-E: binary document uploads — .docx (local text extraction), images
// (LLM vision), and PDF (rejected). The extraction itself is unit-tested in
// internal/documentextract; here we pin the HTTP routing end to end.

func buildDocxZip(t *testing.T, bodyXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(bodyXML)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// postMultipartFile sends one multipart file field with an explicit content
// type, mirroring what the browser's FormData produces.
func postMultipartFile(t *testing.T, s *Server, filename, mime string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	hdr.Set("Content-Type", mime)
	fw, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/doc-search", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}

func TestDocSearchUploadsDocx(t *testing.T) {
	extract := `{"requirement":"杭州市政工程 商品混凝土","province":"浙江","city":"杭州","category":"市政工程","min_qual_level":""}`
	upstream := docSearchUpstream(t, extract)
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(docSearchGateway(t, upstream))
	s.WithVectorStore(vsmem.New())
	createFullSupplier(t, s, `{
		"basic_info":{"company_name":"杭州混凝土工程有限公司","region":{"province":"浙江","city":"杭州"}},
		"categories":["市政工程"],
		"qualifications":[{"type":"建筑工程施工总承包","level":"二级"}]
	}`)
	if w := do(t, s, http.MethodPost, "/api/v1/ai/index", ""); w.Code != http.StatusOK {
		t.Fatalf("index status %d body=%s", w.Code, w.Body.String())
	}

	docx := buildDocxZip(t, `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:t>杭州市政工程需要C30商品混凝土</w:t></w:p></w:body>
</w:document>`)

	rec := postMultipartFile(t, s, "requirement.docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document", docx)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var resp docSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Results[0].Name != "杭州混凝土工程有限公司" {
		t.Fatalf("results = %+v, want 杭州 supplier (docx text extracted → matched)", resp.Results)
	}
}

func TestDocSearchUploadsImage(t *testing.T) {
	extract := `{"requirement":"杭州市政工程","province":"浙江","city":"杭州","category":"市政工程","min_qual_level":""}`
	upstream := docSearchUpstream(t, extract)
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(docSearchGateway(t, upstream))
	s.WithVectorStore(vsmem.New())
	createFullSupplier(t, s, `{
		"basic_info":{"company_name":"杭州混凝土工程有限公司","region":{"province":"浙江","city":"杭州"}},
		"categories":["市政工程"]
	}`)
	if w := do(t, s, http.MethodPost, "/api/v1/ai/index", ""); w.Code != http.StatusOK {
		t.Fatalf("index status %d body=%s", w.Code, w.Body.String())
	}

	// Minimal PNG signature; the handler routes image/* to vision, not text.
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	rec := postMultipartFile(t, s, "requirement.png", "image/png", png)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var resp docSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected results from the image (vision) path")
	}
}

func TestDocSearchRejectsPDF(t *testing.T) {
	upstream := docSearchUpstream(t, "")
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(docSearchGateway(t, upstream))
	s.WithVectorStore(vsmem.New())

	rec := postMultipartFile(t, s, "requirement.pdf", "application/pdf", []byte("%PDF-1.7\nfake"))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415 (body=%s)", rec.Code, rec.Body.String())
	}
}
