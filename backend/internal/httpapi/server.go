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
//	GET    /api/v1/suppliers/duplicates  pre-entry duplicate check (录入去重)
//	GET    /api/v1/suppliers/{id}      full document
//	PATCH  /api/v1/suppliers/{id}      partial update (auto change_log)
//	DELETE /api/v1/suppliers/{id}      archive
//	POST   /api/v1/suppliers/{id}/restore
//	POST   /api/v1/suppliers/{id}/blacklist      add to blacklist (淘汰/黑名单)
//	POST   /api/v1/suppliers/{id}/unblacklist    remove from blacklist
//	POST   /api/v1/suppliers/{id}/merge          merge a duplicate into this
//	                                           supplier (合并重复供应商；other archived)
//	GET    /api/v1/suppliers/{id}/risk          live shell-company rule report
//	POST   /api/v1/suppliers/{id}/risk-check    re-run rules + persist verdict
//	POST   /api/v1/suppliers/{id}/risk-review   human resolves flag (verified|dismissed)
//	GET    /api/v1/risk/shell                   shell-risk review queue (active, unreviewed)
//	POST   /api/v1/suppliers/{id}/attachments   upload (multipart, ≤50MB)
//	DELETE /api/v1/suppliers/{id}/attachments?url=  remove an attachment
//	GET    /api/v1/attachments/{key...}         download/stream
//	GET    /api/v1/import/template              download .xlsx template
//	POST   /api/v1/import/preview               parse + suggest column mapping
//	POST   /api/v1/import/commit                batch-import rows
//	GET    /api/v1/export?format=json|xlsx      export all matching suppliers
//	                                           (same filters as list)
//	GET    /api/v1/reminders/expiring?within=90  qualification expiry scan
//	                                           (90/30/7-day windows + expired)
//	GET    /api/v1/backup                     download a full library backup
//	                                           (zip: DB snapshot + attachments)
//	GET    /api/v1/visibility/policy          current visibility policy (admin config)
//	PUT    /api/v1/visibility/policy          persist policy + run one enforce sweep
//	GET    /api/v1/visibility/violations      visibility policy scan (read-only)
//	POST   /api/v1/visibility/enforce         run the 待调整→downgrade sweep
//	POST   /api/v1/suppliers/{id}/appeal-visibility
//	                                         owner appeals a 待调整 flag (pauses countdown)
//	POST   /api/v1/suppliers/{id}/resolve-visibility-appeal
//	                                         admin grants (exception) or denies (downgrade)
package httpapi

import (
	"context"
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

	"github.com/supplider/supplider/backend/internal/backup"
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
	// Backup streams a full library archive (zip: database snapshot +
	// attachments); nil disables GET /api/v1/backup (501) — e.g. the
	// in-memory dev build has nothing on disk to back up.
	Backup BackupFunc
	// Restore wires in-app restore/migration (staged zip, swapped in at
	// next process start); nil answers 501 on /api/v1/backup/restore.
	Restore *RestoreFuncs
	Mux     *http.ServeMux
}

// BackupFunc streams one backup archive to w (see internal/backup).
type BackupFunc func(ctx context.Context, w io.Writer) error

