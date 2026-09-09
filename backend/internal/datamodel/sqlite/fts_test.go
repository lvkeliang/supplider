package sqlite_test

// FTS5 full-text search behavior (personal tier transition search
// engine): trigram substring matching over Chinese text, pinyin lookups
// (full syllables and initials), index-time synonyms, short-term LIKE
// fallback and index sync on updates. These complement the shared
// contract suite (which only pins naive keyword semantics).

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/search"
)

func ftsStore(t *testing.T) datamodel.SupplierStore {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "supplider.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func putSupplier(t *testing.T, st datamodel.SupplierStore, id, name string, opts ...func(*domain.Supplier)) *domain.Supplier {
	t.Helper()
	now := time.Now().UTC()
	doc := &domain.Supplier{
		ID:     id,
		Owner:  "u",
		Status: domain.StatusActive,
		BasicInfo: domain.BasicInfo{
			CompanyName: name,
			Region:      domain.Region{Province: "浙江", City: "杭州", District: "西湖区"},
		},
		Visibility: domain.VisSelf,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	for _, o := range opts {
		o(doc)
	}
	if err := st.Put(context.Background(), doc); err != nil {
		t.Fatalf("Put %s: %v", id, err)
	}
	return doc
}

func searchIDs(t *testing.T, st datamodel.SupplierStore, kw string) []string {
	t.Helper()
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Keyword: kw},
	})
	if err != nil {
		t.Fatalf("List keyword=%q: %v", kw, err)
	}
	ids := make([]string, 0, len(page.Items))
	for _, d := range page.Items {
		ids = append(ids, d.ID)
	}
	return ids
}

// TestFTSSetup indexes two suppliers with distinctive names.
func ftsFixture(t *testing.T, st datamodel.SupplierStore) {
	t.Helper()
	putSupplier(t, st, "sup_fts_1", "杭州一建建设有限公司",
		func(d *domain.Supplier) { d.Categories = []string{"施工服务"} })
	putSupplier(t, st, "sup_fts_2", "宁波建材贸易有限公司",
		func(d *domain.Supplier) {
			d.Categories = []string{"货物采购"}
			d.BasicInfo.BusinessScope = "水泥、砂石批发"
			d.BasicInfo.Region = domain.Region{Province: "浙江", City: "宁波", District: "鄞州区"}
		})
}

func TestFTSTrigramSubstring(t *testing.T) {
	st := ftsStore(t)
	ftsFixture(t, st)

	// ≥3-char Chinese substring (trigram MATCH).
	ids := searchIDs(t, st, "杭州一建")
	if len(ids) != 1 || ids[0] != "sup_fts_1" {
		t.Errorf(`"杭州一建" = %v, want [sup_fts_1]`, ids)
	}
	// Mid-name substring works without word boundaries.
	ids = searchIDs(t, st, "建材贸易")
	if len(ids) != 1 || ids[0] != "sup_fts_2" {
		t.Errorf(`"建材贸易" = %v, want [sup_fts_2]`, ids)
	}
}

func TestFTSShortTermLikeFallback(t *testing.T) {
	st := ftsStore(t)
	ftsFixture(t, st)

	// 2-char terms are below the trigram minimum and fall back to LIKE:
	// "水泥" lives in business_scope and MUST be findable.
	ids := searchIDs(t, st, "水泥")
	if len(ids) != 1 || ids[0] != "sup_fts_2" {
		t.Errorf(`"水泥" = %v, want [sup_fts_2] (short-term LIKE fallback)`, ids)
	}
	ids = searchIDs(t, st, "杭州")
	if len(ids) != 1 || ids[0] != "sup_fts_1" {
		t.Errorf(`"杭州" = %v, want [sup_fts_1]`, ids)
	}
}

func TestFTSPinyin(t *testing.T) {
	st := ftsStore(t)
	ftsFixture(t, st)

	// Full concatenated pinyin (substring of the per-field token).
	ids := searchIDs(t, st, "hangzhouyijian")
	if len(ids) != 1 || ids[0] != "sup_fts_1" {
		t.Errorf(`pinyin "hangzhouyijian" = %v, want [sup_fts_1]`, ids)
	}
	// Initial-letter abbreviation 杭州一建 → hzyj.
	ids = searchIDs(t, st, "hzyj")
	if len(ids) != 1 || ids[0] != "sup_fts_1" {
		t.Errorf(`initials "hzyj" = %v, want [sup_fts_1]`, ids)
	}
	// Case-insensitive Latin.
	ids = searchIDs(t, st, "NINGBO")
	if len(ids) != 1 || ids[0] != "sup_fts_2" {
		t.Errorf(`pinyin "NINGBO" = %v, want [sup_fts_2]`, ids)
	}
}

