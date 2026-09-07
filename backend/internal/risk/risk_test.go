package risk_test

// Tests for the non-AI shell-company rules. Pure function, frozen clock;
// the same expectations hold on every tier because the engine contains no
// infrastructure dependencies.

import (
	"strings"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/risk"
)

var now = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func baseSupplier() *domain.Supplier {
	return &domain.Supplier{
		Status: domain.StatusActive,
		BasicInfo: domain.BasicInfo{
			CompanyName:       "杭州一建有限公司",
			CreditCode:        "91330100MA27X0000X", // checksum fixed by validCode below
			LegalPerson:       "张三",
			ContactPhone:      "13800000000",
			RegisteredCapital: "5000万人民币",
			BusinessScope:     "房屋建筑工程施工",
			EstablishmentDate: "2010-05-01",
			SupplierType:      "施工商",
			Region:            domain.Region{Province: "浙江", City: "杭州"},
		},
		Qualifications: []domain.Qualification{{Type: "建筑工程施工总承包", Level: "一级"}},
	}
}

// validCode computes a checksum-correct 18-char code from any 17-char
// prefix, so tests use a known-good code without memorizing a real one.
func validCode(prefix17 string) string {
	// Expose the algorithm via CheckCreditCode round-trip: brute force the
	// last char over the GB alphabet.
	const alphabet = "0123456789ABCDEFGHJKLMNPQRTUWXY"
	for _, c := range alphabet {
		cand := prefix17 + string(c)
		if risk.CheckCreditCode(cand) {
			return cand
		}
	}
	panic("no checksum found")
}

func hasSignal(rep risk.Report, code string) bool {
	for _, s := range rep.Signals {
		if s.Code == code {
			return true
		}
	}
	return false
}

func TestCleanSupplierNoSignals(t *testing.T) {
	s := baseSupplier()
	s.BasicInfo.CreditCode = validCode("91330100MA27X0000")
	rep := risk.Evaluate(s, now)
	if len(rep.Signals) != 0 {
		t.Errorf("clean supplier fired signals: %+v", rep.Signals)
	}
	if rep.ShellRisk {
		t.Error("clean supplier marked shell risk")
	}
}

func TestCreditCodeChecksum(t *testing.T) {
	// Round-trip: a correctly computed checksum passes.
	good := validCode("91330100MA27X0000")
	if !risk.CheckCreditCode(good) {
		t.Fatalf("checksum-valid code %q rejected", good)
	}
	// Flip the last character → checksum fails.
	bad := good[:17] + "0"
	if good[17] == '0' {
		bad = good[:17] + "1"
	}
	if risk.CheckCreditCode(bad) {
		t.Fatalf("tampered code %q accepted", bad)
	}

	// Charset-valid but checksum-wrong (flip the last char of a valid code).
	s := baseSupplier()
	tampered := bad
	rep := risk.Evaluate(s, now)
	_ = tampered
	s.BasicInfo.CreditCode = bad
	rep = risk.Evaluate(s, now)
	if !hasSignal(rep, "R103") {
		t.Errorf("fabricated code did not fire R103: %+v", rep.Signals)
	}
	if !rep.ShellRisk {
		t.Error("checksum failure (high) must set shell risk")
	}

	// Illegal characters (contains S and I, both excluded) → R102.
	s.BasicInfo.CreditCode = "91330100MA2TEST001"
	rep = risk.Evaluate(s, now)
	if !hasSignal(rep, "R102") {
		t.Errorf("illegal-char code did not fire R102: %+v", rep.Signals)
	}

	// Wrong length → R102.
	s.BasicInfo.CreditCode = "123"
	rep = risk.Evaluate(s, now)
	if !hasSignal(rep, "R102") {
		t.Errorf("short code did not fire R102: %+v", rep.Signals)
	}

	// Missing → R101 (low, never shell risk on its own).
	s.BasicInfo.CreditCode = ""
	rep = risk.Evaluate(s, now)
	if !hasSignal(rep, "R101") || rep.ShellRisk {
		t.Errorf("missing code: R101=%v shell=%v", hasSignal(rep, "R101"), rep.ShellRisk)
	}
}

func TestYoungThinConstructionlessAndCapital(t *testing.T) {
	// Young company + thin profile + no quals → multiple mediums → shell.
	s := baseSupplier()
	s.BasicInfo.CreditCode = validCode("91330100MA27X0000")
	s.BasicInfo.EstablishmentDate = now.AddDate(0, 0, -30).Format("2006-01-02") // 30 days old
	s.BasicInfo.LegalPerson = ""
	s.BasicInfo.RegisteredCapital = ""
	s.BasicInfo.BusinessScope = ""
	s.Qualifications = nil
	rep := risk.Evaluate(s, now)

	for _, want := range []string{"R201", "R202", "R203"} {
		if !hasSignal(rep, want) {
			t.Errorf("want signal %s, got: %+v", want, rep.Signals)
		}
	}
	if !rep.ShellRisk {
		t.Error("young + thin + no-qualifications supplier must be shell risk")
	}
	if notes := rep.Notes(); !strings.Contains(notes, "R201") {
		t.Errorf("notes should carry medium/high signals, got %q", notes)
	}
}

func TestLowCapital(t *testing.T) {
	s := baseSupplier()
	s.BasicInfo.CreditCode = validCode("91330100MA27X0000")
	s.BasicInfo.RegisteredCapital = "50万元"
	rep := risk.Evaluate(s, now)
	if !hasSignal(rep, "R301") {
		t.Errorf("50万 capital did not fire R301: %+v", rep.Signals)
	}
	if rep.ShellRisk {
		t.Error("low capital alone (low severity) must not set shell risk")
	}
}

func TestDuplicateQualifications(t *testing.T) {
	s := baseSupplier()
	s.BasicInfo.CreditCode = validCode("91330100MA27X0000")
	// Same type+level entered twice (no cert numbers) → R204.
	s.Qualifications = []domain.Qualification{
		{Type: "建筑工程施工总承包", Level: "一级"},
		{Type: "建筑工程施工总承包", Level: "一级"},
	}
	rep := risk.Evaluate(s, now)
	if !hasSignal(rep, "R204") {
		t.Errorf("duplicate type+level did not fire R204: %+v", rep.Signals)
	}

	// Distinct levels of the same type are NOT duplicates.
	s.Qualifications = []domain.Qualification{
		{Type: "建筑工程施工总承包", Level: "一级"},
		{Type: "建筑工程施工总承包", Level: "二级"},
	}
	if rep = risk.Evaluate(s, now); hasSignal(rep, "R204") {
		t.Errorf("distinct levels wrongly fired R204: %+v", rep.Signals)
	}

	// Shared certificate number fires even when type/level differ.
	s.Qualifications = []domain.Qualification{
		{Type: "建筑工程施工总承包", Level: "一级", CertNo: "JZ-001"},
		{Type: "市政公用工程施工总承包", Level: "二级", CertNo: "JZ-001"},
	}
	if rep = risk.Evaluate(s, now); !hasSignal(rep, "R204") {
		t.Errorf("duplicate cert number did not fire R204: %+v", rep.Signals)
	}
}

func TestNonConstructionSupplierWithoutQuals(t *testing.T) {
	s := baseSupplier()
	s.BasicInfo.CreditCode = validCode("91330100MA27X0000")
	s.BasicInfo.SupplierType = "贸易商"
	s.Categories = []string{"建材贸易"}
	s.Qualifications = nil
	rep := risk.Evaluate(s, now)
	if hasSignal(rep, "R203") {
		t.Error("trade supplier must not fire construction-without-quals rule")
	}
}
