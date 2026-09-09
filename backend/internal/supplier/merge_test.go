package supplier_test

// Tests for manual duplicate-merge (合并重复供应商): the master keeps its
// identity and absorbs the duplicate's additive collections; the duplicate is
// archived (history retained). Guards refuse self-merge, archived and
// blacklisted records.

import (
	"context"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestMergeConsolidatesAndArchivesDuplicate(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	// Master: base company with one performance record and category.
	masterIn := namedInput("合并主体建设有限公司", "91330100MA27X2001M", "杭州")
	masterIn.Categories = []string{"施工"}
	masterIn.Performance = []domain.Performance{{Project: "一期工程", Date: "2025-03", Score: 4.0}}
	master, err := svc.Create(ctx, masterIn)
	if err != nil {
		t.Fatalf("create master: %v", err)
	}

	// Duplicate: same company recorded separately, with extra data to merge.
	dupIn := namedInput("合并主体建设公司", "91330100MA27X2002N", "杭州") // different name/code = distinct doc
	dupIn.Categories = []string{"施工", "市政"}                     // "施工" dup, "市政" new
	dupIn.Qualifications = []domain.Qualification{{Type: "市政公用工程总承包", Level: "二级"}}
	dupIn.Products = []domain.ProductService{{Name: "沥青供应", Desc: "道路沥青"}}
	dupIn.Performance = []domain.Performance{
		{Project: "二期工程", Date: "2025-08", Score: 5.0}, // new
		{Project: "一期工程", Date: "2025-03", Score: 4.0}, // exact dup — dropped
	}
	dupIn.CustomFields = map[string]any{"垫资能力": "3 个月", "备注": "dup value"}
	dup, err := svc.Create(ctx, dupIn)
	if err != nil {
		t.Fatalf("create duplicate: %v", err)
	}
	// Attachments are registered after creation (no CreateInput field).
	dup, err = svc.AddAttachment(ctx, dup.ID, domain.Attachment{
		Name: "营业执照.jpg", URL: "/api/v1/attachments/x/1_a.jpg", Size: 10,
	})
	if err != nil {
		t.Fatalf("add attachment: %v", err)
	}
	// Master has its own custom field that must win.
	master, _ = svc.Update(ctx, master.ID, supplier.UpdateInput{
		CustomFields: &map[string]any{"备注": "master value"},
	})

	merged, res, err := svc.MergeSuppliers(ctx, master.ID, dup.ID)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// Result counters.
	if res.PerformanceAdded != 1 || res.CategoriesAdded != 1 || res.QualsAdded != 1 ||
		res.ProductsAdded != 1 || res.AttachmentsAdded != 1 || res.CustomFieldsAdded != 1 {
		t.Errorf("merge counts wrong: %+v", res)
	}

	// Master identity preserved.
	if merged.ID != master.ID || merged.BasicInfo.CompanyName != "合并主体建设有限公司" {
		t.Errorf("master identity changed: id=%s name=%s", merged.ID, merged.BasicInfo.CompanyName)
	}
	// Collections unioned.
	if len(merged.Categories) != 2 || !contains(merged.Categories, "市政") {
		t.Errorf("categories not unioned: %+v", merged.Categories)
	}
	if len(merged.Qualifications) != 2 || !hasQual(merged.Qualifications, "市政公用工程总承包") {
		t.Errorf("qualifications not merged (want base + 市政): %+v", merged.Qualifications)
	}
	if len(merged.ProductsServices) != 1 {
		t.Errorf("products not merged: %+v", merged.ProductsServices)
	}
	if len(merged.PerformanceHistory) != 2 {
		t.Errorf("performance should be 2 (1+1 new, dup dropped), got %d", len(merged.PerformanceHistory))
	}
	if len(merged.Attachments) != 1 || merged.Attachments[0].Name != "营业执照.jpg" {
		t.Errorf("attachment reference not merged: %+v", merged.Attachments)
	}
	// Custom fields: master value wins on conflict, new key carried over.
	if merged.CustomFields["备注"] != "master value" {
		t.Errorf("master custom value must win, got %v", merged.CustomFields["备注"])
	}
	if merged.CustomFields["垫资能力"] != "3 个月" {
		t.Errorf("missing carried-over custom field: %+v", merged.CustomFields)
	}
	// Rating recomputed from consolidated history (4.0 + 5.0)/2 = 4.5.
	if merged.Rating < 4.49 || merged.Rating > 4.51 {
		t.Errorf("rating should recompute to ~4.5, got %.2f", merged.Rating)
	}
	// Audit trail on master.
	sawMerge := false
	for _, c := range merged.ChangeLog {
		if c.Field == "merged_from" && strings.Contains(toString(c.New), dup.ID) {
			sawMerge = true
		}
	}
	if !sawMerge {
		t.Errorf("master change_log missing merged_from entry")
	}

	// Duplicate is archived (hidden from default list; retrievable; has
	// merged_into pointer).
	archived, gerr := svc.Get(ctx, dup.ID)
	if gerr != nil || archived.Status != domain.StatusArchived {
		t.Fatalf("duplicate should be archived: %+v err=%v", archived, gerr)
	}
	sawPointer := false
	for _, c := range archived.ChangeLog {
		if c.Field == "merged_into" {
			sawPointer = true
		}
	}
	if !sawPointer {
		t.Errorf("duplicate change_log missing merged_into entry")
	}
	page, _ := svc.List(ctx, datamodel.Query{})
	if len(page.Items) != 1 || page.Items[0].ID != master.ID {
		t.Errorf("default list should show only the surviving master: %+v", page.Items)
	}
}

func TestMergeGuards(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	a, _ := svc.Create(ctx, namedInput("甲公司有限公司", "91330100MA27X2003A", "杭州"))
	b, _ := svc.Create(ctx, namedInput("乙公司有限公司", "91330100MA27X2004B", "宁波"))

	// Self-merge.
	if _, _, err := svc.MergeSuppliers(ctx, a.ID, a.ID); err == nil {
		t.Error("self-merge must be rejected")
	}
	// Missing id.
	if _, _, err := svc.MergeSuppliers(ctx, "", b.ID); err == nil {
		t.Error("empty master id must be rejected")
	}
	// Blacklisted record on either side is refused.
	bl, _ := svc.Create(ctx, namedInput("拉黑公司有限公司", "91330100MA27X2005C", "温州"))
	if _, err := svc.Blacklist(ctx, bl.ID, "造假"); err != nil {
		t.Fatalf("blacklist: %v", err)
	}
	if _, _, err := svc.MergeSuppliers(ctx, a.ID, bl.ID); err == nil ||
		!strings.Contains(err.Error(), "blacklisted") {
		t.Errorf("merging a blacklisted record must be refused, got %v", err)
	}
	// Archived record is refused.
	if err := svc.Archive(ctx, b.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, _, err := svc.MergeSuppliers(ctx, a.ID, b.ID); err == nil ||
		!strings.Contains(err.Error(), "archived") {
		t.Errorf("merging an archived record must be refused, got %v", err)
	}
}

func hasQual(qs []domain.Qualification, typ string) bool {
	for _, q := range qs {
		if q.Type == typ {
			return true
		}
	}
	return false
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestMergePreservesScoreOnlyPerformanceRecords(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	masterIn := namedInput("评分主体有限公司", "91330100MA27X3001A", "杭州")
	// Score-only evaluation: no project/date (a legal record; validation
	// bounds scores only).
	masterIn.Performance = []domain.Performance{{Score: 4.0}}
	master, err := svc.Create(ctx, masterIn)
	if err != nil {
		t.Fatalf("create master: %v", err)
	}

	dupIn := namedInput("评分主体公司", "91330100MA27X3002B", "杭州")
	dupIn.Performance = []domain.Performance{
		{Score: 2.0, Feedback: "配合度差"}, // distinct: survives
		{Delivery: 5.0, Quality: 3.0},  // distinct dimensions: survives
		{Score: 4.0},                   // byte-identical to master's: dedup
	}
	dup, err := svc.Create(ctx, dupIn)
	if err != nil {
		t.Fatalf("create duplicate: %v", err)
	}

	merged, res, err := svc.MergeSuppliers(ctx, master.ID, dup.ID)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if res.PerformanceAdded != 2 {
		t.Errorf("PerformanceAdded = %d, want 2 (distinct score-only records)", res.PerformanceAdded)
	}
	if len(merged.PerformanceHistory) != 3 {
		t.Errorf("performance history = %d records, want 3", len(merged.PerformanceHistory))
	}
	// Rating must include all three distinct evaluations:
	// (4.0, 2.0, mean(5,3)=4.0)/3 ≈ 3.33.
	if merged.Rating < 3.32 || merged.Rating > 3.34 {
		t.Errorf("rating = %.2f, want ~3.33 over all retained records", merged.Rating)
	}
}

func TestMergeEnrichesUnnumberedQualificationWithCertData(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	masterIn := namedInput("资质主体有限公司", "91330100MA27X4001A", "杭州")
	// Same type+level, recorded without a certificate number or expiry.
	masterIn.Qualifications = []domain.Qualification{{Type: "建筑工程施工总承包", Level: "一级"}}
	master, err := svc.Create(ctx, masterIn)
	if err != nil {
		t.Fatalf("create master: %v", err)
	}

	dupIn := namedInput("资质主体公司", "91330100MA27X4002B", "杭州")
	dupIn.Qualifications = []domain.Qualification{{
		Type: "建筑工程施工总承包", Level: "一级",
		CertNo: "D133012345", Expiry: "2027-06-30", Issuer: "浙江省住建厅", Verified: true,
	}}
	dup, err := svc.Create(ctx, dupIn)
	if err != nil {
		t.Fatalf("create duplicate: %v", err)
	}

	merged, res, err := svc.MergeSuppliers(ctx, master.ID, dup.ID)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if res.QualsAdded != 0 {
		t.Errorf("QualsAdded = %d, want 0 (same cert line, enriched in place)", res.QualsAdded)
	}
	if len(merged.Qualifications) != 1 {
		t.Fatalf("qualifications = %d, want 1 merged line", len(merged.Qualifications))
	}
	q := merged.Qualifications[0]
	if q.CertNo != "D133012345" || q.Expiry != "2027-06-30" ||
		q.Issuer != "浙江省住建厅" || !q.Verified {
		t.Errorf("master qual not enriched from duplicate: %+v", q)
	}

	// Two DIFFERENT certificate numbers of the same type+level stay
	// separate lines (re-issued/new physical cert must not collapse).
	dup2In := namedInput("资质主体一分公司", "91330100MA27X4003C", "杭州")
	dup2In.Qualifications = []domain.Qualification{{
		Type: "建筑工程施工总承包", Level: "一级", CertNo: "D299999999",
	}}
	dup2, _ := svc.Create(ctx, dup2In)
	merged2, res2, err := svc.MergeSuppliers(ctx, master.ID, dup2.ID)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if res2.QualsAdded != 1 || len(merged2.Qualifications) != 2 {
		t.Errorf("distinct cert numbers must both survive: added=%d quals=%+v",
			res2.QualsAdded, merged2.Qualifications)
	}
}