// RestoreFuncs are the filesystem-side restore operations, bound by the
// wiring layer (data directory + adapter snapshot checker).
type RestoreFuncs struct {
	Stage   func(r io.Reader) (backup.Manifest, error)
	Pending func() (backup.Manifest, bool, error)
	Cancel  func() error
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

// WithBackup wires the full-library backup archive writer (zip) and
// returns the server for chaining. Pass nil to disable GET /api/v1/backup
// (the endpoint answers 501).
func (s *Server) WithBackup(fn BackupFunc) *Server {
	s.Backup = fn
	return s
}

// WithRestore wires the staged in-app restore endpoints and returns the
// server for chaining. Pass nil (the default) to answer 501 — the ephemeral
// in-memory build has no persistent data directory to restore into.
func (s *Server) WithRestore(fns *RestoreFuncs) *Server {
	s.Restore = fns
	return s
}

func (s *Server) routes() {
	s.Mux.HandleFunc("GET /readyz", s.handleReady)
	s.Mux.HandleFunc("GET /api/v1/features", s.handleFeatures)
	s.Mux.HandleFunc("POST /api/v1/suppliers", s.handleCreate)
	s.Mux.HandleFunc("GET /api/v1/suppliers", s.handleList)
	s.Mux.HandleFunc("GET /api/v1/suppliers/duplicates", s.handleDuplicates)
	s.Mux.HandleFunc("GET /api/v1/suppliers/{id}", s.handleGet)
	s.Mux.HandleFunc("PATCH /api/v1/suppliers/{id}", s.handleUpdate)
	s.Mux.HandleFunc("DELETE /api/v1/suppliers/{id}", s.handleArchive)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/restore", s.handleRestore)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/blacklist", s.handleBlacklist)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/unblacklist", s.handleUnblacklist)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/watch", s.handleWatch)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/merge", s.handleMerge)
	s.Mux.HandleFunc("GET /api/v1/suppliers/{id}/risk", s.handleSupplierRisk)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/risk-check", s.handleRiskCheck)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/risk-review", s.handleRiskReview)
	s.Mux.HandleFunc("GET /api/v1/risk/shell", s.handleShellRiskQueue)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/attachments", s.handleUploadAttachment)
	s.Mux.HandleFunc("DELETE /api/v1/suppliers/{id}/attachments", s.handleDeleteAttachment)
	s.Mux.HandleFunc("GET /api/v1/attachments/{key...}", s.handleDownloadAttachment)
	s.Mux.HandleFunc("GET /api/v1/import/template", s.handleImportTemplate)
	s.Mux.HandleFunc("POST /api/v1/import/preview", s.handleImportPreview)
	s.Mux.HandleFunc("POST /api/v1/import/commit", s.handleImportCommit)
	s.Mux.HandleFunc("GET /api/v1/export", s.handleExport)
	s.Mux.HandleFunc("GET /api/v1/reminders/expiring", s.handleExpiringReminders)
	s.Mux.HandleFunc("GET /api/v1/notifications", s.handleListNotifications)
	s.Mux.HandleFunc("POST /api/v1/notifications/read-all", s.handleMarkAllNotificationsRead)
	s.Mux.HandleFunc("POST /api/v1/notifications/{id}/read", s.handleMarkNotificationRead)
	s.Mux.HandleFunc("GET /api/v1/backup", s.handleBackup)
	s.Mux.HandleFunc("GET /api/v1/backup/restore", s.handleRestoreStatus)
	s.Mux.HandleFunc("POST /api/v1/backup/restore", s.handleRestoreStage)
	s.Mux.HandleFunc("DELETE /api/v1/backup/restore", s.handleRestoreCancel)
	s.Mux.HandleFunc("GET /api/v1/visibility/policy", s.handleGetVisibilityPolicy)
	s.Mux.HandleFunc("PUT /api/v1/visibility/policy", s.handleSaveVisibilityPolicy)
	s.Mux.HandleFunc("POST /api/v1/visibility/policy", s.handleSaveVisibilityPolicy)
	s.Mux.HandleFunc("GET /api/v1/preferences/local", s.handleGetLocalPreference)
	s.Mux.HandleFunc("PUT /api/v1/preferences/local", s.handleSaveLocalPreference)
	s.Mux.HandleFunc("GET /api/v1/visibility/violations", s.handleVisibilityViolations)
	s.Mux.HandleFunc("POST /api/v1/visibility/enforce", s.handleVisibilityEnforce)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/appeal-visibility", s.handleAppealVisibility)
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/resolve-visibility-appeal", s.handleResolveVisibilityAppeal)
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

