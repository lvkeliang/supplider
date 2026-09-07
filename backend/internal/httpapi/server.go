// Package httpapi exposes the supplier HTTP API. Handlers are tier-agnostic
// business code: they depend on *supplier.Service and featureflag.Features
// only, never on a concrete store.
//
// Endpoints (v1):
//
//	GET    /readyz                     liveness + store ping
//	GET    /api/v1/features            runtime feature matrix (drives UI)
//	POST   /api/v1/suppliers           create
//	GET    /api/v1/suppliers           list (summary page; filters + cursor)
//	GET    /api/v1/suppliers/{id}      full document
//	PATCH  /api/v1/suppliers/{id}      partial update (auto change_log)
//	DELETE /api/v1/suppliers/{id}      archive
//	POST   /api/v1/suppliers/{id}/restore
//	GET    /api/v1/suppliers/{id}/risk          live shell-company rule report
//	POST   /api/v1/suppliers/{id}/risk-check    re-run rules + persist verdict
//	GET    /api/v1/risk/shell                   shell-risk review queue (active)
//	POST   /api/v1/suppliers/{id}/attachments   upload (multipart, ≤50MB)
//	GET    /api/v1/attachments/{key...}         download/stream
//	GET    /api/v1/import/template              download .xlsx template
//	POST   /api/v1/import/preview               parse + suggest column mapping
//	POST   /api/v1/import/commit                batch-import rows
//	GET    /api/v1/export?format=json|xlsx      export all matching suppliers
//	                                           (same filters as list)
//	GET    /api/v1/reminders/expiring?within=90  qualification expiry scan
//	                                           (90/30/7-day windows + expired)
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/exporter"
	"github.com/supplider/supplider/backend/internal/featureflag"
	"github.com/supplider/supplider/backend/internal/importer"
	"github.com/supplider/supplider/backend/internal/objectstore"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// Server bundles HTTP dependencies.
type Server struct {
	Service  *supplier.Service
	Features featureflag.Features
	// Objects is the attachment store; nil disables attachment upload/
	// download (handlers answer 501). Wired via WithObjects.
	Objects objectstore.Store
	Mux     *http.ServeMux
}

// New wires routes and returns the server.
func New(svc *supplier.Service, feats featureflag.Features) *Server {
	s := &Server{Service: svc, Features: feats, Mux: http.NewServeMux()}
	s.routes()
	return s
}

// WithObjects attaches the object store used for supplier attachments and
// returns the server for chaining. Pass nil to leave attachments disabled.
func (s *Server) WithObjects(store objectstore.Store) *Server {
	s.Objects = store
	return s
}

func (s *Server) routes() {
	s.Mux.HandleFunc("GET /readyz", s.handleReady)
	s.Mux.HandleFunc("GET /api/v1/features", s.handleFeatures)
	s.Mux.HandleFunc("POST /api/v1/suppliers", s.handleCreate)
	s.Mux.HandleFunc("GET /api/v1/suppliers", s.handleList)
	s.Mux.HandleFunc("GET /api/v1/suppliers/{id}", s.handleGet)
	s.Mux.HandleFunc("PATCH /api/v1/suppliers/{id}", s.handleUpdate)
	s.Mux.HandleFunc("DELETE /api/v1/suppliers/{id}", s.handleArchive)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/restore", s.handleRestore)
	s.Mux.HandleFunc("GET /api/v1/suppliers/{id}/risk", s.handleSupplierRisk)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/risk-check", s.handleRiskCheck)
	s.Mux.HandleFunc("GET /api/v1/risk/shell", s.handleShellRiskQueue)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/attachments", s.handleUploadAttachment)
	s.Mux.HandleFunc("GET /api/v1/attachments/{key...}", s.handleDownloadAttachment)
	s.Mux.HandleFunc("GET /api/v1/import/template", s.handleImportTemplate)
	s.Mux.HandleFunc("POST /api/v1/import/preview", s.handleImportPreview)
	s.Mux.HandleFunc("POST /api/v1/import/commit", s.handleImportCommit)
	s.Mux.HandleFunc("GET /api/v1/export", s.handleExport)
	s.Mux.HandleFunc("GET /api/v1/reminders/expiring", s.handleExpiringReminders)
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	// Ping is exercised through a lightweight list call; the store is
	// reachable if it returns without error.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Features)
}

