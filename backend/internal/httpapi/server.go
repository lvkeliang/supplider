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
//	POST   /api/v1/suppliers/{id}/attachments   upload (multipart, ≤50MB)
//	GET    /api/v1/attachments/{key...}         download/stream
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
	"github.com/supplider/supplider/backend/internal/featureflag"
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
	s.Mux.HandleFunc("POST /api/v1/suppliers/{id}/attachments", s.handleUploadAttachment)
	s.Mux.HandleFunc("GET /api/v1/attachments/{key...}", s.handleDownloadAttachment)
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

	query := datamodel.Query{
		Limit:  limit,
		Cursor: q.Get("cursor"),
		Sort:   datamodel.Sort{Field: datamodel.SortField(q.Get("sort")), Order: datamodel.SortOrder(q.Get("order"))},
		Filter: datamodel.SupplierFilter{
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
		},
	}

	page, err := s.Service.List(r.Context(), query)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
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