func TestFTSSynonyms(t *testing.T) {
	st := ftsStore(t)
	// Supplier writes 商砼; users search the standard term 混凝土
	// (3 chars → trigram MATCH over the synonym-augmented content).
	putSupplier(t, st, "sup_syn_1", "杭州商砼供应站",
		func(d *domain.Supplier) { d.BasicInfo.BusinessScope = "商砼生产与配送" })

	ids := searchIDs(t, st, "混凝土")
	if len(ids) != 1 || ids[0] != "sup_syn_1" {
		t.Errorf(`"混凝土" = %v, want [sup_syn_1] via synonym expansion`, ids)
	}
	// Reverse direction: searching the colloquial term also works.
	ids = searchIDs(t, st, "商品混凝土")
	if len(ids) != 1 || ids[0] != "sup_syn_1" {
		t.Errorf(`"商品混凝土" = %v, want [sup_syn_1]`, ids)
	}

	// A document written with the STANDARD term must be findable by the
	// 2-char colloquial "商砼" — below the trigram minimum, so this
	// exercises the LIKE fallback against the synonym-augmented
	// search_text (expansions live in BOTH index paths). sup_syn_1 also
	// matches (it literally says 商砼); assert the synonym-only doc hits.
	putSupplier(t, st, "sup_syn_2", "宁波混凝土制品厂",
		func(d *domain.Supplier) { d.BasicInfo.BusinessScope = "混凝土预制构件" })
	ids = searchIDs(t, st, "商砼")
	if !contains(ids, "sup_syn_2") {
		t.Errorf(`"商砼" = %v, want it to include sup_syn_2 via synonym in LIKE fallback`, ids)
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestFTSKeywordCombinedWithFilter(t *testing.T) {
	st := ftsStore(t)
	ftsFixture(t, st)

	// Keyword narrows via FTS; the structured city filter ANDs on top —
	// a Hangzhou pinyin match must not survive a Ningbo city filter.
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Keyword: "hangzhou", City: "宁波"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("FTS keyword + city filter = %d rows, want 0", len(page.Items))
	}

	page, err = st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Keyword: "宁波建材", City: "杭州"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// "宁波" is a 2-char LIKE term and "建材" is 2-char too; the Hangzhou
	// city filter excludes the only keyword match.
	if len(page.Items) != 0 {
		t.Errorf("short keyword + city filter = %d rows, want 0", len(page.Items))
	}
}

// TestFTSConcatCJKAcrossFields covers space-free Chinese queries whose
// concepts live in DIFFERENT document fields (region columns vs category
// /product text), e.g. the real-world "杭州混凝土". The trigram phrase
// cannot be contiguous; bigram coordination must find it.
func TestFTSConcatCJKAcrossFields(t *testing.T) {
	st := ftsStore(t)
	// Company name omits the city; 杭州 exists only in the region fields.
	putSupplier(t, st, "sup_cc_1", "顺通商品混凝土有限公司",
		func(d *domain.Supplier) {
			d.Categories = []string{"建材"}
			d.ProductsServices = []domain.ProductService{{Name: "混凝土c30配送"}}
		})
	putSupplier(t, st, "sup_cc_2", "海达商砼站",
		func(d *domain.Supplier) {
			d.Categories = []string{"建材"}
			d.BasicInfo.Region = domain.Region{Province: "浙江", City: "宁波", District: "鄞州区"}
		})

	if ids := searchIDs(t, st, "杭州混凝土"); len(ids) != 1 || ids[0] != "sup_cc_1" {
		t.Errorf(`"杭州混凝土" = %v, want [sup_cc_1] (place+product in different fields)`, ids)
	}
	if ids := searchIDs(t, st, "杭州商砼"); len(ids) != 1 || ids[0] != "sup_cc_1" {
		t.Errorf(`"杭州商砼" = %v, want [sup_cc_1] (synonym expansion + place)`, ids)
	}
	if ids := searchIDs(t, st, "宁波混凝土"); len(ids) != 1 || ids[0] != "sup_cc_2" {
		t.Errorf(`"宁波混凝土" = %v, want [sup_cc_2]`, ids)
	}
	// Generic product alone must not drag in the other city's supplier.
	if ids := searchIDs(t, st, "杭州建材"); !sameIDs(ids, "sup_cc_1") {
		t.Errorf(`"杭州建材" = %v, want only [sup_cc_1]`, ids)
	}
}

// TestFTSConcatCJKASCIIIdentifier pins the precision fix: a Han company-name
// prefix shared by every document must NOT make an ASCII sequence-number
// suffix go fuzzy. "…供应商001" may match only rows containing "001"
// contiguously (001 and its 001x variants) — never 010/101/201.
func TestFTSConcatCJKASCIIIdentifier(t *testing.T) {
	st := ftsStore(t)
	for _, n := range []string{"000", "001", "0010", "0011", "010", "101", "201"} {
		putSupplier(t, st, "sup_seq_"+n, "测试导出供应商"+n)
	}
	ids := searchIDs(t, st, "测试导出供应商001")
	for _, bad := range []string{"sup_seq_000", "sup_seq_010", "sup_seq_101", "sup_seq_201"} {
		if contains(ids, bad) {
			t.Errorf(`"…供应商001" = %v, must not include %s (ASCII suffix went fuzzy)`, ids, bad)
		}
	}
	for _, good := range []string{"sup_seq_001", "sup_seq_0010", "sup_seq_0011"} {
		if !contains(ids, good) {
			t.Errorf(`"…供应商001" = %v, must include %s (contiguous 001)`, ids, good)
		}
	}
}

// TestFTSCoordinationParityWithReferenceMatcher is the anti-divergence
// guarantee: for every coordination-eligible term the SQL predicate built
// by keywordPredicates must return EXACTLY the documents for which the
// engine-agnostic matcher search.AllTermsMatch(search.DocumentText(doc))
// returns true. The future Meilisearch adapter inherits the same reference.
func TestFTSCoordinationParityWithReferenceMatcher(t *testing.T) {
	st := ftsStore(t)
	docs := []*domain.Supplier{
		// name without place + region fields + product in a separate field
		{
			ID: "p_1", Owner: "u", Status: domain.StatusActive, Visibility: domain.VisSelf,
			BasicInfo: domain.BasicInfo{
				CompanyName: "顺通商品混凝土有限公司",
				Region:      domain.Region{Province: "浙江", City: "杭州", District: "西湖区"},
			},
			Categories:       []string{"建材"},
			ProductsServices: []domain.ProductService{{Name: "混凝土c30配送"}},
		},
		{
			ID: "p_2", Owner: "u", Status: domain.StatusActive, Visibility: domain.VisSelf,
			BasicInfo: domain.BasicInfo{
				CompanyName: "海达商砼站",
				Region:      domain.Region{Province: "浙江", City: "宁波", District: "鄞州区"},
			},
			Categories: []string{"建材"},
		},
		{
			ID: "p_3", Owner: "u", Status: domain.StatusActive, Visibility: domain.VisSelf,
			BasicInfo: domain.BasicInfo{
				CompanyName: "杭州一建集团有限公司",
				Region:      domain.Region{Province: "浙江", City: "杭州", District: "余杭区"},
			},
			Categories: []string{"施工服务"},
			CustomFields: map[string]any{
				"资质等级": "施工总承包一级",
			},
		},
		{
			ID: "p_4", Owner: "u", Status: domain.StatusActive, Visibility: domain.VisSelf,
			BasicInfo: domain.BasicInfo{
				CompanyName: "测试导出供应商010",
				Region:      domain.Region{Province: "浙江", City: "杭州", District: "西湖区"},
			},
		},
	}
	now := time.Now().UTC()
	ctx := context.Background()
	for _, d := range docs {
		d.CreatedAt, d.UpdatedAt = now, now
		if err := st.Put(ctx, d); err != nil {
			t.Fatalf("Put %s: %v", d.ID, err)
		}
	}

	queries := []string{
		"杭州混凝土",      // cross-field place+product
		"杭州商砼",       // cross-field via synonym expansion
		"杭州建材",       // place+category
		"宁波混凝土",      // product present, wrong place
		"杭州有限公司",     // leading place + generic suffix
		"北京有限公司",     // generic suffix without the place
		"杭州一建",       // contiguous in name (exact path)
		"杭州施工总承包",    // name place + custom-field concept
		"杭州混凝土c30",   // with literal ASCII fragment present
		"杭州混凝土c40",   // …fragment absent
		"测试导出供应商001", // shared Han prefix, digit tail mismatch
		"杭州 混凝土",     // two spaced terms (AND)
		"杭州 宁波",      // contradictory spaced terms
	}
	for _, kw := range queries {
		// Parity holds as long as no term can be answered ONLY via the pinyin
		// column (the Go reference matcher models DocumentText, not py):
		// coordination-eligible terms are Han-majority (their exact MATCH
		// cannot hit ASCII pinyin tokens), pure-Han terms cannot either, and
		// sub-3-rune terms use LIKE on search_text directly.
		for _, term := range search.SplitTerms(kw) {
			if _, eligible := search.CJKCoordination(term); eligible {
				continue
			}
			allHan := true
			for _, r := range term {
				if !(r >= 0x4E00 && r <= 0x9FFF) {
					allHan = false
				}
			}
			// ≥3-rune non-Han terms (pinyin/credit codes) may match via the
			// py column only; the Go reference does not model py, so such
			// terms belong in TestFTSPinyin, not this parity matrix.
			if utf8.RuneCountInString(term) >= 3 && !allHan {
				t.Fatalf("test matrix drift: %q contains ASCII term %q that may match via py only", kw, term)
			}
		}

		page, err := st.List(ctx, datamodel.Query{Filter: datamodel.SupplierFilter{Keyword: kw}})
		if err != nil {
			t.Fatalf("List %q: %v", kw, err)
		}
		got := map[string]bool{}
		for _, d := range page.Items {
			got[d.ID] = true
		}
		want := map[string]bool{}
		for _, d := range docs {
			if search.AllTermsMatch(search.DocumentText(d), kw) {
				want[d.ID] = true
			}
		}
		if !sameSet(got, want) {
			t.Errorf("keyword %q: SQL hits %s, reference matcher %s", kw, keys(got), keys(want))
		}
	}
}

func sameIDs(ids []string, want ...string) bool {
	if len(ids) != len(want) {
		return false
	}
	for i := range want {
		if ids[i] != want[i] {
			return false
		}
	}
	return true
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestFTSIndexSyncsOnUpdate(t *testing.T) {
	st := ftsStore(t)
	doc := putSupplier(t, st, "sup_upd_1", "旧名称土石方工程队")

	// Rename via Put (upsert): old name stops matching, new name starts.
	doc.BasicInfo.CompanyName = "新名称智能科技有限公司"
	doc.UpdatedAt = doc.UpdatedAt.Add(time.Second)
	if err := st.Put(context.Background(), doc); err != nil {
		t.Fatalf("Put update: %v", err)
	}
	if ids := searchIDs(t, st, "旧名称土石方"); len(ids) != 0 {
		t.Errorf(`old name still indexed: %v`, ids)
	}
	if ids := searchIDs(t, st, "智能科技"); len(ids) != 1 || ids[0] != "sup_upd_1" {
		t.Errorf(`new name not indexed: %v`, ids)
	}
}

func TestFTSBackfillOnReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supplider.db")

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	putSupplier(t, st, "sup_bf_1", "温州回填测试建设公司")
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: the backfill path runs at Open; keyword search must work.
	st2, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open reopen: %v", err)
	}
	defer st2.Close()
	if ids := searchIDs(t, st2, "回填测试"); len(ids) != 1 || ids[0] != "sup_bf_1" {
		t.Errorf("after reopen keyword = %v, want [sup_bf_1]", ids)
	}
}

