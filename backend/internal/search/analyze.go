// Chinese-aware text analysis shared by every search adapter.
//
// This package holds the search ENGINE PORT (Index, in search.go) plus the
// tokenization helpers any engine needs: query term splitting, pinyin
// romanization (so "hzyj" / "hangzhou" find 杭州一建 without Chinese input)
// and construction-industry synonyms (商砼 ↔ 混凝土). The SQLite FTS5
// adapter uses these today; the embedded-Meilisearch adapter will reuse
// the SAME functions — Chinese language behavior must not diverge across
// tiers.
package search

import (
	"sort"
	"strings"

	pinyin "github.com/mozillazg/go-pinyin"
)

// termPunct is stripped from the edges of each query term.
const termPunct = `,，。.、;；:：""''""!?！？()（）[]【】/\\"`

// SplitTerms splits a free-text keyword into normalized terms:
// whitespace-separated, lowercased, with surrounding punctuation stripped.
// Each term is matched independently with AND semantics. CJK phrases stay
// intact (e.g. "杭州 一建" is two terms; "杭州一建" is one) — downstream
// engines decide tokenization.
func SplitTerms(kw string) []string {
	raw := strings.Fields(strings.ToLower(strings.TrimSpace(kw)))
	terms := make([]string, 0, len(raw))
	for _, t := range raw {
		if t = strings.Trim(t, termPunct); t != "" {
			terms = append(terms, t)
		}
	}
	return terms
}

// Pinyin converts Chinese text into the two romanization forms used to
// augment search indexes:
//
//	full     - every hanzi's syllable concatenated, e.g. "杭州一建" →
//	           "hangzhouyijian". Emitted PER FIELD as one token, so trigram
//	           / edge-ngram indexes match any prefix-less substring
//	           ("hangzhou" ⊂ "hangzhouyijianyouxiangongsi").
//	initials - the first-letter sequence, e.g. "hzyj" (缩写输入).
//
// Non-hanzi characters are ignored. Returns empty strings for text
// without Chinese. Duo-yin-zi (多音字) use the most common reading.
func Pinyin(text string) (full, initials string) {
	a := pinyin.NewArgs() // default Style: Normal (syllables, no tones)
	groups := pinyin.Pinyin(text, a)
	var fb, ib strings.Builder
	for _, seg := range groups {
		if len(seg) == 0 || seg[0] == "" {
			continue
		}
		fb.WriteString(seg[0])
		ib.WriteByte(seg[0][0]) // syllables are ASCII letters
	}
	return fb.String(), ib.String()
}

// synonymGroups are terms treated as equivalent at INDEX time: when a
// document contains ANY member, the other members are appended to its
// searchable text, so searching "混凝土" finds suppliers who wrote
// "商砼" and vice-versa. Kept deliberately small and construction-focused;
// an admin-configurable thesaurus arrives with the small_business tier.
var synonymGroups = [][]string{
	{"商砼", "商品混凝土", "混凝土", "砼"},
	{"挖机", "挖掘机", "钩机"},
	{"吊机", "吊车", "起重机"},
}

// SynonymExpansions returns synonyms that a document containing text
// should be additionally indexed under: the members of a matched group
// that do NOT occur in the text as standalone terms.
//
// "Standalone" matters because group members nest inside each other:
// "砼" is a substring of "商砼" and "混凝土" of "商品混凝土". A naive
// Contains check would therefore treat a document mentioning only
// "商砼" as already containing "砼" and skip the expansion — wrong once
// a tokenizer segments words (Meilisearch) or a query term is shorter
// than the indexed phrase. So presence is tested on a copy of the text
// with all OTHER group members erased first (longest first; members that
// are substrings of the candidate are kept, so erasure cannot corrupt
// the candidate itself).
func SynonymExpansions(text string) []string {
	var out []string
	for _, g := range synonymGroups {
		// Order members longest-first so erasing a longer member also
		// removes the shorter members nested inside it.
		group := append([]string(nil), g...)
		sort.SliceStable(group, func(i, j int) bool {
			return len([]rune(group[i])) > len([]rune(group[j]))
		})

		present := make(map[string]bool, len(group))
		for _, w := range group {
			tmp := text
			for _, m := range group {
				if m == w || strings.Contains(w, m) {
					continue // keep w (and its own substrings) intact
				}
				tmp = strings.ReplaceAll(tmp, m, " ")
			}
			present[w] = strings.Contains(tmp, w)
		}

		any := false
		for _, w := range group {
			if present[w] {
				any = true
				break
			}
		}
		if !any {
			continue
		}
		// Emit expansions in the group's declared order (stable output).
		for _, w := range g {
			if !present[w] {
				out = append(out, w)
			}
		}
	}
	return out
}
