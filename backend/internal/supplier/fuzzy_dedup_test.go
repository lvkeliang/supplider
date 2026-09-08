package supplier_test

// Tests for the low-confidence `possible` duplicate tier (名称相近): the
// conservative fuzzy-name rules that sit BELOW strong (credit code) and
// probable (identical normalized name) — abbreviation/full-name
// containment, homophone pinyin, and one-character typos. Possible matches
// are province-gated hints for a human; they never drive skip-on-import.

import (
	"context"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// findPossible runs the check and returns the possible-level match for the
// candidate name, if exactly one is expected.
func findPossible(t *testing.T, svc *supplier.Service, name, province string) []supplier.DuplicateMatch {
	t.Helper()
	ms, err := svc.CheckDuplicates(context.Background(), domain.BasicInfo{
		CompanyName: name,
		Region:      domain.Region{Province: province},
	})
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	return ms
}

func TestFuzzyContainmentShortForm(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	if _, err := svc.Create(ctx, dedupInput("杭州一建集团有限公司", "", "浙江", "杭州")); err != nil {
		t.Fatal(err)
	}

	// Short form of the same registered name → possible (containment).
	ms := findPossible(t, svc, "杭州一建", "浙江")
	if len(ms) != 1 || ms[0].Level != supplier.MatchPossible || ms[0].Reason == "" {
		t.Fatalf("want 1 possible containment match with reason, got %+v", ms)
	}

	// Two-character "short form" is too weak to warn on.
	if ms := findPossible(t, svc, "一建", "浙江"); len(ms) != 0 {
		t.Fatalf("2-rune short name must not match, got %+v", ms)
	}

	// Same trade name but a clearly different full name: the 4-rune core
	// covers only 4/14 (< 0.4 ratio) — must not warn (different entity).
	if ms := findPossible(t, svc, "杭州一建机械施工有限公司", "浙江"); len(ms) != 0 {
		t.Fatalf("low-ratio containment must not match, got %+v", ms)
	}
}

func TestFuzzyPinyinHomophone(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	if _, err := svc.Create(ctx, dedupInput("杭州一建建设有限公司", "", "浙江", "杭州")); err != nil {
		t.Fatal(err)
	}

	// 一建 / 亿建: different hanzi, identical pinyin (yijian) → possible.
	ms := findPossible(t, svc, "杭州亿建建设有限公司", "浙江")
	if len(ms) != 1 || ms[0].Level != supplier.MatchPossible {
		t.Fatalf("want 1 possible pinyin match, got %+v", ms)
	}

	// Province gate: same homophone in a different province → no hint.
	if ms := findPossible(t, svc, "杭州亿建建设有限公司", "江苏"); len(ms) != 0 {
		t.Fatalf("fuzzy match must be province-gated, got %+v", ms)
	}

	// Missing province on either side must not suppress the hint.
	ms = findPossible(t, svc, "杭州亿建建设有限公司", "")
	if len(ms) != 1 {
		t.Fatalf("unknown candidate province should still match, got %+v", ms)
	}
}

func TestFuzzyOneCharTypo(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	if _, err := svc.Create(ctx, dedupInput("杭州一建建设有限公司", "", "浙江", "杭州")); err != nil {
		t.Fatal(err)
	}

	// 建 → 井: different pinyin (jian/jing), exactly one rune differs.
	ms := findPossible(t, svc, "杭州一井建设有限公司", "浙江")
	if len(ms) != 1 || ms[0].Level != supplier.MatchPossible {
		t.Fatalf("want 1 possible edit-1 match, got %+v", ms)
	}

	// Two runes differ (一二 + 建井): edit distance 2 → no match.
	if ms := findPossible(t, svc, "杭州二井建设有限公司", "浙江"); len(ms) != 0 {
		t.Fatalf("2-rune difference must not match, got %+v", ms)
	}
}

func TestFuzzyDistinctCommonNamesDoNotMatch(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	if _, err := svc.Create(ctx, dedupInput("杭州建材贸易有限公司", "", "浙江", "杭州")); err != nil {
		t.Fatal(err)
	}
	// Shared "杭州建材" prefix but different industries — no containment,
	// different pinyin, far edit distance. A warning here would be noise
	// that erodes trust in the banner.
	if ms := findPossible(t, svc, "杭州建材机械有限公司", "浙江"); len(ms) != 0 {
		t.Fatalf("distinct common-prefix names must not match, got %+v", ms)
	}
}

func TestFuzzyPossibleRanksBelowStrong(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	code := validCreditCode("91330100MA27X0000")
	// Doc 1 shares the candidate's code AND full name; doc 2 is a fuzzy
	// containment target ("杭州一建" ⊂ "杭州一建集团").
	if _, err := svc.Create(ctx, dedupInput("杭州一建集团有限公司", code, "浙江", "杭州")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, dedupInput("杭州一建集团", "", "浙江", "杭州")); err != nil {
		t.Fatal(err)
	}

	ms, err := svc.CheckDuplicates(ctx, domain.BasicInfo{
		CompanyName: "杭州一建",
		CreditCode:  code,
		Region:      domain.Region{Province: "浙江"},
	})
	if err != nil {
		t.Fatal(err)
	}
	levels := map[string]bool{}
	for _, m := range ms {
		levels[m.Level] = true
	}
	if !levels[supplier.MatchStrong] || !levels[supplier.MatchPossible] {
		t.Fatalf("want strong + possible matches, got %+v", ms)
	}
	if ms[0].Level != supplier.MatchStrong {
		t.Fatalf("strong must rank before possible, got %+v", ms[0])
	}
}

func TestFuzzyPossibleWarnOnlyOnImportSkip(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	if _, err := svc.Create(ctx, dedupInput("杭州一建集团有限公司", "", "浙江", "杭州")); err != nil {
		t.Fatal(err)
	}

	// Possible-only row (short form) with skip=true: still CREATED, only a
	// warning is recorded — low confidence must not drop new companies.
	rep := svc.Import(ctx, []supplier.ImportItem{{
		Row: 1,
		Input: supplier.CreateInput{
			Owner: "u",
			BasicInfo: domain.BasicInfo{
				CompanyName: "杭州一建",
				Region:      domain.Region{Province: "浙江", City: "杭州"},
			},
		},
	}}, supplier.ImportOptions{SkipDuplicates: true})
	if rep.Created != 1 || rep.Skipped != 0 || len(rep.Duplicates) != 1 {
		t.Fatalf("possible row must be created with warning: %+v", rep)
	}
	if rep.Duplicates[0].Matches[0].Level != supplier.MatchPossible {
		t.Fatalf("warning level wrong: %+v", rep.Duplicates[0].Matches)
	}

	// Probable row (identical normalized name) with skip=true: skipped.
	rep = svc.Import(ctx, []supplier.ImportItem{{
		Row: 2,
		Input: supplier.CreateInput{
			Owner: "u",
			BasicInfo: domain.BasicInfo{
				CompanyName: "杭州一建集团（有限公司）",
				Region:      domain.Region{Province: "浙江", City: "杭州"},
			},
		},
	}}, supplier.ImportOptions{SkipDuplicates: true})
	if rep.Created != 0 || rep.Skipped != 1 {
		t.Fatalf("probable row must be skipped in skip mode: %+v", rep)
	}
}