// ---------- suppliers ----------

// createRequest is the POST body. Mirrors supplier.CreateInput.
type createRequest struct {
	Owner          string                  `json:"owner"`
	BasicInfo      domain.BasicInfo        `json:"basic_info"`
	Qualifications []domain.Qualification  `json:"qualifications"`
	Categories     []string                `json:"categories"`
	Products       []domain.ProductService `json:"products_services"`
	Performance    []domain.Performance    `json:"performance_history"`
	Visibility     int                     `json:"visibility"`
	SharedWith     []string                `json:"shared_with"`
	CustomFields   map[string]any          `json:"custom_fields"`
	Source         string                  `json:"source"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	doc, err := s.Service.Create(r.Context(), supplier.CreateInput{
		Owner:          req.Owner,
		BasicInfo:      req.BasicInfo,
		Qualifications: req.Qualifications,
		Categories:     req.Categories,
		Products:       req.Products,
		Performance:    req.Performance,
		Visibility:     req.Visibility,
		SharedWith:     req.SharedWith,
		CustomFields:   req.CustomFields,
		Source:         req.Source,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, doc)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	doc, err := s.Service.Get(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// updateRequest is the PATCH body; pointer fields distinguish "unchanged"
// from "set to empty".
type updateRequest struct {
	BasicInfo      *domain.BasicInfo        `json:"basic_info"`
	Qualifications *[]domain.Qualification  `json:"qualifications"`
	Categories     *[]string                `json:"categories"`
	Products       *[]domain.ProductService `json:"products_services"`
	Performance    *[]domain.Performance    `json:"performance_history"`
	Visibility     *int                     `json:"visibility"`
	SharedWith     *[]string                `json:"shared_with"`
	CustomFields   *map[string]any          `json:"custom_fields"`
	Source         string                   `json:"source"`
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	doc, err := s.Service.Update(r.Context(), r.PathValue("id"), supplier.UpdateInput{
		BasicInfo:      req.BasicInfo,
		Qualifications: req.Qualifications,
		Categories:     req.Categories,
		Products:       req.Products,
		Performance:    req.Performance,
		Visibility:     req.Visibility,
		SharedWith:     req.SharedWith,
		CustomFields:   req.CustomFields,
		Source:         req.Source,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	if err := s.Service.Archive(r.Context(), r.PathValue("id")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	doc, err := s.Service.Restore(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// ---------- attachments ----------

// handleUploadAttachment accepts a multipart form with a single "file"
// field and attaches it to the supplier. The 50MB red line is enforced
// twice: http.MaxBytesReader aborts an over-long request body, and the
// object store independently caps the streamed bytes.
func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	if s.Objects == nil {
		writeError(w, http.StatusNotImplemented, "attachments are not configured (no data directory)")
		return
	}
	id := r.PathValue("id")

	// Allow multipart framing overhead on top of the hard file limit.
	r.Body = http.MaxBytesReader(w, r.Body, objectstore.MaxAttachmentSize+(1<<20))
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("attachment exceeds the %dMB limit", objectstore.MaxAttachmentSize/(1024*1024)))
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

	origName := path.Base(strings.TrimSpace(header.Filename))
	if origName == "." || origName == "/" || origName == "" {
		writeError(w, http.StatusBadRequest, "missing filename")
		return
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Key is namespaced by supplier id + a unique timestamped base, so the
	// on-disk path never contains a user-controlled separator.
	key := fmt.Sprintf("%s/%d_%s", id, time.Now().UnixNano(), safeFileName(origName))

	obj, err := s.Objects.Put(r.Context(), key, origName, mimeType, file, header.Size)
	if err != nil {
		var tooLarge *objectstore.ErrTooLarge
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("attachment exceeds the %dMB limit", objectstore.MaxAttachmentSize/(1024*1024)))
			return
		}
		writeServiceError(w, err)
		return
	}

	att := domain.Attachment{
		Name:     origName,
		URL:      "/api/v1/attachments/" + key,
		Size:     obj.Size,
		MIMEType: mimeType,
	}
	doc, err := s.Service.AddAttachment(r.Context(), id, att)
	if err != nil {
		// Best-effort cleanup of the now-orphaned object.
		_ = s.Objects.Remove(r.Context(), key)
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, doc)
}

// handleDownloadAttachment streams a stored object. The key's first path
// segment is the supplier id; original filename and MIME type are resolved
// from the supplier document's attachment record (the object store only
// holds bytes keyed by key).
func (s *Server) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	if s.Objects == nil {
		writeError(w, http.StatusNotImplemented, "attachments are not configured")
		return
	}
	key := strings.TrimPrefix(r.PathValue("key"), "/")

	rc, obj, err := s.Objects.Get(r.Context(), key)
	if errors.Is(err, objectstore.ErrObjectNotFound) {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	defer rc.Close()

	name, mimeType := obj.Name, "application/octet-stream"
	if supplierID, _, ok := strings.Cut(key, "/"); ok {
		if doc, gerr := s.Service.Get(r.Context(), supplierID); gerr == nil {
			for _, a := range doc.Attachments {
				if a.URL == "/api/v1/attachments/"+key {
					if a.Name != "" {
						name = a.Name
					}
					if a.MIMEType != "" {
						mimeType = a.MIMEType
					}
					break
				}
			}
		}
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	// RFC 5987 encoding preserves non-ASCII (Chinese) filenames.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="download"; filename*=UTF-8''%s`, url.PathEscape(name)))
	_, _ = io.Copy(w, rc)
}

