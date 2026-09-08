package supplier_test

// Tests for per-row duplicate detection during bulk Excel import (批量导入查重).
// The same conservative rules as manual-entry dedup (credit code = strong,
// normalized name = probable) run against a one-pass index of the existing
// library PLUS rows already accepted in the same batch. Default mode warns but
// still imports; SkipDuplicates mode skips matched rows.

import (
	"context"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// importItem builds one spreadsheet row from a CreateInput override.
func importItem(row int, in supplier.CreateInput) supplier.ImportItem {
	return supplier.ImportItem{Row: row, Input: in}
}

// namedInput is cleanInput with a different company name / credit code / city
// so two suppliers are genuinely distinct.
func namedInput(name, code, city string) supplier.CreateInput {
	in := cleanInput()
	in.BasicInfo.CompanyName = name
	in.BasicInfo.CreditCode = code
	in.BasicInfo.Region.City = city
	return in
}

// seedOne creates one supplier directly (the "existing library").
func seedOne(t *testing.T, svc *supplier.Service, in supplier.CreateInput) string {
	t.Helper()
	doc, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	return doc.ID
}

func TestImportWarnsButImportsDuplicatesByDefault(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	existing := namedInput("已存在建材有限公司", "91330100MA27X0001A", "杭州")
	seedOne(t, svc, existing)

	// Row 2 duplicates the existing company by name; row 3 is genuinely new.
	dupRow := namedInput("已存在建材有限公司", "", "杭州") // same normalized name
	newRow := namedInput("全新服务商股份有限公司", "91330100MA27X0002B", "宁波")

	rep := svc.Import(ctx, []supplier.ImportItem{
		importItem(2, dupRow),
		importItem(3, newRow),
	}, supplier.ImportOptions{}) // warn mode (default)

	if rep.Created != 2 || rep.Skipped != 0 {
		t.Fatalf("warn mode should import both: created=%d skipped=%d", rep.Created, rep.Skipped)
	}
	if len(rep.Duplicates) != 1 {
		t.Fatalf("want 1 duplicate warning, got %d: %+v", len(rep.Duplicates), rep.Duplicates)
	}
	d := rep.Duplicates[0]
	if d.Row != 2 || len(d.Matches) != 1 || d.Matches[0].Level != supplier.MatchProbable {
		t.Errorf("duplicate report wrong: %+v", d)
	}
}

func TestImportSkipsDuplicatesWhenEnabled(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	existing := namedInput("跳过测试集团有限公司", "91330100MA27X0003C", "温州")
	existingID := seedOne(t, svc, existing)

	// Strong match: same 18-char credit code (different displayed name even).
	dupByCode := namedInput("名称不同但代码同", "91330100MA27X0003C", "温州")
	newRow := namedInput("另一家全新公司", "91330100MA27X0004D", "绍兴")

	rep := svc.Import(ctx, []supplier.ImportItem{
		importItem(2, dupByCode),
		importItem(3, newRow),
	}, supplier.ImportOptions{SkipDuplicates: true})

	if rep.Created != 1 || rep.Skipped != 1 {
		t.Fatalf("skip mode: created=%d skipped=%d, want 1/1", rep.Created, rep.Skipped)
	}
	if len(rep.IDs) != 1 {
		t.Errorf("skipped row must not yield an id, got %+v", rep.IDs)
	}
	if len(rep.Duplicates) != 1 {
		t.Fatalf("want the skipped row reported in duplicates, got %+v", rep.Duplicates)
	}
	m := rep.Duplicates[0].Matches[0]
	if m.Level != supplier.MatchStrong || m.SupplierID != existingID {
		t.Errorf("strong match should point at existing supplier: %+v", m)
	}
}

func TestImportCatchesIntraBatchDuplicates(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	// Empty library; two rows in the SAME file are the same company by name.
	rowA := namedInput("批次内重复有限公司", "91330100MA27X0005E", "金华")
	rowB := namedInput("批次内重复有限公司", "", "金华") // same normalized name

	// Warn mode: both import, but the second is flagged against the first.
	rep := svc.Import(ctx, []supplier.ImportItem{
		importItem(2, rowA),
		importItem(3, rowB),
	}, supplier.ImportOptions{})
	if rep.Created != 2 {
		t.Fatalf("warn mode creates both: %d", rep.Created)
	}
	if len(rep.Duplicates) != 1 || rep.Duplicates[0].Row != 3 {
		t.Fatalf("second identical row should flag against the first: %+v", rep.Duplicates)
	}

	// Skip mode on a fresh store: the second identical row is skipped.
	svc2 := supplier.NewService(memory.New())
	rep2 := svc2.Import(ctx, []supplier.ImportItem{
		importItem(2, rowA),
		importItem(3, rowB),
		importItem(4, namedInput("不相关公司", "91330100MA27X0006F", "嘉兴")),
	}, supplier.ImportOptions{SkipDuplicates: true})
	if rep2.Created != 2 || rep2.Skipped != 1 {
		t.Fatalf("skip mode intra-batch: created=%d skipped=%d want 2/1", rep2.Created, rep2.Skipped)
	}
}

func TestImportDuplicateCarriesBlacklistStatus(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	// A blacklisted fraudster already in the library.
	fraud := namedInput("造假骗子有限公司", "91330100MA27X0007G", "台州")
	fraudID := seedOne(t, svc, fraud)
	if _, err := svc.Blacklist(ctx, fraudID, "资质造假"); err != nil {
		t.Fatalf("Blacklist: %v", err)
	}

	// Re-import the same company by credit code.
	rep := svc.Import(ctx, []supplier.ImportItem{
		importItem(2, namedInput("造假骗子有限公司", "91330100MA27X0007G", "台州")),
	}, supplier.ImportOptions{SkipDuplicates: true})

	if rep.Skipped != 1 || len(rep.Duplicates) != 1 {
		t.Fatalf("blacklisted match should skip + report: %+v", rep)
	}
	m := rep.Duplicates[0].Matches[0]
	if m.Status != domain.StatusBlacklisted || m.Level != supplier.MatchStrong {
		t.Errorf("blacklisted strong match expected, got status=%s level=%s", m.Status, m.Level)
	}
}

func TestImportDistinctRowsAreNotFlagged(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	rep := svc.Import(ctx, []supplier.ImportItem{
		importItem(2, namedInput("甲建设有限公司", "91330100MA27X0008H", "杭州")),
		importItem(3, namedInput("乙贸易有限公司", "91330100MA27X0009J", "宁波")),
		importItem(4, namedInput("丙服务有限公司", "91330100MA27X0010K", "温州")),
	}, supplier.ImportOptions{SkipDuplicates: true})

	if rep.Created != 3 || rep.Skipped != 0 || len(rep.Duplicates) != 0 {
		t.Fatalf("distinct rows should all import with no flags: %+v", rep)
	}
}