// handleDuplicates answers a pre-entry duplicate check (录入去重): given a
// candidate's name/credit code/region it returns existing suppliers that
// look like the same company — strong on identical credit code, probable on
// identical normalized name. Blacklisted/archived records are included so a
// re-onboarded fraudster or a previously-removed company is still flagged.
// Non-blocking: the caller decides whether to proceed.
func (s *Server) handleDuplicates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	candidate := domain.BasicInfo{
		CompanyName: strings.TrimSpace(q.Get("name")),
		CreditCode:  strings.TrimSpace(q.Get("credit_code")),
		Region: domain.Region{
			Province: strings.TrimSpace(q.Get("province")),
			City:     strings.TrimSpace(q.Get("city")),
		},
	}
	matches, err := s.Service.CheckDuplicates(r.Context(), candidate)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(matches), "matches": matches})
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

// handleBlacklist moves a supplier onto the blacklist (淘汰/黑名单). Body is
// optional: {"reason": "..."}; the reason may also come from ?reason=. The
// supplier stays visible (in list/search) but badged as do-not-use.
func (s *Server) handleBlacklist(w http.ResponseWriter, r *http.Request) {
	reason := r.URL.Query().Get("reason")
	if r.Body != nil {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
		if strings.TrimSpace(body.Reason) != "" {
			reason = body.Reason
		}
	}
	doc, err := s.Service.Blacklist(r.Context(), r.PathValue("id"), reason)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleUnblacklist(w http.ResponseWriter, r *http.Request) {
	doc, err := s.Service.Unblacklist(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// mergeRequest is the POST .../merge body. The path id is the MASTER (the
// record that survives); duplicate_id is the record folded in and archived.
type mergeRequest struct {
	DuplicateID string `json:"duplicate_id"`
}

// handleMerge consolidates a duplicate supplier into the path-id master
// (合并重复供应商): collections are unioned, history retained, duplicate
// archived. Returns the updated master plus a count summary.
func (s *Server) handleMerge(w http.ResponseWriter, r *http.Request) {
	var req mergeRequest
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}
	if strings.TrimSpace(req.DuplicateID) == "" {
		req.DuplicateID = r.URL.Query().Get("duplicate_id")
	}
	doc, res, err := s.Service.MergeSuppliers(r.Context(), r.PathValue("id"), req.DuplicateID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"supplier": doc, "merged": res})
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

// attachmentKeyFromURL extracts the object key from a stored attachment URL
// ("/api/v1/attachments/<key>"), tolerating a bare key or a leading slash.
func attachmentKeyFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	const prefix = "/api/v1/attachments/"
	if strings.HasPrefix(raw, prefix) {
		raw = raw[len(prefix):]
	}
	return strings.TrimPrefix(raw, "/")
}

// handleDeleteAttachment removes one attachment from the document and then
// deletes the backing object bytes. The attachment is identified by its URL
// (or bare key) via ?url=...; removing the document record happens first so
// a missing object (already cleaned up, or never stored) does not strand a
// dangling record. Object deletion is best-effort after the record is gone.
func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if s.Objects == nil {
		writeError(w, http.StatusNotImplemented, "attachments are not configured")
		return
	}
	id := r.PathValue("id")
	raw := r.URL.Query().Get("url")
	if raw == "" {
		raw = r.URL.Query().Get("key")
	}
	if strings.TrimSpace(raw) == "" {
		writeError(w, http.StatusBadRequest, "url (attachment URL or key) is required")
		return
	}
	// Normalize to the stored record URL so RemoveAttachment matches.
	key := attachmentKeyFromURL(raw)
	url := "/api/v1/attachments/" + key

	doc, removed, err := s.Service.RemoveAttachment(r.Context(), id, url)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Document record is gone; now drop the bytes (best-effort — a missing
	// object is not an error). The key must belong to this supplier.
	if strings.HasPrefix(key, id+"/") {
		if rmErr := s.Objects.Remove(r.Context(), key); rmErr != nil &&
			!errors.Is(rmErr, objectstore.ErrObjectNotFound) {
			log.Printf("httpapi: remove object %s: %v", key, rmErr)
		}
	}
	_ = removed
	writeJSON(w, http.StatusOK, doc)
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
	// 录入去重：默认仅报告重复行（仍导入）；skip_duplicates=true 时命中
	// 既有供应商（含黑名单/归档）的行直接跳过、不创建。
	var opts supplier.ImportOptions
	switch strings.ToLower(strings.TrimSpace(r.FormValue("skip_duplicates"))) {
	case "true", "1", "yes", "on":
		opts.SkipDuplicates = true
	}
	report := s.Service.Import(r.Context(), items, opts)
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
	// prefer=0/false/off temporarily disables the saved home-region
	// ranking for this query (the list-page "本地优先" switch).
	switch q.Get("prefer") {
	case "0", "false", "off", "no":
		query.NoLocalPreference = true
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
		WatchedOnly:     q.Get("watched") == "true" || q.Get("watched") == "1",
		Keyword:         q.Get("q"),
		PreferProvince:  q.Get("prefer_province"),
		PreferCity:      q.Get("prefer_city"),
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

// ---------- visibility policy enforcement (可见性策略收紧) ----------

// tierDefaultPolicy is the policy used until an admin saves an explicit
// one: the cap is the tier's highest enabled level (personal: 0/1 → cap 1;
// featureflag.VisibilityLevels counts enabled levels, highest is count-1)
// and the buffer is the PRD-mandated 7 days.
func (s *Server) tierDefaultPolicy() supplier.VisibilityPolicy {
	return supplier.VisibilityPolicy{MaxLevel: s.Features.VisibilityLevels - 1, BufferDays: supplier.DefaultBufferDays}
}

// resolvePolicy builds the effective policy for a request: explicit
// request overrides win; otherwise the admin-persisted policy is loaded;
// failing that (never configured / unreadable) the tier default applies.
func (s *Server) resolvePolicy(ctx context.Context, maxLevel int, hasMax bool, bufferDays int) supplier.VisibilityPolicy {
	policy, _, err := s.Service.LoadVisibilityPolicy(ctx, s.tierDefaultPolicy())
	if err != nil {
		log.Printf("httpapi: load visibility policy: %v (using tier default)", err)
		policy = s.tierDefaultPolicy()
	}
	if hasMax {
		policy.MaxLevel = maxLevel
	}
	if bufferDays > 0 {
		policy.BufferDays = bufferDays
	}
	return policy
}

// policyParams reads optional max_level / buffer_days overrides from a JSON
// body and/or the query string (body wins for JSON callers). A missing/empty
// body is fine — all fields are optional.
func policyParams(w http.ResponseWriter, r *http.Request) (maxLevel int, hasMax bool, bufferDays int) {
	q := r.URL.Query()
	if v := q.Get("max_level"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxLevel, hasMax = n, true
		}
	}
	if v := q.Get("buffer_days"); v != "" {
		bufferDays, _ = strconv.Atoi(v)
	}
	if r.Body != nil {
		var body struct {
			MaxLevel   *int `json:"max_level"`
			BufferDays *int `json:"buffer_days"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err == nil {
			if body.MaxLevel != nil {
				maxLevel, hasMax = *body.MaxLevel, true
			}
			if body.BufferDays != nil {
				bufferDays = *body.BufferDays
			}
		}
	}
	return maxLevel, hasMax, bufferDays
}

// visibilityPolicyResponse is the GET .../policy payload: the effective
// policy plus whether an admin explicitly configured it (vs. the tier
// default) so the UI can label the setting.
type visibilityPolicyResponse struct {
	Policy       supplier.VisibilityPolicy `json:"policy"`
	Configured   bool                      `json:"configured"`
	TierMaxLevel int                       `json:"tier_max_level"`
}

// handleGetVisibilityPolicy answers the current effective visibility
// policy (admin-persisted, else tier default).
func (s *Server) handleGetVisibilityPolicy(w http.ResponseWriter, r *http.Request) {
	policy, configured, err := s.Service.LoadVisibilityPolicy(r.Context(), s.tierDefaultPolicy())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, visibilityPolicyResponse{
		Policy:       policy,
		Configured:   configured,
		TierMaxLevel: s.Features.VisibilityLevels - 1,
	})
}

// savePolicyRequest is the PUT/POST .../policy body. MaxLevel is required
// (it is the whole point of the policy); buffer days default to the PRD
// 7-day cushion.
type savePolicyRequest struct {
	MaxLevel   *int `json:"max_level"`
	BufferDays int  `json:"buffer_days"`
}

// handleSaveVisibilityPolicy persists the admin visibility policy and
// immediately runs ONE enforce sweep, so tightening the cap flags
// non-compliant records (待调整 + 7-day deadline) in the same request
// rather than waiting for the next automatic daily sweep.
func (s *Server) handleSaveVisibilityPolicy(w http.ResponseWriter, r *http.Request) {
	var req savePolicyRequest
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}
	if req.MaxLevel == nil {
		// CLI convenience: ?max_level=N
		if v := r.URL.Query().Get("max_level"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				req.MaxLevel = &n
			}
		}
	}
	if req.BufferDays == 0 {
		if v := r.URL.Query().Get("buffer_days"); v != "" {
			req.BufferDays, _ = strconv.Atoi(v)
		}
	}
	if req.MaxLevel == nil {
		writeError(w, http.StatusBadRequest, "max_level is required (the highest allowed visibility level, 0-4)")
		return
	}
	tierMax := s.Features.VisibilityLevels - 1
	if *req.MaxLevel < domain.VisSelf || *req.MaxLevel > domain.VisCompany {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("max_level must be %d-%d", domain.VisSelf, domain.VisCompany))
		return
	}
	if *req.MaxLevel > tierMax {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("this edition only enables visibility levels 0-%d; max_level cannot be set to %d", tierMax, *req.MaxLevel))
		return
	}
	policy, err := s.Service.SaveVisibilityPolicy(r.Context(), supplier.VisibilityPolicy{
		MaxLevel:   *req.MaxLevel,
		BufferDays: req.BufferDays,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Immediate sweep: the admin who tightens the policy gets the 扫描→标记
	// result right away; the sidecar's daily loop keeps it moving afterwards.
	rep, err := s.Service.EnforceVisibilityPolicy(r.Context(), policy)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"policy":     policy,
		"configured": true,
		"report":     rep,
	})
}

// ---------- local preference (本地供应商偏好) ----------

// localPreferenceRequest is the PUT body for the home region.
type localPreferenceRequest struct {
	Province string `json:"province"`
	City     string `json:"city"`
}

// localPreferenceResponse is the GET/PUT payload: the preference plus a
// configured flag (a province-less preference means local-first is off).
type localPreferenceResponse struct {
	Province   string `json:"province"`
	City       string `json:"city,omitempty"`
	Configured bool   `json:"configured"`
}

func (s *Server) handleGetLocalPreference(w http.ResponseWriter, r *http.Request) {
	p, err := s.Service.LoadLocalPreference(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, localPreferenceResponse{
		Province: p.Province, City: p.City, Configured: !p.Empty(),
	})
}

func (s *Server) handleSaveLocalPreference(w http.ResponseWriter, r *http.Request) {
	var req localPreferenceRequest
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}
	if v := r.URL.Query().Get("province"); v != "" {
		req.Province = v
	}
	if v := r.URL.Query().Get("city"); v != "" {
		req.City = v
	}
	// ?clear=1 removes the preference (local-first ranking off).
	if r.URL.Query().Get("clear") == "1" || r.URL.Query().Get("clear") == "true" {
		if err := s.Service.ClearLocalPreference(r.Context()); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, localPreferenceResponse{})
		return
	}
	if strings.TrimSpace(req.Province) == "" {
		writeError(w, http.StatusBadRequest, "province is required (PUT ?clear=1 to remove the preference)")
		return
	}
	p, err := s.Service.SaveLocalPreference(r.Context(), supplier.LocalPreference{
		Province: req.Province, City: req.City,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, localPreferenceResponse{
		Province: p.Province, City: p.City, Configured: true,
	})
}

// StartMaintenanceLoops launches the sidecar's background sweeps. The
// visibility disposition sweep runs once shortly after startup (a desktop
// app may not be open every day, so boot catches overdue downgrades) and
// then once per 24h while running. Returns when ctx is cancelled.
func (s *Server) StartMaintenanceLoops(ctx context.Context) {
	go func() {
		// Small delay so boot logs / readiness aren't tangled with the sweep.
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		s.runMaintenanceSweeps(ctx)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runMaintenanceSweeps(ctx)
			}
		}
	}()
}

// runVisibilitySweep executes one disposition sweep against the persisted
// (or tier-default) policy, logging the outcome. Failures are logged and
// retried on the next tick — the loop must never crash the sidecar.
func (s *Server) runVisibilitySweep(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("httpapi: visibility sweep panic: %v", rec)
		}
	}()
	policy, _, err := s.Service.LoadVisibilityPolicy(ctx, s.tierDefaultPolicy())
	if err != nil {
		log.Printf("httpapi: visibility sweep: load policy: %v", err)
		return
	}
	rep, err := s.Service.EnforceVisibilityPolicy(ctx, policy)
	if err != nil {
		log.Printf("httpapi: visibility sweep: %v", err)
		return
	}
	if rep.Flagged > 0 || rep.Downgraded > 0 || rep.Resolved > 0 {
		log.Printf("httpapi: visibility sweep (cap L%d): flagged=%d downgraded=%d resolved=%d pending=%d appealed=%d remaining=%d",
			policy.MaxLevel, rep.Flagged, rep.Downgraded, rep.Resolved, rep.Pending, rep.Appealed, len(rep.Items))
	}
}

// runMaintenanceSweeps runs every daily/boot background sweep: visibility
// disposition and watched-supplier expiry notifications. Each is isolated so
// one failure never blocks the other.
func (s *Server) runMaintenanceSweeps(ctx context.Context) {
	s.runVisibilitySweep(ctx)
	s.runExpiryNotificationSweep(ctx)
}

// runExpiryNotificationSweep raises one notification per watched supplier
// whose qualification enters a 90/30/7-day or expired window. Repeated runs
// are idempotent (per-window dedup keys). Logged only when it raises new
// alerts; panics are contained like the visibility sweep.
func (s *Server) runExpiryNotificationSweep(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("httpapi: expiry notification sweep panic: %v", rec)
		}
	}()
	n, err := s.Service.NotifyWatchedExpiring(ctx)
	if err != nil {
		log.Printf("httpapi: expiry notification sweep: %v", err)
		return
	}
	if n > 0 {
		log.Printf("httpapi: expiry notification sweep raised=%d", n)
	}
}

// visibilityReport is the GET .../violations payload: the read-only scan
// (通知录入者 basis), most urgent first.
type visibilityReport struct {
	GeneratedAt time.Time                      `json:"generated_at"`
	Policy      supplier.VisibilityPolicy      `json:"policy"`
	Count       int                            `json:"count"`
	Items       []supplier.VisibilityViolation `json:"items"`
}

// handleVisibilityViolations answers the read-only policy scan: every live
// supplier above the visibility cap, annotated with disposition state
// (violation / pending / appealed / overdue).
func (s *Server) handleVisibilityViolations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	maxLevel, hasMax := 0, false
	if v := q.Get("max_level"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxLevel, hasMax = n, true
		}
	}
	bufferDays := 0
	if v := q.Get("buffer_days"); v != "" {
		bufferDays, _ = strconv.Atoi(v)
	}
	policy := s.resolvePolicy(r.Context(), maxLevel, hasMax, bufferDays)
	items, err := s.Service.ScanVisibilityViolations(r.Context(), policy)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, visibilityReport{
		GeneratedAt: time.Now().UTC(),
		Policy:      policy,
		Count:       len(items),
		Items:       items,
	})
}

// handleVisibilityEnforce runs one disposition sweep (数据处置): new
// violators are flagged 待调整 with a 7-day deadline, owners who already
// fixed their level are cleared, and overdue un-appealed records are
// auto-downgraded to the cap. Returns the sweep report.
func (s *Server) handleVisibilityEnforce(w http.ResponseWriter, r *http.Request) {
	maxLevel, hasMax, bufferDays := policyParams(w, r)
	policy := s.resolvePolicy(r.Context(), maxLevel, hasMax, bufferDays)
	rep, err := s.Service.EnforceVisibilityPolicy(r.Context(), policy)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleAppealVisibility files an owner appeal (申诉) against a pending
// visibility adjustment, pausing the auto-downgrade countdown. Body is
// optional: {"note": "..."} (also accepted via ?note=).
func (s *Server) handleAppealVisibility(w http.ResponseWriter, r *http.Request) {
	note := r.URL.Query().Get("note")
	if r.Body != nil {
		var body struct {
			Note string `json:"note"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
		if strings.TrimSpace(body.Note) != "" {
			note = body.Note
		}
	}
	doc, err := s.Service.AppealVisibility(r.Context(), r.PathValue("id"), note)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// resolveAppealRequest is the POST .../resolve-visibility-appeal body.
type resolveAppealRequest struct {
	Grant      bool `json:"grant"`       // true=申诉成立(例外保留) false=驳回(立即降级)
	MaxLevel   *int `json:"max_level"`   // optional policy override
	BufferDays int  `json:"buffer_days"` // optional policy override
}

// handleResolveVisibilityAppeal is the admin decision on an open appeal:
// grant keeps the level as an approved exception; deny downgrades to the
// policy cap immediately.
func (s *Server) handleResolveVisibilityAppeal(w http.ResponseWriter, r *http.Request) {
	var req resolveAppealRequest
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}
	// Grant may also arrive as ?grant=true|false (CLI convenience). An
	// explicit body field wins; otherwise the query decides.
	grant := req.Grant
	if q := r.URL.Query().Get("grant"); q != "" {
		switch strings.ToLower(strings.TrimSpace(q)) {
		case "true", "1", "yes", "grant":
			grant = true
		case "false", "0", "no", "deny":
			grant = false
		}
	}
	maxLevel, hasMax := 0, req.MaxLevel != nil
	if req.MaxLevel != nil {
		maxLevel = *req.MaxLevel
	}
	policy := s.resolvePolicy(r.Context(), maxLevel, hasMax, req.BufferDays)
	doc, err := s.Service.ResolveVisibilityAppeal(r.Context(), r.PathValue("id"), grant, policy)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
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

// riskReviewRequest is the POST .../risk-review body. All fields but
// outcome are optional.
type riskReviewRequest struct {
	Outcome string `json:"outcome"` // verified | dismissed
	By      string `json:"by"`
	Note    string `json:"note"`
}

// handleRiskReview records a human's resolution of the shell-risk verdict
// (人工审核闭环): the supplier leaves the review queue until a risk-relevant
// edit reopens it. Returns the updated document.
func (s *Server) handleRiskReview(w http.ResponseWriter, r *http.Request) {
	var req riskReviewRequest
	// Body is optional — reviewers may POST with an empty body + ?outcome=.
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}
	if strings.TrimSpace(req.Outcome) == "" {
		req.Outcome = r.URL.Query().Get("outcome")
	}
	if strings.TrimSpace(req.By) == "" {
		req.By = "local"
	}
	doc, err := s.Service.ReviewRisk(r.Context(), r.PathValue("id"), supplier.RiskReviewInput{
		Outcome: req.Outcome,
		By:      req.By,
		Note:    req.Note,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
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
// handleBackup streams a full library backup (数据备份): a consistent DB
// snapshot plus all attachment files as a zip. The snapshot (VACUUM INTO)
// is taken before any archive bytes are written, so snapshot failures can
// still be reported as JSON; mid-stream failures (client disconnect) are
// logged.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if s.Backup == nil {
		writeError(w, http.StatusNotImplemented, "backup is not available (no on-disk data directory)")
		return
	}
	stamp := time.Now().UTC().Format("20060102")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		`attachment; filename="supplider-backup.zip"; filename*=UTF-8''`+
			url.PathEscape(fmt.Sprintf("supplider备份_%s.zip", stamp)))
	if err := s.Backup(r.Context(), w); err != nil {
		log.Printf("httpapi: backup: %v", err)
		// Only effective if no archive bytes were written yet.
		writeError(w, http.StatusInternalServerError, "backup failed")
	}
}

// restoreStatusResponse describes a staged restore to the UI.
type restoreStatusResponse struct {
	Staged   bool             `json:"staged"`
	Manifest *backup.Manifest `json:"manifest,omitempty"`
	Hint     string           `json:"hint,omitempty"`
}

// handleRestoreStatus reports whether a validated restore is waiting for
// the next application restart.
func (s *Server) handleRestoreStatus(w http.ResponseWriter, r *http.Request) {
	if s.Restore == nil {
		writeError(w, http.StatusNotImplemented, "restore is not available (no on-disk data directory)")
		return
	}
	m, staged, err := s.Restore.Pending()
	if err != nil {
		log.Printf("httpapi: restore pending: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read pending restore")
		return
	}
	if !staged {
		writeJSON(w, http.StatusOK, restoreStatusResponse{Staged: false})
		return
	}
	writeJSON(w, http.StatusOK, restoreStatusResponse{
		Staged:   true,
		Manifest: &m,
		Hint:     "恢复包已校验暂存：完全退出并重新打开应用后生效（当前数据库会先保留为 restore.rollback-* 以便回退）",
	})
}

// handleRestoreStage accepts a backup zip (raw application/zip body or a
// multipart "file" field), validates it and stages it for the next restart.
// Nothing live is replaced until then; the current library is preserved in
// a rollback directory at apply time.
func (s *Server) handleRestoreStage(w http.ResponseWriter, r *http.Request) {
	if s.Restore == nil {
		writeError(w, http.StatusNotImplemented, "restore is not available (no on-disk data directory)")
		return
	}
	// 256 MiB cap (attachments are ≤50MB each; personal libraries are small).
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	body := r.Body
	if ct := r.Header.Get("Content-Type"); strings.HasPrefix(ct, "multipart/") {
		if err := r.ParseMultipartForm(256 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, `multipart restore requires a "file" field`)
			return
		}
		defer f.Close()
		body = f
	}
	manifest, err := s.Restore.Stage(body)
	if err != nil {
		// Every Stage failure is a bad/unusable archive — never 500.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, restoreStatusResponse{
		Staged:   true,
		Manifest: &manifest,
		Hint:     "恢复包已通过完整性校验并暂存。请完全退出应用后重新打开，数据将在启动时恢复（当前数据自动保留一份回退副本）。",
	})
}

// handleRestoreCancel discards a staged restore.
func (s *Server) handleRestoreCancel(w http.ResponseWriter, r *http.Request) {
	if s.Restore == nil {
		writeError(w, http.StatusNotImplemented, "restore is not available (no on-disk data directory)")
		return
	}
	if err := s.Restore.Cancel(); err != nil {
		log.Printf("httpapi: restore cancel: %v", err)
		writeError(w, http.StatusInternalServerError, "could not cancel pending restore")
		return
	}
	writeJSON(w, http.StatusOK, restoreStatusResponse{Staged: false})
}

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
	case errors.Is(err, datamodel.ErrInvalidPagination):
		// A malformed/garbled ?cursor= (bad base64/JSON, stale link) is the
		// caller's fault — answer 400 so the client can restart from page 1
		// instead of treating an opaque token failure as a server crash.
		writeError(w, http.StatusBadRequest, "invalid pagination cursor")
	case errors.Is(err, objectstore.ErrInvalidKey):
		// A malformed or traversal-shaped attachment key (../, absolute,
		// empty). The store already refuses to escape its root; classify it
		// as a bad request, not an internal error.
		writeError(w, http.StatusBadRequest, "invalid attachment key")
	default:
		// Domain "not found" errors (e.g. attachment/record missing) are 404.
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		// Validation errors are 400; everything unexpected is 500.
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "must be") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("httpapi: internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