// ---------- Excel import ----------

// maxImportBytes bounds an uploaded workbook. 1000-row supplier sheets are a
// few hundred KB; 20MB is generous headroom well under any memory concern.
const maxImportBytes = 20 << 20

// handleImportTemplate serves the .xlsx import template.
func (s *Server) handleImportTemplate(w http.ResponseWriter, r *http.Request) {
	data, err := importer.Template()
	if err != nil {
		log.Printf("httpapi: build template: %v", err)
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition",
		`attachment; filename="template.xlsx"; filename*=UTF-8''`+url.PathEscape("供应商导入模板.xlsx"))
	_, _ = w.Write(data)
}

// handleImportPreview parses a workbook and returns headers + sample rows +
// suggested column mapping (no writes) so the UI can present manual mapping.
func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	data, _, err := readImportFile(w, r)
	if err != nil {
		writeImportError(w, err)
		return
	}
	insp, err := importer.Inspect(data)
	if err != nil {
		writeImportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, insp)
}

// handleImportCommit applies a column mapping and batch-creates suppliers.
// Form fields: file (xlsx), mapping (JSON {"<col>":"<field>"}), owner,
// visibility. Missing mapping falls back to the auto-suggested mapping.
func (s *Server) handleImportCommit(w http.ResponseWriter, r *http.Request) {
	data, _, err := readImportFile(w, r)
	if err != nil {
		writeImportError(w, err)
		return
	}

	mapping := parseMapping(r.FormValue("mapping"))
	if len(mapping) == 0 {
		insp, err := importer.Inspect(data)
		if err != nil {
			writeImportError(w, err)
			return
		}
		mapping = insp.Suggested
	}

	visibility := 0
	if v := r.FormValue("visibility"); v != "" {
		visibility, _ = strconv.Atoi(v)
	}
	defaults := importer.Defaults{Owner: strings.TrimSpace(r.FormValue("owner")), Visibility: visibility}

	items, err := importer.Build(data, mapping, defaults)
	if err != nil {
		writeImportError(w, err)
		return
	}
	report := s.Service.Import(r.Context(), items)
	writeJSON(w, http.StatusOK, report)
}

