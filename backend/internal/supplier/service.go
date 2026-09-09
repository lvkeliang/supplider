// Package supplier holds supplier business logic: lifecycle operations,
// validation, ID generation, automatic change_log diffing and rating
// aggregation.
//
// This package is tier-agnostic: it depends ONLY on datamodel.SupplierStore
// (an interface). The same code compiles under personal / small_business /
// enterprise tags — concrete stores arrive via the adapter factory.
package supplier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
)

// Service implements the supplier lifecycle use cases.
type Service struct {
	store datamodel.SupplierStore
	// notifs raises in-app alerts for followed suppliers. It is the same
	// concrete store (memory/sqlite implement both ports); nil when a store
	// offers no feed, in which case watch notifications silently no-op.
	notifs datamodel.NotificationStore
	now    func() time.Time // injectable for tests
	newID  func() string    // injectable for tests; defaults to domain.NewSupplierID
}

// NewService wires the service to a store.
func NewService(store datamodel.SupplierStore) *Service {
	notifs, _ := store.(datamodel.NotificationStore)
	return &Service{
		store:  store,
		notifs: notifs,
		now:    func() time.Time { return time.Now().UTC() },
		newID:  domain.NewSupplierID,
	}
}

// WithIDGenerator overrides supplier ID generation. Production uses
// domain.NewSupplierID; tests inject a deterministic sequence.
func (s *Service) WithIDGenerator(fn func() string) *Service {
	s.newID = fn
	return s
}

// CreateInput carries the fields a user may set at creation time.
type CreateInput struct {
	Owner          string
	BasicInfo      domain.BasicInfo
	Qualifications []domain.Qualification
	Categories     []string
	Products       []domain.ProductService
	Performance    []domain.Performance
	Visibility     int
	SharedWith     []string
	CustomFields   map[string]any
	Source         string // manual | import (change_log provenance)
}

// Create validates, assigns an ID, stamps timestamps and persists a new
// supplier. The initial change_log records the creation.
func (s *Service) Create(ctx context.Context, in CreateInput) (*domain.Supplier, error) {
	if in.Source == "" {
		in.Source = domain.SourceManual
	}
	if err := validateBasic(in.BasicInfo); err != nil {
		return nil, err
	}
	if in.Visibility < domain.VisSelf || in.Visibility > domain.VisCompany {
		return nil, fmt.Errorf("supplier: visibility must be 0-4, got %d", in.Visibility)
	}
	if err := validatePerformance(in.Performance); err != nil {
		return nil, err
	}

	now := s.now()
	doc := &domain.Supplier{
		Owner:              in.Owner,
		Status:             domain.StatusActive,
		BasicInfo:          in.BasicInfo,
		Qualifications:     in.Qualifications,
		Categories:         in.Categories,
		ProductsServices:   in.Products,
		PerformanceHistory: in.Performance,
		Visibility:         in.Visibility,
		SharedWith:         in.SharedWith,
		CustomFields:       in.CustomFields,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	doc.ChangeLog = []domain.ChangeEntry{{
		Field:  "_created",
		New:    in.BasicInfo.CompanyName,
		Date:   now,
		Source: in.Source,
	}}
	recomputeRating(doc)
	s.applyRisk(doc) // 空壳特征检测：非 AI 规则引擎，随写入刷新 risk_flags

	// ID generation retries on collision. Put is an UPSERT in every
	// adapter (Update relies on that), so it cannot detect a duplicated
	// create id itself — probe with Get first, otherwise a colliding id
	// would silently overwrite another supplier document.
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		id := s.newID()
		switch existing, err := s.store.Get(ctx, id); {
		case err == nil && existing != nil:
			continue // id taken (active or archived) — regenerate
		case err != nil && !errors.Is(err, datamodel.ErrNotFound):
			return nil, err
		}
		doc.ID = id
		err := s.store.Put(ctx, doc)
		if err == nil {
			return doc, nil
		}
		if !errors.Is(err, datamodel.ErrConflict) {
			return nil, err
		}
		// Adapter-reported race (e.g. two processes picked the same id).
	}
	return nil, fmt.Errorf("supplier: could not allocate unique id after %d attempts", maxAttempts)
}

// Get returns one document (archived included).
func (s *Service) Get(ctx context.Context, id string) (*domain.Supplier, error) {
	return s.store.Get(ctx, id)
}

