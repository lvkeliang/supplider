// Duplicate supplier detection (录入去重): the non-AI guard against the
// core problem the platform exists to solve — supplier resources scattered
// across people, so the same real-world company gets entered twice. When a
// supplier is about to be added (manual form, Excel import, or an MCP/AI
// agent), CheckDuplicates scans the existing library and flags likely
// matches BEFORE the duplicate is created.
//
// Matching is deliberately conservative and explainable (no fuzzy/AI layer):
//   - strong:   identical 18-char unified social credit code (definitive —
//     a real company has exactly one).
//   - probable: identical normalized company name (registered names are
//     unique within a registration authority; punctuation/spacing
//     variants are normalized away).
//
// The scan covers EVERY supplier including blacklisted and archived ones:
// re-onboarding a confirmed-fraud company that is already on the blacklist
// is the highest-value match, so the result always carries the existing
// record's status. This never blocks entry — it surfaces a warning a human
// confirms (same "flag, don't block" philosophy as the risk engine).
package supplier

import (
	"context"
	"sort"
	"strings"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
)

// Match levels.
const (
	MatchStrong   = "strong"   // 信用代码一致（确凿）
	MatchProbable = "probable" // 公司名称一致（高度疑似）
)

// DuplicateMatch is one existing supplier that looks like the candidate
// being entered.
type DuplicateMatch struct {
	SupplierID string `json:"supplier_id"`
	Name       string `json:"name"`
	Province   string `json:"province,omitempty"`
	City       string `json:"city,omitempty"`
	Status     string `json:"status"` // active | blacklisted | archived
	Level      string `json:"level"`  // strong | probable
	Reason     string `json:"reason"` // 中文解释
	CreditCode string `json:"credit_code,omitempty"`
}

// CheckDuplicates scans the whole library (including archived/blacklisted)
// for suppliers that match the candidate's basic info. Returns strong
// matches first, then probable; within a level, blacklisted records rank
// first (safety) then name. Empty when the candidate has neither a usable
// credit code nor company name. Pure read — never mutates or blocks.
func (s *Service) CheckDuplicates(ctx context.Context, candidate domain.BasicInfo) ([]DuplicateMatch, error) {
	targetCode := normalizeCreditCode(candidate.CreditCode)
	targetName := normalizeCompanyName(candidate.CompanyName)
	if targetCode == "" && targetName == "" {
		return []DuplicateMatch{}, nil
	}

	matches := make([]DuplicateMatch, 0)
	cursor := ""
	for {
		page, err := s.store.List(ctx, datamodel.Query{
			// Include archived/blacklisted: a blacklisted fraudster must be
			// surfaced even though re-onboarding might target an old record.
			Filter: datamodel.SupplierFilter{IncludeArchived: true},
			Limit:  datamodel.MaxPageSize,
			Cursor: cursor,
			Sort:   datamodel.Sort{Field: datamodel.SortCreatedAt, Order: datamodel.OrderDesc},
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range page.Items {
			m, ok := matchOne(targetCode, targetName, doc)
			if ok {
				matches = append(matches, m)
			}
		}
		if page.NextCursor == "" || len(matches) >= MaxExportDocs {
			break
		}
		cursor = page.NextCursor
	}

	rankAndSortMatches(matches)
	return matches, nil
}

// matchRank orders definitive (strong / credit-code) matches before
// probable (name) ones, and within each level surfaces a blacklisted
// (do-not-use) record first.
func matchRank(m DuplicateMatch) int {
	r := 2 // probable
	if m.Level == MatchStrong {
		r = 0
	}
	if m.Status != domain.StatusBlacklisted {
		r++ // non-blacklisted sorts after blacklisted within its level
	}
	return r
}

func matchOne(targetCode, targetName string, doc *domain.Supplier) (DuplicateMatch, bool) {
	return matchEntry(targetCode, targetName, indexDoc(doc))
}

// dedupEntry is a library supplier with its match keys pre-normalized, so a
// bulk pass (Excel import) normalizes every existing record ONCE instead of
// re-normalizing it for each candidate row.
type dedupEntry struct {
	code string // normalized 18-char credit code ("" if unusable)
	name string // normalized company name ("" if empty)
	doc  *domain.Supplier
}

// indexDoc pre-normalizes a document's credit code and company name.
func indexDoc(doc *domain.Supplier) dedupEntry {
	return dedupEntry{
		code: normalizeCreditCode(doc.BasicInfo.CreditCode),
		name: normalizeCompanyName(doc.BasicInfo.CompanyName),
		doc:  doc,
	}
}

func matchEntry(targetCode, targetName string, e dedupEntry) (DuplicateMatch, bool) {
	level, reason := "", ""
	switch {
	case targetCode != "" && e.code != "" && targetCode == e.code:
		level = MatchStrong
		reason = "统一社会信用代码与已有供应商一致（同一主体的确凿标识）"
	case targetName != "" && e.name != "" && targetName == e.name:
		level = MatchProbable
		reason = "公司名称与已有供应商一致（注册名称高度唯一，疑似同一主体）"
	default:
		return DuplicateMatch{}, false
	}

	doc := e.doc
	r := doc.BasicInfo.Region
	return DuplicateMatch{
		SupplierID: doc.ID,
		Name:       doc.BasicInfo.CompanyName,
		Province:   r.Province,
		City:       r.City,
		Status:     doc.Status,
		Level:      level,
		Reason:     reason,
		CreditCode: doc.BasicInfo.CreditCode,
	}, true
}

// loadDedupIndex walks the WHOLE library once (including archived and
// blacklisted records) and returns pre-normalized entries for bulk duplicate
// checks. Bulk import calls this a single time and then matches every row
// against the in-memory slice — avoiding one full table scan per row.
func (s *Service) loadDedupIndex(ctx context.Context) ([]dedupEntry, error) {
	entries := make([]dedupEntry, 0)
	cursor := ""
	for {
		page, err := s.store.List(ctx, datamodel.Query{
			Filter: datamodel.SupplierFilter{IncludeArchived: true},
			Limit:  datamodel.MaxPageSize,
			Cursor: cursor,
			Sort:   datamodel.Sort{Field: datamodel.SortCreatedAt, Order: datamodel.OrderDesc},
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range page.Items {
			entries = append(entries, indexDoc(doc))
		}
		if page.NextCursor == "" || len(entries) >= MaxExportDocs {
			break
		}
		cursor = page.NextCursor
	}
	return entries, nil
}

// rankAndSortMatches orders strong before probable and blacklisted first
// within a level, mirroring CheckDuplicates' ordering.
func rankAndSortMatches(matches []DuplicateMatch) {
	sort.SliceStable(matches, func(i, j int) bool {
		ri, rj := matchRank(matches[i]), matchRank(matches[j])
		if ri != rj {
			return ri < rj
		}
		return matches[i].Name < matches[j].Name
	})
}

// normalizeCreditCode trims and uppercases a credit code so formatting
// differences (lowercase x, surrounding spaces) don't defeat a match.
func normalizeCreditCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 18 {
		return "" // only well-formed codes are usable for the strong match
	}
	return s
}

// normalizeCompanyName strips whitespace and punctuation that doesn't change
// a registered company's identity, so e.g. "杭州一建（集团）有限公司" and
// "杭州一建集团有限公司" or spacing variants compare equal. Latin letters
// are lowercased.
func normalizeCompanyName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToLower(strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r',
			'(', ')', '（', '）', '[', ']', '【', '】',
			'·', '•', '.', '。', ',', '，', '、', '-', '—', '_', '/':
			return -1 // drop
		}
		return r
	}, s))
}