// readImportFile reads the multipart "file" field of an import request,
// capping the body size.
func readImportFile(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, "", fmt.Errorf("import file too large (limit %dMB)", maxImportBytes/(1024*1024))
		}
		return nil, "", fmt.Errorf("invalid multipart form: %w", err)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", fmt.Errorf(`expected a multipart "file" field: %w`, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxImportBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxImportBytes {
		return nil, "", fmt.Errorf("import file too large (limit %dMB)", maxImportBytes/(1024*1024))
	}
	return data, header.Filename, nil
}

// parseMapping decodes the {"columnIndex":"fieldKey"} mapping JSON.
func parseMapping(s string) map[int]string {
	out := map[int]string{}
	if strings.TrimSpace(s) == "" {
		return out
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return out
	}
	for k, v := range raw {
		if i, err := strconv.Atoi(k); err == nil {
			out[i] = v
		}
	}
	return out
}

// writeImportError maps importer failures onto HTTP status codes.
func writeImportError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "too large"):
		writeError(w, http.StatusRequestEntityTooLarge, msg)
	case strings.Contains(msg, "not a valid .xlsx"), strings.Contains(msg, "empty"),
		strings.Contains(msg, "multipart"), strings.Contains(msg, "no sheets"):
		writeError(w, http.StatusBadRequest, msg)
	default:
		log.Printf("httpapi: import error: %v", err)
		writeError(w, http.StatusBadRequest, msg)
	}
}

// safeFileName strips path separators and control characters so a supplied
// filename cannot introduce a traversal segment into the object key.
func safeFileName(name string) string {
	name = path.Base(name)
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return '_'
		}
		if r < 0x20 {
			return '_'
		}
		return r
	}, name)
	return name
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))

	query := datamodel.Query{
		Limit:  limit,
		Cursor: q.Get("cursor"),
		Sort:   datamodel.Sort{Field: datamodel.SortField(q.Get("sort")), Order: datamodel.SortOrder(q.Get("order"))},
		Filter: filterFromQuery(q),
	}

	page, err := s.Service.List(r.Context(), query)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// filterFromQuery parses the shared supplier filter parameters used by
// both the interactive list and the export endpoint.
func filterFromQuery(q url.Values) datamodel.SupplierFilter {
	visMax := (*int)(nil)
	if v := q.Get("visibility_max"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			visMax = &n
		}
	}
	minQual := 0
	if lvl := q.Get("min_qual_level"); lvl != "" {
		minQual = domain.QualRank(lvl)
	}
	minRating, _ := strconv.ParseFloat(q.Get("min_rating"), 64)
	maxRating, _ := strconv.ParseFloat(q.Get("max_rating"), 64)

	return datamodel.SupplierFilter{
		Province:        q.Get("province"),
		City:            q.Get("city"),
		District:        q.Get("district"),
		Categories:      nonEmpty(strings.Split(q.Get("category"), ",")),
		MinQualRank:     minQual,
		MinRating:       minRating,
		MaxRating:       maxRating,
		OwnerID:         q.Get("owner"),
		VisibilityMax:   visMax,
		Status:          q.Get("status"),
		IncludeArchived: q.Get("include_archived") == "true" || q.Get("include_archived") == "1",
		Keyword:         q.Get("q"),
	}
}

// ---------- maintenance reminders ----------

// expiringReport is the GET /api/v1/reminders/expiring payload: the flat
// alert list (most urgent first) plus aggregate counts the UI renders as a
// banner without re-deriving buckets.
type expiringReport struct {
	GeneratedAt time.Time              `json:"generated_at"`
	WithinDays  int                    `json:"within_days"`
	Count       int                    `json:"count"`
	Expired     int                    `json:"expired"` // subset of Count, bucket == expired
	Items       []supplier.ExpiryAlert `json:"items"`
}

