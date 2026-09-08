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
//   - possible: names are merely SIMILAR — abbreviation vs full name
//     (containment), homophone typo (equal pinyin), or a one-character
//     typo. Low confidence, always phrased as "might be": these surface
//     for a human glance and never drive any automatic action.
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
	"unicode/utf8"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/search"
)

// Match levels.
const (
	MatchStrong   = "strong"   // 信用代码一致（确凿）
	MatchProbable = "probable" // 公司名称一致（高度疑似）
	MatchPossible = "possible" // 公司名称相近（低置信度，仅供人工判断）
)

// Fuzzy-name thresholds. Tuned conservatively: false negatives cost a
// manual re-scan, false positives cost trust in the warning banner.
const (
	// possibleContainMin: a short name this many runes may match via
	// containment ("杭州一建" ⊂ "杭州一建集团有限公司").
	possibleContainMin = 4
	// possibleContainRatio: short must cover at least this fraction of
	// long ("一建" ⊂ anything would be far too weak).
	possibleContainRatio = 0.4
	// possibleMinName: pinyin/edit rules only fire on names this long.
	possibleMinName = 6
	// possiblePinyinRatio: pinyin-equal names must be almost equal length.
	possiblePinyinRatio = 0.85
)

// DuplicateMatch is one existing supplier that looks like the candidate
// being entered.
type DuplicateMatch struct {
	SupplierID string `json:"supplier_id"`
	Name       string `json:"name"`
	Province   string `json:"province,omitempty"`
	City       string `json:"city,omitempty"`
	Status     string `json:"status"` // active | blacklisted | archived
	Level      string `json:"level"`  // strong | probable | possible
	Reason     string `json:"reason"` // 中文解释
	CreditCode string `json:"credit_code,omitempty"`
}

// CheckDuplicates scans the whole library (including archived/blacklisted)
// for suppliers that match the candidate's basic info. Returns strong
// matches first, then probable, then possible; within a level, blacklisted
// records rank first (safety) then name. Empty when the candidate has
// neither a usable credit code nor company name. Pure read — never blocks.
func (s *Service) CheckDuplicates(ctx context.Context, candidate domain.BasicInfo) ([]DuplicateMatch, error) {
	k := keyFrom(candidate)
	if k.code == "" && k.name == "" {
		return []DuplicateMatch{}, nil
	}
	// Both the interactive check and bulk import match against the same
	// one-pass, pre-normalized index (keys + pinyin computed once).
	index, err := s.loadDedupIndex(ctx)
	if err != nil {
		return nil, err
	}
	return findInDedupIndex(k, index), nil
}

// matchRank orders definitive (strong / credit-code) matches before
// probable (identical-name) ones, then possible (merely similar); within
// each level it surfaces a blacklisted (do-not-use) record first.
func matchRank(m DuplicateMatch) int {
	var r int
	switch m.Level {
	case MatchStrong:
		r = 0
	case MatchProbable:
		r = 2
	default: // possible
		r = 4
	}
	if m.Status != domain.StatusBlacklisted {
		r++ // non-blacklisted sorts after blacklisted within its level
	}
	return r
}

// matchKey is the pre-normalized identity used for matching. It is the same
// shape for a scanned library entry and the candidate being entered.
type matchKey struct {
	code     string // normalized 18-char credit code ("" if unusable)
	name     string // normalized company name ("" if empty)
	py       string // full pinyin of the Chinese name ("" if unusable)
	province string // trimmed province, for the fuzzy region gate
}

// dedupEntry is a library supplier keyed for bulk matching.
type dedupEntry struct {
	matchKey
	doc *domain.Supplier
}

// keyFrom normalizes a candidate's identity. Pinyin is computed only for
// names without Latin letters (go-pinyin drops Latin, so mixed names would
// compare on the Chinese remainder and collide spuriously).
func keyFrom(bi domain.BasicInfo) matchKey {
	name := normalizeCompanyName(bi.CompanyName)
	k := matchKey{
		code:     normalizeCreditCode(bi.CreditCode),
		name:     name,
		province: strings.TrimSpace(bi.Region.Province),
	}
	if name != "" && !strings.ContainsAny(name, "abcdefghijklmnopqrstuvwxyz0123456789") {
		k.py, _ = search.Pinyin(name)
	}
	return k
}

// indexDoc pre-normalizes a document's match keys.
func indexDoc(doc *domain.Supplier) dedupEntry {
	return dedupEntry{matchKey: keyFrom(doc.BasicInfo), doc: doc}
}

