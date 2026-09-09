package search

import "testing"

func TestCJKConcatPlaceAndCategory(t *testing.T) {
	// Place and product live in different fields; the searchable blob
	// concatenates them (so they are not contiguous). This is the reported
	// real-world query "杭州混凝土" typed without a space.
	// Mirrors DocumentText: field blob PLUS synonym expansions (the
	// 混凝土 group contributes 商砼 / 砼 when 混凝土 is present).
	blob := "杭州顺通商品混凝土有限公司 浙江 杭州 建材 混凝土 商品混凝土 c30 商砼 砼"
	cases := []struct {
		query string
		want  bool
	}{
		{"杭州混凝土", true},  // 杭州 + 混凝土 across fields
		{"杭州建材", true},   // place + category
		{"杭州商砼", true},   // leading place + synonym token 商砼
		{"混凝土", true},    // ordinary contiguous term
		{"顺通", true},     // name fragment
		{"宁波混凝土", false}, // 混凝土 present but leading concept 宁波 absent
		{"上海混凝土", false},
		{"杭州有限公司", true},  // leading 杭州 + generic tail → that region's company
		{"北京有限公司", false}, // generic tail alone must not match out-of-region doc
	}
	for _, c := range cases {
		if got := AllTermsMatch(blob, c.query); got != c.want {
			t.Errorf("AllTermsMatch(%q) = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestCJKCoordinationShape(t *testing.T) {
	plan, ok := CJKCoordination("杭州混凝土")
	if !ok {
		t.Fatal("expected coordination for 5-rune Han run")
	}
	want := []string{"杭州", "州混", "混凝", "凝土"}
	if len(plan.Bigrams) != len(want) {
		t.Fatalf("bigrams = %v, want %v", plan.Bigrams, want)
	}
	for i := range want {
		if plan.Bigrams[i] != want[i] {
			t.Errorf("bigram[%d] = %q, want %q (all=%v)", i, plan.Bigrams[i], want[i], plan.Bigrams)
		}
	}
	if plan.MinHits != 2 {
		t.Errorf("minHits = %d, want 2", plan.MinHits)
	}
	if len(plan.Front) != 2 || plan.Front[0] != "杭州" {
		t.Errorf("front anchor = %v, want [杭州 州混]", plan.Front)
	}
	if plan.Back != "凝土" {
		t.Errorf("back anchor = %q, want 凝土", plan.Back)
	}
}

func TestCJKCoordinationIneligible(t *testing.T) {
	for _, term := range []string{
		"杭州",       // too short (exact substring handles it)
		"混凝土",      // 3 runes: exact trigram path
		"hangzhou", // Latin/pinyin: contiguous substring/pinyin column
		"hzyj",
		"c30混凝土",     // mixed, Han not a majority
		"91330106MA", // digits/letters
	} {
		if _, ok := CJKCoordination(term); ok {
			t.Errorf("CJKCoordination(%q) eligible, want not", term)
		}
	}
}

func TestCJKCoordinationASCIIFragments(t *testing.T) {
	// A Han-majority run with an ASCII code inside: the code is an
	// identifier fragment and must occur contiguously, never be spread
	// across bigrams.
	plan, ok := CJKCoordination("杭州混凝土c30")
	if !ok {
		t.Fatal("expected coordination for Han-majority mixed run")
	}
	if got := plan.ExactFrags; len(got) != 1 || got[0] != "c30" {
		t.Errorf("exact fragments = %v, want [c30]", got)
	}
	if plan.Back != "30" {
		t.Errorf("back anchor = %q, want 30", plan.Back)
	}

	// The shared-prefix regression: 205 companies named
	// "测试导出供应商NNN" searched with "…供应商001". Only docs containing
	// the contiguous fragment "001" may match.
	plan, ok = CJKCoordination("测试导出供应商001")
	if !ok {
		t.Fatal("expected coordination for Han-majority run with digit tail")
	}
	if got := plan.ExactFrags; len(got) != 1 || got[0] != "001" {
		t.Fatalf("exact fragments = %v, want [001]", got)
	}
	for _, c := range []struct {
		blob string
		want bool
	}{
		{"测试导出供应商001 浙江 杭州", true},  // exact contiguous anyway
		{"测试导出供应商0010 浙江 杭州", true}, // 001 is a prefix of 0010
		{"测试导出供应商010 浙江 杭州", false}, // digits reordered: "001" absent
		{"测试导出供应商101 浙江 杭州", false},
		{"测试导出供应商201 浙江 杭州", false},
	} {
		if got := TermMatchesText(c.blob, "测试导出供应商001"); got != c.want {
			t.Errorf("TermMatchesText(%q) = %v, want %v", c.blob, got, c.want)
		}
	}
}

func TestCJKBackAnchorRejectsSharedPrefix(t *testing.T) {
	// Pure-Han analogue of the digit-tail regression: a long shared
	// prefix with a different tail concept must not match.
	base := "杭州宏达混凝土有限公司 浙江 杭州 混凝土"
	if AllTermsMatch(base, "杭州宏达水泥") {
		t.Error(`tail "水泥" absent: shared-prefix bigrams must not satisfy the query`)
	}
	// Same prefix with the tail concept present matches.
	if !AllTermsMatch(base+" 水泥制品", "杭州宏达水泥") {
		t.Error("leading + trailing concepts present should match")
	}
}

func TestAllTermsANDWithCoordination(t *testing.T) {
	blob := "杭州一建集团有限公司 浙江 杭州 施工服务 建筑工程"
	if !AllTermsMatch(blob, "杭州 施工") {
		t.Error("two spaced terms both present must match (AND)")
	}
	if AllTermsMatch(blob, "杭州 混凝土") {
		t.Error("absent second term must fail even though first matches")
	}
	if AllTermsMatch(blob, "杭州施工 宁波") {
		t.Error("multi-term: an unrelated trailing term must fail")
	}
	// "杭州施工" itself concatenates place+category and coordinates.
	if !AllTermsMatch(blob, "杭州施工") {
		t.Error("concatenated place+category present across text must match")
	}
}

func TestGenericSuffixDoesNotMatchAcrossRegion(t *testing.T) {
	// A Shanghai company must not satisfy "杭州有限公司": tail bigrams
	// (有限/限公/公司) occur, but the front anchor 杭州 is absent.
	shanghai := "上海宏达水泥制品厂 上海 上海 建材 水泥制品 有限公司"
	if AllTermsMatch(shanghai, "杭州有限公司") {
		t.Error("generic 有限公司 suffix matched without the leading place")
	}
	// The same suffix with the correct leading place DOES match.
	hangzhou := "杭州宏达建材有限公司 浙江 杭州 建材"
	if !AllTermsMatch(hangzhou, "杭州有限公司") {
		t.Error("leading place + suffix should match the Hangzhou company")
	}
}
