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

	sort.SliceStable(matches, func(i, j int) bool {
		ri, rj := matchRank(matches[i]), matchRank(matches[j])
		if ri != rj {
			return ri < rj
		}
		return matches[i].Name < matches[j].Name
	})
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
	existingCode := normalizeCreditCode(doc.BasicInfo.CreditCode)
	existingName := normalizeCompanyName(doc.BasicInfo.CompanyName)

	level, reason := "", ""
	switch {
	case targetCode != "" && existingCode != "" && targetCode == existingCode:
		level = MatchStrong
		reason = "统一社会信用代码与已有供应商一致（同一主体的确凿标识）"
	case targetName != "" && existingName != "" && targetName == existingName:
		level = MatchProbable
		reason = "公司名称与已有供应商一致（注册名称高度唯一，疑似同一主体）"
	default:
		return DuplicateMatch{}, false
	}

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
