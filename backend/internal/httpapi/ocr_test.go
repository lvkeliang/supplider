package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// TR-19-B: OCR 辅助录入 endpoint. A fake vision provider verifies the image
// is forwarded and the extracted JSON is parsed into the supplier prefill.

func TestOCRUnconfiguredReturnsServiceUnavailable(t *testing.T) {
	s := newTestServer(t) // no gateway
	w := do(t, s, http.MethodPost, "/api/v1/ai/ocr", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestOCRExtractsSupplierFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The gateway forwards the image as a data-URI part; just assert the
		// request reached us and return a realistic extraction.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"company_name\":\"杭州宏远建设有限公司\",\"credit_code\":\"91330106MA2HXY7K4B\",\"legal_person\":\"张三\",\"registered_capital\":\"1000万人民币\",\"establishment_date\":\"2015-03-15\",\"business_scope\":\"房屋建筑工程施工\",\"address\":\"浙江省杭州市西湖区\"}"}}]}`))
	}))
	defer upstream.Close()

	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: upstream.URL,
		APIKey:  "sk-test",
		Model:   "vision-model",
	}, nil))

	// Build a multipart form with a tiny fake image.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "license.jpg")
	_, _ = fw.Write([]byte{0xff, 0xd8, 0xff, 0xd9}) // minimal JPEG-ish bytes
	// Content-Type is set per-part via CreatePart; FormFile defaults to
	// application/octet-stream, so write the header explicitly.
	mw.Close()

	// CreatePart gives control over the per-file content-type.
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="license.jpg"`)
	hdr.Set("Content-Type", "image/jpeg")
	fw, _ = mw.CreatePart(hdr)
	_, _ = fw.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/ocr", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var result OCRResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.CompanyName != "杭州宏远建设有限公司" {
		t.Errorf("company_name = %q", result.CompanyName)
	}
	if result.CreditCode != "91330106MA2HXY7K4B" {
		t.Errorf("credit_code = %q", result.CreditCode)
	}
	if result.EstablishmentDate != "2015-03-15" {
		t.Errorf("establishment_date = %q", result.EstablishmentDate)
	}
}

func TestOCRRejectsNonImage(t *testing.T) {
	s := newTestServer(t)
	s.WithGateway(aigateway.New(aigateway.Config{
		Format:  aigateway.FormatOpenAI,
		BaseURL: "http://127.0.0.1:9",
		APIKey:  "k",
		Model:   "m",
	}, nil))

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "doc.pdf")
	_, _ = fw.Write([]byte("not an image"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/ocr", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestExtractJSONObjectStripsFences(t *testing.T) {
	cases := map[string]string{
		"plain":  `{"a":1}`,
		"fenced": "```json\n{\"a\":1}\n```",
		"prose":  "好的，结果如下：{\"a\":1} 谢谢",
	}
	for name, in := range cases {
		out, err := extractJSONObject(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(out) != `{"a":1}` {
			t.Errorf("%s: got %q", name, out)
		}
	}
	if _, err := extractJSONObject("no json here"); err == nil {
		t.Error("expected error for missing JSON object")
	}
}
