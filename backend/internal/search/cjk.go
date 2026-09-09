// Concatenated CJK query handling (无空格中文检索), shared by every search
// adapter so FTS5 (today) and Meilisearch/Elasticsearch (later) behave
// identically.
//
// Chinese users type natural queries WITHOUT word boundaries: the
// 地域+品类 pattern "杭州混凝土" is one run, but the document stores the
// place ("杭州") and the product ("混凝土") in different fields. A trigram
// phrase needs the whole run contiguous, so it matches nothing. We cannot
// ship a segmentation dictionary in the zero-dependency personal tier, so
// this file uses a dictionary-free heuristic: split the run into overlapping
// bigrams and accept a document when
//
//  1. enough of them occur ANYWHERE in the searchable text (coverage),
//  2. one of the run's LEADING bigrams occurs (front anchor — stops a generic
//     suffix such as 有限公司 matching on its own),
//  3. the run's TRAILING bigram occurs (back anchor — stops a long shared
//     prefix matching once the user's tail concept is absent), and
//  4. every ASCII letter/digit run inside the term occurs CONTIGUOUSLY
//     (codes such as "001" or "c30" are identifiers, never fuzzy-bigrammed).
package search

import (
	"strings"
	"unicode"
)

// cjkCoordMinRunes is the shortest space-free term eligible for bigram
// coordination. 1-3 rune terms already work as exact trigram/LIKE substrings.
const cjkCoordMinRunes = 4

// minExactFragLen is the shortest ASCII run treated as a literal fragment.
// A lone letter/digit is too weak (it occurs in most credit codes) and just
// rides along via bigram coverage.
const minExactFragLen = 2

// CJKPlan is the engine-agnostic coordination plan for one concatenated
// (space-free), predominantly Han query term.
type CJKPlan struct {
	// Bigrams are the term's distinct overlapping bigrams, in first-seen
	// order (a repeated syllable cannot inflate the hit count).
	Bigrams []string
	// MinHits is the minimum number of Bigrams the searchable text must
	// contain for the coverage part of the match.
	MinHits int
	// Front are leading bigrams of which AT LEAST ONE must occur.
	Front []string
	// Back is the trailing bigram, which MUST occur (empty only when the
	// term has no bigrams — i.e. coordination is ineligible).
	Back string
	// ExactFrags are maximal ASCII letter/digit runs (length ≥ 2) that must
	// each occur CONTIGUOUSLY in the searchable text: product codes,
	// sequence numbers and the like stay literal.
	ExactFrags []string
}

// CJKCoordination decomposes a single query term into its coordination plan.
// ok is false for short terms, Latin/pinyin/digit runs (those already match
// as contiguous substrings), or terms that are not predominantly Han.
func CJKCoordination(term string) (plan CJKPlan, ok bool) {
	runes := []rune(strings.ToLower(strings.TrimSpace(term)))
	if len(runes) < cjkCoordMinRunes {
		return CJKPlan{}, false
	}
	han := 0
	for _, r := range runes {
		if isHan(r) {
			han++
		}
	}
	// Require a strict Han majority so mixed runs like "c30混凝土" (exactly
	// half Han) keep their current contiguous/pinyin handling instead of
	// fuzzy-bigram matching.
	if han*2 <= len(runes) {
		return CJKPlan{}, false
	}

	seen := map[string]bool{}
	for i := 0; i+1 < len(runes); i++ {
		bg := string(runes[i : i+2])
		if !seen[bg] {
			seen[bg] = true
			plan.Bigrams = append(plan.Bigrams, bg)
		}
	}
	if len(plan.Bigrams) == 0 {
		return CJKPlan{}, false
	}
	// Require at least half the distinct bigrams (rounded up): 4 runes/3
	// bigrams → 2; 5/4 → 2; 6/5 → 3. Always ≥ 2 at the 4-rune threshold.
	plan.MinHits = (len(plan.Bigrams) + 1) / 2
	if plan.MinHits < 2 {
		plan.MinHits = 2
	}
	if len(plan.Bigrams) >= 2 {
		plan.Front = plan.Bigrams[:2]
	} else {
		plan.Front = plan.Bigrams
	}
	plan.Back = plan.Bigrams[len(plan.Bigrams)-1]
	plan.ExactFrags = asciiRuns(runes)
	return plan, true
}

// asciiRuns extracts maximal runs of ASCII letters/digits of length ≥ 2.
func asciiRuns(runes []rune) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() >= minExactFragLen {
			out = append(out, b.String())
		}
		b.Reset()
	}
	for _, r := range runes {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// TermMatchesText reports whether a single (already space-split, lowercased
// at call time) term matches the searchable text. It is the in-memory
// adapter's matcher and the reference behavior the SQL predicate mirrors:
// exact contiguous substring wins; otherwise a Han run matches via bigram
// coordination (coverage + front/back anchors + literal ASCII fragments).
// Both arguments are compared case-insensitively (CJK is unaffected; this
// also covers Latin substrings inside the blob).
func TermMatchesText(text, term string) bool {
	t := strings.ToLower(text)
	q := strings.ToLower(strings.TrimSpace(term))
	if q == "" {
		return true
	}
	if strings.Contains(t, q) {
		return true
	}

	plan, ok := CJKCoordination(term)
	if !ok {
		return false
	}
	hits := 0
	for _, bg := range plan.Bigrams {
		if strings.Contains(t, bg) {
			hits++
		}
	}
	if hits < plan.MinHits {
		return false
	}
	// Front anchor: the leading concept must be present — a generic tail
	// (有限公司 / 供应商) alone must never satisfy a long concatenated query.
	front := false
	for _, bg := range plan.Front {
		if strings.Contains(t, bg) {
			front = true
			break
		}
	}
	if !front {
		return false
	}
	// Back anchor: the user's final concept must also be present, so docs
	// sharing only a long prefix (same industry prefix, different number)
	// cannot satisfy the query.
	if plan.Back != "" && !strings.Contains(t, plan.Back) {
		return false
	}
	// ASCII identifier fragments stay literal.
	for _, frag := range plan.ExactFrags {
		if !strings.Contains(t, frag) {
			return false
		}
	}
	return true
}

// AllTermsMatch reports whether EVERY whitespace-separated term in the
// keyword matches (AND semantics), applying per-term bigram coordination.
// An empty keyword matches everything.
func AllTermsMatch(text, keyword string) bool {
	for _, term := range SplitTerms(keyword) {
		if !TermMatchesText(text, term) {
			return false
		}
	}
	return true
}

// isHan reports whether r is a CJK Unified Ideograph (basic + ext-A range
// covers essentially all company/place/product characters in practice).
func isHan(r rune) bool {
	return unicode.Is(unicode.Han, r)
}