// UpdateInput carries mutable fields. Nil slices / nil map mean "no change";
// an explicit empty slice clears that collection. The service diffs against
// the stored document and appends one change_log entry per changed field.
type UpdateInput struct {
	BasicInfo      *domain.BasicInfo
	Qualifications *[]domain.Qualification
	Categories     *[]string
	Products       *[]domain.ProductService
	Performance    *[]domain.Performance
	Visibility     *int
	SharedWith     *[]string
	CustomFields   *map[string]any
	Source         string
}

// Update applies a partial update, recording every changed field in
// change_log (field / old / new / date / source).
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (*domain.Supplier, error) {
	if in.Source == "" {
		in.Source = domain.SourceManual
	}
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if doc.Status == domain.StatusArchived {
		return nil, fmt.Errorf("supplier: %s is archived; restore before editing", id)
	}

	now := s.now()
	changes := []domain.ChangeEntry{}
	addChange := func(c domain.ChangeEntry) {
		if c.Field != "" {
			changes = append(changes, c)
		}
	}

	if in.BasicInfo != nil {
		if err := validateBasic(*in.BasicInfo); err != nil {
			return nil, err
		}
		changes = append(changes, diffFields("basic_info", doc.BasicInfo, *in.BasicInfo, now, in.Source)...)
		doc.BasicInfo = *in.BasicInfo
	}
	if in.Qualifications != nil {
		addChange(diffField("qualifications", doc.Qualifications, *in.Qualifications, now, in.Source))
		doc.Qualifications = *in.Qualifications
	}
	if in.Categories != nil {
		addChange(diffField("categories", doc.Categories, *in.Categories, now, in.Source))
		doc.Categories = *in.Categories
	}
	if in.Products != nil {
		addChange(diffField("products_services", doc.ProductsServices, *in.Products, now, in.Source))
		doc.ProductsServices = *in.Products
	}
	if in.Performance != nil {
		if err := validatePerformance(*in.Performance); err != nil {
			return nil, err
		}
		addChange(diffField("performance_history", doc.PerformanceHistory, *in.Performance, now, in.Source))
		doc.PerformanceHistory = *in.Performance
	}
	if in.Visibility != nil {
		if *in.Visibility < domain.VisSelf || *in.Visibility > domain.VisCompany {
			return nil, fmt.Errorf("supplier: visibility must be 0-4, got %d", *in.Visibility)
		}
		addChange(diffField("visibility", doc.Visibility, *in.Visibility, now, in.Source))
		doc.Visibility = *in.Visibility
		// A policy exception (申诉成立) applied to the previously approved
		// level — a subsequent edit picks a new level and must re-earn it.
		if doc.VisException {
			addChange(diffField("vis_exception", true, false, now, in.Source))
			doc.VisException = false
		}
	}
	if in.SharedWith != nil {
		addChange(diffField("shared_with", doc.SharedWith, *in.SharedWith, now, in.Source))
		doc.SharedWith = *in.SharedWith
	}
	if in.CustomFields != nil {
		addChange(diffField("custom_fields", doc.CustomFields, *in.CustomFields, now, in.Source))
		doc.CustomFields = *in.CustomFields
	}

	if len(changes) == 0 {
		return doc, nil // no-op update: do not touch UpdatedAt
	}
	doc.ChangeLog = append(doc.ChangeLog, changes...)
	doc.UpdatedAt = now
	recomputeRating(doc)
	// If the edit touches fields the rule engine reads, a prior human
	// clearance no longer applies — reopen the review before re-evaluating.
	reopenRiskReviewIfNeeded(doc, changes)
	wasRisk := doc.RiskFlags.ShellRisk
	s.applyRisk(doc) // 资料变更后重新跑空壳规则

	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	// A NEW shell-risk verdict on a followed supplier raises an alert; a
	// risk clearing or an unchanged verdict does not.
	if doc.Watched && !wasRisk && doc.RiskFlags.ShellRisk {
		s.notify(ctx, doc, datamodel.NotifRiskFlagged, datamodel.SeverityDanger,
			"关注供应商出现空壳风险信号",
			"资料变更后规则引擎新判定为空壳风险，请查看风险检测并核验原件", "")
	}
	return doc, nil
}

