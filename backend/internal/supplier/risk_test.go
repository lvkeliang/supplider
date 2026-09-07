package supplier_test

// Tests for the lifecycle wiring of the non-AI shell-company rule engine:
// risk_flags is derived on every Create/Update (like rating), the live
// review scan flags risky active suppliers only, and external (higher-tier)
// risk signals survive re-evaluation.

import (
	"context"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/risk"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// validCreditCode brute-forces the GB 32100-2015 checksum character for a
// 17-char prefix so tests use a known-valid code without hard-coding one.
func validCreditCode(prefix17 string) string {
	const alphabet = "0123456789ABCDEFGHJKLMNPQRTUWXY"
	for _, c := range alphabet {
		cand := prefix17 + string(c)
		if risk.CheckCreditCode(cand) {
			return cand
		}
	}
	panic("no checksum found")
}

// cleanInput is a fully-populated supplier that fires NO local rule
// (mirrors internal/risk's clean baseline).
func cleanInput() supplier.CreateInput {
	return supplier.CreateInput{
		Owner: "user_001",
		BasicInfo: domain.BasicInfo{
			CompanyName:       "杭州一建有限公司",
			CreditCode:        validCreditCode("91330100MA27X0000"),
			LegalPerson:       "张三",
			ContactPhone:      "13800000000",
			RegisteredCapital: "5000万人民币",
			BusinessScope:     "房屋建筑工程施工",
			EstablishmentDate: "2010-05-01",
			SupplierType:      "施工商",
			Region:            domain.Region{Province: "浙江", City: "杭州"},
		},
		Qualifications: []domain.Qualification{{Type: "建筑工程施工总承包", Level: "一级"}},
		Categories:     []string{"施工服务"},
		Visibility:     domain.VisSelf,
	}
}

// tamperedCode returns a checksum-INVALID 18-char code (charset-legal but
// the check digit is wrong) → the R103 high-severity rule.
func tamperedCode() string {
	good := validCreditCode("91330100MA27X0000")
	bad := good[:17] + "0"
	if good[17] == '0' {
		bad = good[:17] + "1"
	}
	return bad
}

func TestCreateCleanSupplierNotFlagged(t *testing.T) {
	svc := supplier.NewService(memory.New())
	doc, err := svc.Create(context.Background(), cleanInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if doc.RiskFlags.ShellRisk {
		t.Errorf("clean supplier flagged shell risk; notes=%q", doc.RiskFlags.Notes)
	}

	// The denormalized flag reaches the list summary.
	page, err := svc.List(context.Background(), datamodel.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ShellRisk {
		t.Errorf("summary shell_risk = %v, want false", page.Items)
	}
}

func TestCreateBadCreditCodeFlagged(t *testing.T) {
	svc := supplier.NewService(memory.New())
	in := cleanInput()
	in.BasicInfo.CreditCode = tamperedCode()
	doc, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !doc.RiskFlags.ShellRisk {
		t.Error("checksum-invalid credit code must set shell risk")
	}

	// Live report carries the explainable R103 signal.
	rep, err := svc.RiskReport(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("RiskReport: %v", err)
	}
	found := false
	for _, sig := range rep.Signals {
		if sig.Code == "R103" {
			found = true
		}
	}
	if !found {
		t.Errorf("R103 missing from live report: %+v", rep.Signals)
	}
}

func TestUpdateReEvaluatesRisk(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	doc, _ := svc.Create(ctx, cleanInput())

	// Edit to a bad credit code → the verdict must flip to risky.
	bad := cleanInput().BasicInfo
	bad.CreditCode = tamperedCode()
	upd, err := svc.Update(ctx, doc.ID, supplier.UpdateInput{BasicInfo: &bad})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !upd.RiskFlags.ShellRisk {
		t.Error("risk not re-evaluated after update (still clean)")
	}

	// ...and fixing it flips back to clear.
	good := cleanInput().BasicInfo
	upd, err = svc.Update(ctx, doc.ID, supplier.UpdateInput{BasicInfo: &good})
	if err != nil {
		t.Fatalf("Update fix: %v", err)
	}
	if upd.RiskFlags.ShellRisk {
		t.Error("risk did not clear after correcting the code")
	}
}

func TestShellRiskScanFlagsActiveOnly(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	clean, _ := svc.Create(ctx, cleanInput())
	riskyIn := cleanInput()
	riskyIn.BasicInfo.CreditCode = tamperedCode()
	risky, _ := svc.Create(ctx, riskyIn)

	items, err := svc.ShellRiskSuppliers(ctx)
	if err != nil {
		t.Fatalf("ShellRiskSuppliers: %v", err)
	}
	if len(items) != 1 || items[0].SupplierID != risky.ID {
		t.Fatalf("scan = %+v, want only the risky supplier", items)
	}
	if items[0].HighCount != 1 {
		t.Errorf("high_count = %d, want 1 (R103)", items[0].HighCount)
	}

	// Archiving removes it from the live review queue (history retained).
	if err := svc.Archive(ctx, risky.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	items, _ = svc.ShellRiskSuppliers(ctx)
	if len(items) != 0 {
		t.Errorf("archived risky supplier still in queue: %+v", items)
	}
	_ = clean
}

func TestCheckRisksPreservesExternalSignals(t *testing.T) {
	st := memory.New()
	svc := supplier.NewService(st)
	ctx := context.Background()
	doc, _ := svc.Create(ctx, cleanInput())

	// Simulate a higher-tier external signal (被执行人 via 企查查 API),
	// which the local engine never writes.
	doc.RiskFlags.ExecutedPerson = true
	if err := st.Put(ctx, doc); err != nil {
		t.Fatalf("seed external flag: %v", err)
	}

	// Re-running the local rules must not clobber the external flag.
	if _, err := svc.CheckRisks(ctx, doc.ID); err != nil {
		t.Fatalf("CheckRisks: %v", err)
	}
	got, err := st.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.RiskFlags.ExecutedPerson {
		t.Error("CheckRisks clobbered external ExecutedPerson flag")
	}
	if got.RiskFlags.ShellRisk {
		t.Error("clean supplier wrongly flagged after recheck")
	}
}
