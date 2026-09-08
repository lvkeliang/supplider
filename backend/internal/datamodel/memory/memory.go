// Package memory is an in-memory SupplierStore adapter.
//
// It serves three purposes:
//  1. Reference implementation that pins down filter/pagination/archive
//     semantics via the shared contract test suite.
//  2. Fast storage for unit tests and CLI sandboxes on every tier.
//  3. Fallback when a personal-tier data directory is not writable.
//
// Documents are stored as JSON clones so the adapter has true
// document semantics (no shared pointers with callers), matching what
// the SQLite JSONB and MongoDB adapters will provide.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
)

// Store is a goroutine-safe in-memory SupplierStore.
type Store struct {
	mu       sync.RWMutex
	docs     map[string]*domain.Supplier
	settings map[string]string
}

// New returns an empty in-memory store.
func New() *Store {
	return &Store{docs: make(map[string]*domain.Supplier), settings: make(map[string]string)}
}

func (s *Store) Ping(_ context.Context) error { return nil }
func (s *Store) Close() error                 { return nil }

// GetSetting implements datamodel.SupplierStore.
func (s *Store) GetSetting(_ context.Context, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.settings[key]
	if !ok {
		return "", datamodel.ErrNotFound
	}
	return v, nil
}

// PutSetting implements datamodel.SupplierStore.
func (s *Store) PutSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}

func (s *Store) Put(_ context.Context, doc *domain.Supplier) error {
	if doc == nil || doc.ID == "" {
		return fmt.Errorf("memory: put requires a document with an ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[doc.ID] = clone(doc)
	return nil
}

func (s *Store) Get(_ context.Context, id string) (*domain.Supplier, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.docs[id]
	if !ok {
		return nil, datamodel.ErrNotFound
	}
	return clone(doc), nil
}

func (s *Store) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[id]
	if !ok {
		return datamodel.ErrNotFound
	}
	now := time.Now().UTC()
	doc.Status = domain.StatusArchived
	doc.ArchivedAt = &now
	doc.UpdatedAt = now
	return nil
}

func (s *Store) List(_ context.Context, q datamodel.Query) (datamodel.Page[*domain.Supplier], error) {
	q.Normalize()
	cur, err := datamodel.DecodeCursor(q.Cursor)
	if err != nil {
		return datamodel.Page[*domain.Supplier]{}, err
	}

	s.mu.RLock()
	matched := make([]*domain.Supplier, 0, len(s.docs))
	for _, doc := range s.docs {
		if matchesFilter(doc, q.Filter) {
			matched = append(matched, clone(doc))
		}
	}
	s.mu.RUnlock()

	sortDocs(matched, q.Sort, q.Filter)

	// Keyset position: drop everything at or before the cursor tuple.
	if cur.ID != "" {
		pos := -1
		curKey := cur.SortKey
		for i, doc := range matched {
			if doc.ID == cur.ID && sortKey(doc, q.Sort.Field) == curKey {
				pos = i
				break
			}
		}
		if pos >= 0 {
			matched = matched[pos+1:]
		}
	}

	page := datamodel.Page[*domain.Supplier]{Items: []*domain.Supplier{}}
	if len(matched) > q.Limit {
		last := matched[q.Limit-1]
		page.NextCursor = datamodel.EncodeCursor(datamodel.Cursor{
			SortKey: sortKey(last, q.Sort.Field),
			ID:      last.ID,
			Prio: datamodel.LocalPrio(last.BasicInfo.Region.Province, last.BasicInfo.Region.City,
				q.Filter.PreferProvince, q.Filter.PreferCity),
		})
		matched = matched[:q.Limit]
	}
	page.Items = matched
	return page, nil
}

// ---------- filtering ----------

func matchesFilter(d *domain.Supplier, f datamodel.SupplierFilter) bool {
	// Archive handling: explicit Status wins; otherwise archived docs are
	// hidden unless IncludeArchived.
	if f.Status != "" {
		if d.Status != f.Status {
			return false
		}
	} else if !f.IncludeArchived && d.Status == domain.StatusArchived {
		return false
	}

	if f.Province != "" && d.BasicInfo.Region.Province != f.Province {
		return false
	}
	if f.City != "" && d.BasicInfo.Region.City != f.City {
		return false
	}
	if f.District != "" && d.BasicInfo.Region.District != f.District {
		return false
	}
	if len(f.Categories) > 0 && !containsAny(d.Categories, f.Categories) {
		return false
	}
	if f.MinQualRank > 0 && domain.HighestQualRank(d) < f.MinQualRank {
		return false
	}
	if f.MinRating > 0 && d.Rating < f.MinRating {
		return false
	}
	if f.MaxRating > 0 && d.Rating > f.MaxRating {
		return false
	}
	if f.OwnerID != "" && d.Owner != f.OwnerID {
		return false
	}
	if f.VisibilityMax != nil && d.Visibility > *f.VisibilityMax {
		return false
	}
	if f.Keyword != "" && !matchesKeyword(d, f.Keyword) {
		return false
	}
	return true
}

func containsAny(haystack, needles []string) bool {
	for _, n := range needles {
		for _, h := range haystack {
			if h == n {
				return true
			}
		}
	}
	return false
}

// matchesKeyword does naive substring matching over the searchable text
// blob. The real full-text port (search.Index) owns tokenization, pinyin
// and fuzzy matching; this only keeps CLI/embedded flows functional
// without a search engine.
func matchesKeyword(d *domain.Supplier, kw string) bool {
	kw = strings.ToLower(strings.TrimSpace(kw))
	if kw == "" {
		return true
	}
	var b strings.Builder
	b.WriteString(d.BasicInfo.CompanyName)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.CreditCode)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.LegalPerson)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.BusinessScope)
	for _, c := range d.Categories {
		b.WriteString(" ")
		b.WriteString(c)
	}
	for _, p := range d.ProductsServices {
		b.WriteString(" ")
		b.WriteString(p.Name)
	}
	for k, v := range d.CustomFields {
		b.WriteString(" ")
		b.WriteString(k)
		b.WriteString(" ")
		fmt.Fprintf(&b, "%v", v)
	}
	return strings.Contains(strings.ToLower(b.String()), kw)
}

