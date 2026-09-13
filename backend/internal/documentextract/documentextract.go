// Package documentextract turns an uploaded requirement document into plain
// text so the doc-search pipeline (LLM requirement extraction → semantic
// match) can consume it. It is the zero-dependency personal-tier extraction
// layer: Word .docx and Excel .xlsx are ZIP+XML, parsed with the stdlib only;
// plain text/markdown/csv pass through. Images are NOT parsed locally — the
// caller routes them to LLM vision (the OCR path). PDF stays unsupported
// (paste the text) until a pure-Go PDF reader is justified.
package documentextract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrUnsupported is returned for a binary document the personal tier does not
// parse (notably PDF). The caller tells the user to paste the text instead.
var ErrUnsupported = errors.New("documentextract: unsupported document type")

// ErrImage is returned when the bytes are a raster image; the caller routes
// them to the LLM vision extractor (no local text extraction is possible).
var ErrImage = errors.New("documentextract: image (route to vision)")

// FromFile dispatches on the MIME hint (or magic bytes when empty) to the
// right extractor and returns the document's plain text.
func FromFile(data []byte, mime string) (string, error) {
	mime = strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.HasPrefix(mime, "text/"):
		return string(data), nil
	case mime == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return Docx(data)
	case mime == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return Xlsx(data)
	case strings.HasPrefix(mime, "image/"):
		return "", ErrImage
	case mime == "application/pdf":
		return "", ErrUnsupported
	}

	// No usable MIME: sniff magic bytes.
	if isImage(data) {
		return "", ErrImage
	}
	if bytes.HasPrefix(data, []byte("%PDF")) {
		return "", ErrUnsupported
	}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		// A ZIP archive: docx carries word/document.xml, xlsx carries xl/.
		switch {
		case bytes.Contains(data, []byte("word/document.xml")):
			return Docx(data)
		case bytes.Contains(data, []byte("xl/")):
			return Xlsx(data)
		}
		return "", ErrUnsupported
	}
	// Fallback: treat as plain text, but reject NUL-laden binaries.
	if idx := bytes.IndexByte(data, 0); idx >= 0 && idx < 512 {
		return "", ErrUnsupported
	}
	return string(data), nil
}

// Docx extracts the plain text of a Word .docx (WordprocessingML). The body
// text lives in word/document.xml as <w:t> runs inside <w:p> paragraphs.
func Docx(data []byte) (string, error) {
	doc, err := zipEntry(data, "word/document.xml")
	if err != nil {
		return "", err
	}
	return collectTText(doc, "p"), nil
}

// Xlsx extracts text from an Excel .xlsx (SpreadsheetML). Requirement docs put
// their content in the shared-string table (xl/sharedStrings.xml); a
// shared-string-less workbook falls back to concatenating worksheet inline
// strings. The goal is a readable blob for the LLM, not a faithful
// spreadsheet reconstruction.
func Xlsx(data []byte) (string, error) {
	if shared, err := zipEntry(data, "xl/sharedStrings.xml"); err == nil {
		return collectTText(shared, "si"), nil
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("documentextract: open xlsx zip: %w", err)
	}
	var b strings.Builder
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			continue
		}
		b.WriteString(collectTText(raw, "row"))
	}
	return b.String(), nil
}

// zipEntry returns the bytes of one named entry in a ZIP archive.
func zipEntry(data []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("documentextract: open zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("documentextract: open %q: %w", name, err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("documentextract: entry %q not found", name)
}

// collectTText streams an XML document and concatenates the character data of
// every <t> element, emitting a newline after each </breakLocal> element
// (paragraphs in Word, shared-string items / rows in Excel). It is a
// namespace-agnostic extraction: in WordprocessingML/SpreadsheetML the only
// <t> elements are text runs.
func collectTText(xmlBytes []byte, breakLocal string) string {
	dec := xml.NewDecoder(bytes.NewReader(xmlBytes))
	var b strings.Builder
	inT := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inT = true
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
			} else if t.Name.Local == breakLocal {
				b.WriteByte('\n')
			}
		case xml.CharData:
			if inT {
				b.Write(t)
			}
		}
	}
	return b.String()
}

func isImage(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) || // JPEG
		bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) || // PNG
		bytes.HasPrefix(data, []byte("GIF8")) || // GIF
		bytes.HasPrefix(data, []byte("BM")) || // BMP
		(len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))) // WEBP
}