// AddAttachment registers an uploaded file on the document.
func (s *Service) AddAttachment(ctx context.Context, id string, att domain.Attachment) (*domain.Supplier, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now()
	att.UploadedAt = now
	doc.Attachments = append(doc.Attachments, att)
	doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
		Field: "attachments", New: att.Name, Date: now, Source: domain.SourceManual,
	})
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// AttachmentReferenced reports whether ANY supplier (including archived
// ones) still carries an attachment record with this URL. Merge
// reference-unions attachments, so the same object can be referenced by the
// surviving master AND the archived duplicate; the wiring layer must only
// delete the backing bytes after the last reference is gone.
func (s *Service) AttachmentReferenced(ctx context.Context, url string) (bool, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return false, nil
	}
	cursor := ""
	for {
		page, err := s.store.List(ctx, datamodel.Query{
			Filter: datamodel.SupplierFilter{IncludeArchived: true},
			Limit:  datamodel.MaxPageSize,
			Cursor: cursor,
			Sort:   datamodel.Sort{Field: datamodel.SortCreatedAt, Order: datamodel.OrderDesc},
		})
		if err != nil {
			return false, err
		}
		for _, d := range page.Items {
			for _, a := range d.Attachments {
				if a.URL == url {
					return true, nil
				}
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return false, nil
}

// RemoveAttachment detaches one attachment identified by its URL from the
// document. It returns the removed record (so the wiring layer can delete
// the backing object bytes) and an error if no attachment carries that URL.
// Archived suppliers are not editable. The object bytes themselves are not
// touched here — the store port does not know about the object store.
func (s *Service) RemoveAttachment(ctx context.Context, id, url string) (*domain.Supplier, *domain.Attachment, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, nil, fmt.Errorf("supplier: attachment url is required")
	}
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if doc.Status == domain.StatusArchived {
		return nil, nil, fmt.Errorf("supplier: %s is archived; restore before editing", id)
	}
	idx := -1
	for i := range doc.Attachments {
		if doc.Attachments[i].URL == url {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, nil, fmt.Errorf("supplier: attachment not found: %s", url)
	}
	removed := doc.Attachments[idx]
	doc.Attachments = append(doc.Attachments[:idx], doc.Attachments[idx+1:]...)
	now := s.now()
	doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
		Field: "attachments", Old: removed.Name, Date: now, Source: domain.SourceManual,
	})
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, nil, err
	}
	return doc, &removed, nil
}

// Archive performs the delete = archive lifecycle action.
func (s *Service) Archive(ctx context.Context, id string) error {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if doc.Status == domain.StatusArchived {
		return nil // idempotent
	}
	now := s.now()
	doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
		Field: "status", Old: doc.Status, New: domain.StatusArchived, Date: now, Source: domain.SourceManual,
	})
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil { // adapter sets archived status + timestamp
		return err
	}
	s.notify(ctx, doc, datamodel.NotifArchived, datamodel.SeverityInfo,
		"关注供应商已归档", "该供应商被归档（保留历史，默认列表不再显示）", "")
	return nil
}

// Restore un-archives a supplier.
func (s *Service) Restore(ctx context.Context, id string) (*domain.Supplier, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if doc.Status != domain.StatusArchived {
		return doc, nil
	}
	now := s.now()
	doc.Status = domain.StatusActive
	doc.ArchivedAt = nil
	doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
		Field: "status", Old: domain.StatusArchived, New: domain.StatusActive, Date: now, Source: domain.SourceManual,
	})
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	s.notify(ctx, doc, datamodel.NotifRestored, datamodel.SeverityInfo,
		"关注供应商已恢复", "该供应商已从归档恢复，重新进入正常列表", "")
	return doc, nil
}

// ImportItem is one spreadsheet row destined for batch import. Row is the
// 1-based spreadsheet row (header = row 1, first data row = 2) so error
// reports point the user at the exact cell range.
type ImportItem struct {
	Row   int         `json:"row"`
	Input CreateInput `json:"input"`
}

// ImportError reports one failed row; valid rows still import.
type ImportError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