func matchEntry(k matchKey, e dedupEntry) (DuplicateMatch, bool) {
	level, reason := "", ""
	switch {
	case k.code != "" && e.code != "" && k.code == e.code:
		level = MatchStrong
		reason = "统一社会信用代码与已有供应商一致（同一主体的确凿标识）"
	case k.name != "" && e.name != "" && k.name == e.name:
		level = MatchProbable
		reason = "公司名称与已有供应商一致（注册名称高度唯一，疑似同一主体）"
	default:
		if r, ok := fuzzyNameMatch(k, e); ok {
			level, reason = MatchPossible, r
		} else {
			return DuplicateMatch{}, false
		}
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

// sameProvinceForFuzzy gates the low-confidence tier: similar names only
// count when the records are plausibly in the same province. An unknown
// province on either side never suppresses a match (missing data must not
// hide a warning). Strong/probable matches ignore region entirely.
func sameProvinceForFuzzy(k matchKey, e dedupEntry) bool {
	return k.province == "" || e.province == "" || k.province == e.province
}

// fuzzyNameMatch applies the three cheap, explainable possible-tier rules.
// All are O(n) in name length — the bulk importer runs this for every
// (row, library entry) pair, so there must be no quadratic DP here.
func fuzzyNameMatch(k matchKey, e dedupEntry) (string, bool) {
	if k.name == "" || e.name == "" || !sameProvinceForFuzzy(k, e) {
		return "", false
	}
	a, b := k.name, e.name

	// Rule 1 — one name contains the other (short/full form variants:
	// "杭州一建" vs "杭州一建集团有限公司").
	if r, ok := containmentReason(a, b); ok {
		return r, true
	}

	ar, br := utf8.RuneCountInString(a), utf8.RuneCountInString(b)
	short, long := ar, br
	if short > long {
		short, long = long, short
	}

	// Rule 2 — identical pinyin (homophone typo: 一建 vs 亿建).
	if k.py != "" && e.py != "" && short >= possibleMinName &&
		float64(short)/float64(long) >= possiblePinyinRatio && k.py == e.py {
		return "公司名称拼音完全一致（可能同音字误写），请人工确认是否同一主体", true
	}

	// Rule 3 — at most one rune substitution/insertion/deletion (笔误).
	if short >= possibleMinName && long-short <= 1 &&
		runeEditAtMost1(a, b) {
		return "公司名称仅一字之差（可能录入笔误），请人工确认是否同一主体", true
	}
	return "", false
}

// containmentReason reports whether one name is a short form of the other:
// the shorter contains-run into the longer, is at least
// possibleContainMin runes and covers ≥ possibleContainRatio of it.
func containmentReason(a, b string) (string, bool) {
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	sr, lr := utf8.RuneCountInString(short), utf8.RuneCountInString(long)
	if sr < possibleContainMin || sr == lr {
		return "", false
	}
	if float64(sr)/float64(lr) < possibleContainRatio {
		return "", false
	}
	if !strings.Contains(long, short) {
		return "", false
	}
	return "公司名称互为包含（可能是简称/全称差异，如漏写后缀），请人工确认是否同一主体", true
}

// runeEditAtMost1 reports whether two strings differ by at most one rune
// edit (substitution, insertion or deletion), using the linear two-pointer
// scan — caller guarantees |len difference| ≤ 1.
func runeEditAtMost1(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) && ra[i] == rb[j] {
		i++
		j++
	}
	if i == len(ra) || j == len(rb) {
		return true // only the (≤1-rune) length tail differs
	}
	switch {
	case len(ra) == len(rb):
		i++
		j++ // substitution
	case len(ra) > len(rb):
		i++ // insertion in a / deletion in b
	default:
		j++
	}
	for i < len(ra) && j < len(rb) && ra[i] == rb[j] {
		i++
		j++
	}
	return i == len(ra) && j == len(rb)
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

// findInDedupIndex matches one candidate against a pre-built index,
// returning matches ordered strong→probable→possible / blacklisted-first
// (same ordering as CheckDuplicates). Returns nil when the candidate has no
// usable identity.
func findInDedupIndex(k matchKey, index []dedupEntry) []DuplicateMatch {
	if k.code == "" && k.name == "" {
		return nil
	}
	matches := make([]DuplicateMatch, 0)
	for _, e := range index {
		if m, ok := matchEntry(k, e); ok {
			matches = append(matches, m)
		}
	}
	rankAndSortMatches(matches)
	if len(matches) > MaxExportDocs {
		matches = matches[:MaxExportDocs]
	}
	return matches
}

// rankAndSortMatches orders strong before probable before possible and
// blacklisted first within the level, mirroring CheckDuplicates' ordering.
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