// TestFTSPathologicalKeywordsNeverErrors pins the phrase-quoting guarantee:
// raw user input containing FTS5 syntax characters (", *, parens, boolean
// operators, colons, LIKE wildcards) must never make the MATCH expression
// raise a syntax error (which would 500 the list/search endpoint). Terms are
// bound as double-quoted phrases; an unmatchable phrase just yields zero rows.
func TestFTSPathologicalKeywordsNeverErrors(t *testing.T) {
	st := ftsStore(t)
	ctx := context.Background()
	putSupplier(t, st, "sup_ftsp", "杭州混凝土有限公司", func(d *domain.Supplier) {
		d.BasicInfo.Region = domain.Region{Province: "浙江", City: "杭州"}
	})

	for _, kw := range []string{
		`"`, `*`, `**`, `(`, `)`, `()`, `a"b`, `杭"州`, `杭州"混凝土`,
		`OR`, `NEAR`, `AND NOT`, `杭州 OR`, `"杭州`, `杭州"`, `%`, `_`,
		`a*`, `:`, `[`, `{`, `\`, `混"凝"土`, "a\tb", `a:b`,
	} {
		_, err := st.List(ctx, datamodel.Query{
			Filter: datamodel.SupplierFilter{Keyword: kw},
			Limit:  10,
		})
		if err != nil {
			t.Errorf("keyword %q made List fail: %v", kw, err)
		}
	}

	// The same quoting must not break a normal long-term search.
	if ids := searchIDs(t, st, "混凝土"); len(ids) != 1 || ids[0] != "sup_ftsp" {
		t.Errorf("normal keyword after pathological probes = %v", ids)
	}
}