// ImportReport summarizes a batch import.
type ImportReport struct {
	Created int `json:"created"`
	Failed  int `json:"failed"`
	// Skipped counts rows not imported because they matched an existing
	// supplier (only when ImportOptions.SkipDuplicates is set).
	Skipped int           `json:"skipped,omitempty"`
	IDs     []string      `json:"ids,omitempty"`
	Errors  []ImportError `json:"errors,omitempty"`
	// Duplicates reports every row that looked like a duplicate — whether it
	// was skipped or (in warn mode) still imported. Non-blocking by default.
	Duplicates []ImportDuplicate `json:"duplicates,omitempty"`
}

// ImportDuplicate reports one imported row that matched an existing supplier
// (or an earlier row in the same batch).
type ImportDuplicate struct {
	Row     int              `json:"row"`
	Name    string           `json:"name"` // candidate company name from the row
	Matches []DuplicateMatch `json:"matches"`
}

// ImportOptions tunes a batch import.
type ImportOptions struct {
	// SkipDuplicates: when true, a row that matches an existing supplier is
	// NOT imported (counted in Skipped). When false (default) the row is
	// imported anyway but reported in Duplicates as a warning — same
	// "flag, don't block" philosophy as manual entry.
	SkipDuplicates bool
}

// Import creates suppliers in bulk (Excel import). Each row goes through the
// SAME Create path — validation, ID generation, change_log — so an imported
// document is indistinguishable from a manually entered one. Source is forced
// to import for provenance. One bad row never aborts the batch: its error is
// recorded and import continues (校验报错行报告).
//
// Before creating, each row is checked for duplicates (录入去重) against the
// existing library — including blacklisted/archived records — AND against rows
// already accepted earlier in the SAME file, so two identical spreadsheet rows
// are caught too. The library is indexed ONCE up front (pre-normalized keys)
// rather than re-scanned per row, keeping a 1000-row import fast.
func (s *Service) Import(ctx context.Context, items []ImportItem, opts ImportOptions) ImportReport {
	rep := ImportReport{Errors: []ImportError{}, Duplicates: []ImportDuplicate{}}

	// One-pass index of the whole library; accepted batch rows are appended
	// as we go so intra-batch duplicates are caught too. A load failure is
	// non-fatal: Create still surfaces store errors per row.
	index, err := s.loadDedupIndex(ctx)
	if err != nil {
		index = nil
	}

	for _, it := range items {
		in := it.Input
		in.Source = domain.SourceImport

		// Duplicate check (pre-normalized index; matches carry status).
		k := keyFrom(in.BasicInfo)
		if matches := findInDedupIndex(k, index); len(matches) > 0 {
			rep.Duplicates = append(rep.Duplicates, ImportDuplicate{
				Row:     it.Row,
				Name:    in.BasicInfo.CompanyName,
				Matches: matches,
			})
			// Skip mode honors only high-confidence matches. The possible
			// tier (name merely similar) is warn-only: skipping it could
			// discard a genuinely new, distinct company. Matches are
			// rank-sorted, so the head level decides.
			if opts.SkipDuplicates && matches[0].Level != MatchPossible {
				rep.Skipped++
				continue
			}
		}

		doc, err := s.Create(ctx, in)
		if err != nil {
			rep.Failed++
			rep.Errors = append(rep.Errors, ImportError{Row: it.Row, Message: err.Error()})
			continue
		}
		rep.Created++
		rep.IDs = append(rep.IDs, doc.ID)
		index = append(index, indexDoc(doc)) // later rows in this file now match it
	}
	return rep
}

// List returns a page of supplier SUMMARIES (list endpoints never return
// full documents — performance red line).
func (s *Service) List(ctx context.Context, q datamodel.Query) (datamodel.Page[domain.Summary], error) {
	// Local-first ranking: the persisted home-region preference is filled
	// in here (not in the adapters/HTTP) so CLI, MCP, gRPC and HTTP all get
	// identical behavior. Ranking only — non-local suppliers still appear.
	q = s.withLocalPreference(ctx, q)
	page, err := s.store.List(ctx, q)
	if err != nil {
		return datamodel.Page[domain.Summary]{}, err
	}
	out := datamodel.Page[domain.Summary]{
		Items:      make([]domain.Summary, 0, len(page.Items)),
		NextCursor: page.NextCursor,
	}
	for _, doc := range page.Items {
		out.Items = append(out.Items, domain.ToSummary(doc))
	}
	return out, nil
}