// ---------- sorting / keyset ----------

func sortKey(d *domain.Supplier, f datamodel.SortField) string {
	switch f {
	case datamodel.SortUpdatedAt:
		return d.UpdatedAt.UTC().Format(time.RFC3339Nano)
	case datamodel.SortRating:
		// Fixed-width so lexicographic order equals numeric order.
		return strconv.FormatFloat(d.Rating, 'f', 6, 64)
	case datamodel.SortName:
		return d.BasicInfo.CompanyName
	default:
		return d.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
}

// cmpTuple compares (sortKey, id): <0 means a sorts BEFORE b in descending
// order (i.e. a is "newer/larger").
func cmpTuple(a, b *domain.Supplier, f datamodel.SortField) int {
	ka, kb := sortKey(a, f), sortKey(b, f)
	if ka != kb {
		if ka < kb {
			return 1 // larger key first (desc)
		}
		return -1
	}
	if a.ID < b.ID {
		return 1
	}
	if a.ID > b.ID {
		return -1
	}
	return 0
}

func sortDocs(docs []*domain.Supplier, s datamodel.Sort, f datamodel.SupplierFilter) {
	// Local-first priority leads when a region preference is set.
	prio := func(d *domain.Supplier) int {
		return datamodel.LocalPrio(d.BasicInfo.Region.Province, d.BasicInfo.Region.City,
			f.PreferProvince, f.PreferCity)
	}
	less := func(i, j int) bool {
		if f.PreferProvince != "" {
			pi, pj := prio(docs[i]), prio(docs[j])
			if pi != pj {
				return pi > pj // local (1) first
			}
		}
		return cmpTuple(docs[i], docs[j], s.Field) < 0
	}
	greater := func(i, j int) bool {
		if f.PreferProvince != "" {
			pi, pj := prio(docs[i]), prio(docs[j])
			if pi != pj {
				return pi > pj // local tier still first under ascending secondary
			}
		}
		return cmpTuple(docs[i], docs[j], s.Field) > 0
	}
	cmp := less
	if s.Order == datamodel.OrderAsc {
		cmp = greater
	}
	sort.Slice(docs, cmp)
}

// clone round-trips a document through JSON to guarantee the stored copy
// is independent of caller-owned memory.
func clone(d *domain.Supplier) *domain.Supplier {
	b, err := json.Marshal(d)
	if err != nil {
		// Our own documents must always be JSON-serializable.
		panic(fmt.Errorf("memory: supplier %s is not JSON-serializable: %w", d.ID, err))
	}
	var out domain.Supplier
	if err := json.Unmarshal(b, &out); err != nil {
		panic(fmt.Errorf("memory: supplier %s clone failed: %w", d.ID, err))
	}
	return &out
}
