package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// OCR 辅助录入 (TR-19-B): upload a business-license/qualification photo, the
// configured vision model extracts supplier fields, and the UI prefills the
// manual form. It is an ENHANCEMENT — the manual form + Excel import are the
// non-AI path (no key → the entry point is hidden and this endpoint is
// defensive-only).

// maxOCRBytes bounds an uploaded photo. A 10MP JPEG is a few MB; 10MB is
// generous and far below any base64/memory concern.
const maxOCRBytes = 10 << 20

// OCRResult is the extracted supplier prefill. Field names mirror the manual
// form so the frontend maps them 1:1. Empty string = not found in the image.
type OCRResult struct {
	CompanyName       string `json:"company_name"`
	CreditCode        string `json:"credit_code"`
	LegalPerson       string `json:"legal_person"`
	RegisteredCapital string `json:"registered_capital"`
	EstablishmentDate string `json:"establishment_date"`
	BusinessScope     string `json:"business_scope"`
	Address           string `json:"address"`
}

const ocrPrompt = `你是企业资质 OCR 助手。从这张营业执照或资质证书图片中提取以下字段，只返回一个 JSON 对象（不要 Markdown 代码块、不要解释）。找不到的字段用空字符串。字段：
- company_name 公司名称
- credit_code 统一社会信用代码（18 位）
- legal_person 法定代表人
- registered_capital 注册资本（如 "1000万人民币"）
- establishment_date 成立日期（YYYY-MM-DD，若图片为"2005年3月15日"请转为 2005-03-15）
- business_scope 经营范围
- address 注册地址`

// handleOCR runs the vision extraction against the configured provider.
func (s *Server) handleOCR(w http.ResponseWriter, r *http.Request) {
	gw := s.liveGateway()
	if gw == nil || !gw.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "AI 模型未配置，请先在设置中配置模型")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxOCRBytes+(1<<20))
	if err := r.ParseMultipartForm(maxOCRBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("图片超过 %dMB 限制", maxOCRBytes/(1024*1024)))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, `expected a multipart "file" field: `+err.Error())
		return
	}
	defer file.Close()

	mime := header.Header.Get("Content-Type")
	if !strings.HasPrefix(mime, "image/") {
		writeError(w, http.StatusBadRequest, "请上传图片文件（营业执照/资质证书照片）")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(file, maxOCRBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read image: "+err.Error())
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "图片为空")
		return
	}

	resp, err := gw.Complete(r.Context(), aigateway.ChatRequest{
		Task: "ocr",
		Messages: []aigateway.ChatMessage{{
			Role:        "user",
			Content:     ocrPrompt,
			ImageBase64: base64.StdEncoding.EncodeToString(raw),
			ImageMIME:   mime,
		}},
		JSONMode: true,
	})
	if err != nil {
		if errors.Is(err, aigateway.ErrAIDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AI 模型未配置")
			return
		}
		writeError(w, http.StatusBadGateway, "OCR 失败："+err.Error())
		return
	}

	body, err := extractJSONObject(resp.Text)
	if err != nil {
		writeError(w, http.StatusBadGateway, "模型未返回有效 JSON："+err.Error())
		return
	}
	var result OCRResult
	if err := json.Unmarshal(body, &result); err != nil {
		writeError(w, http.StatusBadGateway, "模型返回无法解析："+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// extractJSONObject returns the first {...} object in a model reply, stripping
// markdown fences and leading prose that some providers add despite the
// "JSON only" instruction.
func extractJSONObject(text string) ([]byte, error) {
	s := strings.TrimSpace(text)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in response")
	}
	return []byte(s[start : end+1]), nil
}