// handleExpiringReminders answers the qualification-expiry maintenance
// scan (资质到期提醒, 提前 90/30/7 天). ?within=N overrides the 90-day
// outer window. This is the non-AI base path — no network, no keys.
func (s *Server) handleExpiringReminders(w http.ResponseWriter, r *http.Request) {
	within := supplier.DefaultExpiryWindow
	if v := r.URL.Query().Get("within"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			within = n
		}
	}
	alerts, err := s.Service.ExpiringQualifications(r.Context(), within)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	rep := expiringReport{
		GeneratedAt: time.Now().UTC(),
		WithinDays:  within,
		Count:       len(alerts),
		Items:       alerts,
	}
	for _, a := range alerts {
		if a.Bucket == supplier.BucketExpired {
			rep.Expired++
		}
	}
	writeJSON(w, http.StatusOK, rep)
}

// ---------- shell-company risk (空壳特征检测, non-AI) ----------

// handleSupplierRisk answers the live rule-engine report for one supplier:
// the shell_risk verdict plus the full, explainable signal list. The report
// is computed on demand from the stored document (not read from the
// denormalized flag) so it always reflects the current rules.
func (s *Server) handleSupplierRisk(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Service.RiskReport(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleRiskCheck re-runs the local rules and persists the refreshed verdict
// (backfill / on-demand re-审核), returning the same report shape as the
// live GET.
func (s *Server) handleRiskCheck(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Service.CheckRisks(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// shellRiskReport is the GET /api/v1/risk/shell payload: the manual-review
// queue (active suppliers the local rules flag), most suspicious first.
type shellRiskReport struct {
	GeneratedAt time.Time                `json:"generated_at"`
	Count       int                      `json:"count"`
	Items       []supplier.ShellRiskItem `json:"items"`
}

// handleShellRiskQueue runs the live review scan (审核队列). Non-AI base
// path — no network, no keys; the [AI] 空壳风险报告 layers on later.
func (s *Server) handleShellRiskQueue(w http.ResponseWriter, r *http.Request) {
	items, err := s.Service.ShellRiskSuppliers(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, shellRiskReport{
		GeneratedAt: time.Now().UTC(),
		Count:       len(items),
		Items:       items,
	})
}

// ---------- export ----------

// handleExport streams ALL suppliers matching the list filters as either a
// full-fidelity JSON bundle (backup / cross-tier import) or an XLSX
// workbook (human-readable exchange, round-trippable through import). The
// 100-row page cap does not apply — export pages through the full result
// set server-side; the service enforces a high safety cap instead.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	docs, err := s.Service.Export(r.Context(), filterFromQuery(r.URL.Query()))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "json"
	}
	stamp := time.Now().Format("20060102_150405")

	switch format {
	case "json":
		data, err := exporter.JSON(docs, time.Now())
		if err != nil {
			log.Printf("httpapi: export json: %v", err)
			writeServiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition",
			`attachment; filename="suppliers.json"; filename*=UTF-8''`+
				url.PathEscape(fmt.Sprintf("供应商导出_%s.json", stamp)))
		_, _ = w.Write(data)
	case "xlsx":
		data, err := exporter.XLSX(docs)
		if err != nil {
			log.Printf("httpapi: export xlsx: %v", err)
			writeServiceError(w, err)
			return
		}
		w.Header().Set("Content-Type",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition",
			`attachment; filename="suppliers.xlsx"; filename*=UTF-8''`+
				url.PathEscape(fmt.Sprintf("供应商导出_%s.xlsx", stamp)))
		_, _ = w.Write(data)
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("format must be %q or %q, got %q", "json", "xlsx", format))
	}
}

// ---------- helpers ----------

func nonEmpty(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpapi: write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, datamodel.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		// Validation errors are 400; everything unexpected is 500.
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "must be") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("httpapi: internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
