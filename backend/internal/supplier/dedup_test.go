package supplier_test

// Tests for non-AI duplicate detection (录入去重): strong match on credit
// code, probable match on normalized company name, punctuation/space
// tolerance, and the safety case where a BLACKLISTED supplier is matched.

import (
	"context"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// dedupInput builds a minimal create input with the given name/code/region.
func dedupInput(name, code, prov, city string) supplier.CreateInput {
	in := supplier.CreateInput{
		Owner: "u",
		BasicInfo: domain.BasicInfo{
			CompanyName: name,
			Region:      domain.Region{Province: prov, City: city},
		},
		Categories: []string{"测试"},
		Visibility: domain.VisSelf,
	}
	if code != "" {
		in.BasicInfo.CreditCode = code
	}
	return in
}

func TestDuplicateStrongOnCreditCode(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	code := validCreditCode("91330100MA27X0000")
	if _, err := svc.Create(ctx, dedupInput("杭州一建有限公司", code, "浙江", "杭州")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Same code, even a differently-typed name/region, must strongly match.
	matches, err := svc.CheckDuplicates(ctx, domain.BasicInfo{
		CompanyName: "完全不同的名称",
		CreditCode:  " " + strings.ToLower(code) + " ", // trimmed + uppercased
	})
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if len(matches) != 1 || matches[0].Level != supplier.MatchStrong {
		t.Fatalf("want 1 strong match, got %+v", matches)
	}
}

func TestDuplicateProbableOnNormalizedName(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	if _, err := svc.Create(ctx, dedupInput("杭州一建（集团）有限公司", "", "浙江", "杭州")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Punctuation/spacing variant of the same registered name.
	matches, err := svc.CheckDuplicates(ctx, domain.BasicInfo{
		CompanyName: " 杭州一建集团有限公司 ",
		Region:      domain.Region{Province: "浙江", City: "杭州"},
	})
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if len(matches) != 1 || matches[0].Level != supplier.MatchProbable {
		t.Fatalf("want 1 probable name match, got %+v", matches)
	}

	// A genuinely different name does not match.
	none, err := svc.CheckDuplicates(ctx, domain.BasicInfo{CompanyName: "宁波完全不同的贸易公司"})
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("different name wrongly matched: %+v", none)
	}
}

func TestDuplicateIncludesBlacklistedAndArchived(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	code := validCreditCode("91330100MA27X0000")
	fraud, err := svc.Create(ctx, dedupInput("失信建材有限公司", code, "江苏", "南京"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.Blacklist(ctx, fraud.ID, "资质造假"); err != nil {
		t.Fatalf("blacklist: %v", err)
	}

	// Re-onboarding the blacklisted fraudster by credit code must surface
	// the match AND its blacklisted status, ranked first.
	matches, err := svc.CheckDuplicates(ctx, domain.BasicInfo{
		CompanyName: "失信建材有限公司",
		CreditCode:  code,
	})
	if err != nil {
		t.Fatalf("CheckDuplicates: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %+v", matches)
	}
	m := matches[0]
	if m.Status != domain.StatusBlacklisted || m.Level != supplier.MatchStrong {
		t.Errorf("blacklisted match should be strong + carry blacklist status: %+v", m)
	}

	// Archived suppliers are still matched (history retained, re-add guarded).
	arch, _ := svc.Create(ctx, dedupInput("旧供应商有限公司", "", "浙江", "温州"))
	if err := svc.Archive(ctx, arch.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	matches2, _ := svc.CheckDuplicates(ctx, domain.BasicInfo{CompanyName: "旧供应商有限公司"})
	if len(matches2) != 1 || matches2[0].Status != domain.StatusArchived {
		t.Errorf("archived supplier should still match: %+v", matches2)
	}
}

func TestDuplicateEmptyCandidate(t *testing.T) {
	svc := supplier.NewService(memory.New())
	_, _ = svc.Create(context.Background(), dedupInput("某公司有限公司", "", "浙江", "杭州"))
	matches, err := svc.CheckDuplicates(context.Background(), domain.BasicInfo{})
	if err != nil || len(matches) != 0 {
		t.Errorf("empty candidate should yield no matches, got %+v err=%v", matches, err)
	}
}