// MaxExportDocs caps a single export. Personal databases are local and
// small (hundreds to low thousands of rows); the cap guards against
// unbounded memory use rather than representing a workflow limit.
const MaxExportDocs = 50000

// Export returns ALL documents matching the filter — FULL documents, not
// summaries (export is the backup/exchange path, so change_log,
// attachments and custom_fields are included). It walks keyset pages
// internally: unlike the interactive list, export is exempt from the
// 100-row page cap because it is a batch operation, not a UI query.
func (s *Service) Export(ctx context.Context, filter datamodel.SupplierFilter) ([]*domain.Supplier, error) {
	docs := make([]*domain.Supplier, 0, datamodel.DefaultPageSize)
	cursor := ""
	for {
		page, err := s.store.List(ctx, datamodel.Query{
			Filter: filter,
			Limit:  datamodel.MaxPageSize,
			Cursor: cursor,
			Sort:   datamodel.Sort{Field: datamodel.SortCreatedAt, Order: datamodel.OrderDesc},
		})
		if err != nil {
			return nil, err
		}
		docs = append(docs, page.Items...)
		if page.NextCursor == "" || len(docs) >= MaxExportDocs {
			break
		}
		cursor = page.NextCursor
	}
	if len(docs) > MaxExportDocs {
		docs = docs[:MaxExportDocs]
	}
	return docs, nil
}

// ---------- validation / diffing / rating ----------

func validateBasic(b domain.BasicInfo) error {
	if strings.TrimSpace(b.CompanyName) == "" {
		return fmt.Errorf("supplier: company_name is required")
	}
	if strings.TrimSpace(b.Region.Province) == "" || strings.TrimSpace(b.Region.City) == "" {
		return fmt.Errorf("supplier: region.province and region.city are required (地域必填)")
	}
	return nil
}

// diffField produces one change entry when old != new (JSON-normalized
// comparison so []string{} vs nil and map ordering do not create noise).
func diffField(field string, old, new any, now time.Time, source string) domain.ChangeEntry {
	if jsonEqual(old, new) {
		return domain.ChangeEntry{}
	}
	return domain.ChangeEntry{Field: field, Old: old, New: new, Date: now, Source: source}
}

// diffFields diffs two BasicInfo structs field-by-field so the change log
// reads "legal_person: 李四 → 张三" rather than one opaque basic_info blob.
func diffFields(prefix string, old, new domain.BasicInfo, now time.Time, source string) []domain.ChangeEntry {
	ov, _ := json.Marshal(old)
	nv, _ := json.Marshal(new)
	var om, nm map[string]any
	_ = json.Unmarshal(ov, &om)
	_ = json.Unmarshal(nv, &nm)

	changes := []domain.ChangeEntry{}
	for _, k := range unionKeys(om, nm) {
		ov, nv := om[k], nm[k]
		// Absent (omitempty) and explicit-zero are equivalent "empty".
		if isEmpty(ov) && isEmpty(nv) {
			continue
		}
		if !jsonEqual(ov, nv) {
			changes = append(changes, domain.ChangeEntry{
				Field:  prefix + "." + k,
				Old:    ov,
				New:    nv,
				Date:   now,
				Source: source,
			})
		}
	}
	return changes
}

// unionKeys returns the sorted union of keys across both maps — a field
// that was empty (omitted via omitempty) and is newly set must still diff.
func unionKeys(maps ...map[string]any) []string {
	seen := map[string]bool{}
	for _, m := range maps {
		for k := range m {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case float64:
		return t == 0
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	default:
		return false
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// recomputeRating sets the denormalized aggregate score from performance
// history (mean of project scores). Empty history → 0.
func recomputeRating(doc *domain.Supplier) {
	if len(doc.PerformanceHistory) == 0 {
		doc.Rating = 0
		return
	}
	var sum float64
	n := 0
	for _, p := range doc.PerformanceHistory {
		// Effective score: explicit overall score, else mean of the
		// 交付/质量/配合度 dimensions set on the record.
		if s := p.EffectiveScore(); s > 0 {
			sum += s
			n++
		}
	}
	if n == 0 {
		doc.Rating = 0
		return
	}
	doc.Rating = float64(int(sum/float64(n)*100+0.5)) / 100 // round to 2dp
}
