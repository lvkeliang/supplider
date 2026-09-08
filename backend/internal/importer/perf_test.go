package importer_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/importer"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// TestImport1000RowsUnder3s guards the PRD performance red line: importing
// 1000 Excel suppliers must finish within 3 seconds, measured against the
// real SQLite adapter (not the in-memory store).
func TestImport1000RowsUnder3s(t *testing.T) {
	const n = 1000

	// Build a workbook with n data rows.
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	headers := []string{"公司名称", "省份", "城市", "品类", "法定代表人"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	for i := 0; i < n; i++ {
		row := i + 2
		set := func(col int, v any) {
			cell, _ := excelize.CoordinatesToCellName(col, row)
			_ = f.SetCellValue(sheet, cell, v)
		}
		set(1, fmt.Sprintf("批量测试建设公司%04d", i+1))
		set(2, "浙江")
		set(3, "杭州")
		set(4, "施工服务,市政工程")
		set(5, "法人甲")
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	data := buf.Bytes()

	store, err := sqlite.Open(filepath.Join(t.TempDir(), "perf.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	svc := supplier.NewService(store)
	ctx := context.Background()

	insp, err := importer.Inspect(data)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	start := time.Now()
	items, err := importer.Build(data, insp.Suggested, importer.Defaults{Owner: "u"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rep := svc.Import(ctx, items, supplier.ImportOptions{})
	elapsed := time.Since(start)

	if rep.Created != n {
		t.Fatalf("created = %d, want %d (failed=%d, first errors=%v)",
			rep.Created, n, rep.Failed, rep.Errors[:min(3, len(rep.Errors))])
	}
	t.Logf("imported %d rows in %s (%.0f rows/s)", n, elapsed, float64(n)/elapsed.Seconds())
	if elapsed > 3*time.Second {
		t.Fatalf("import took %s, exceeds 3s red line", elapsed)
	}
}

// TestImport1000RichRowsUnder3s exercises the PRD red line with a realistic
// ledger: every row carries contact/website/product columns AND the
// performance scores that add a rating recompute + FTS/rating index work per
// insert. This is the heaviest shape the importer accepts.
func TestImport1000RichRowsUnder3s(t *testing.T) {
	const n = 1000

	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	headers := []string{"公司名称", "省份", "城市", "法定代表人", "联系人", "联系电话",
		"网址", "品类", "主营产品", "资质类型", "资质等级",
		"综合评分", "交付评分", "质量评分", "配合度评分", "合作评价"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	for i := 0; i < n; i++ {
		row := i + 2
		set := func(col int, v any) {
			cell, _ := excelize.CoordinatesToCellName(col, row)
			_ = f.SetCellValue(sheet, cell, v)
		}
		set(1, fmt.Sprintf("批量富数据公司%04d", i+1))
		set(2, "浙江")
		set(3, "杭州")
		set(4, "法人甲")
		set(5, "联系人乙")
		set(6, "13800000000")
		set(7, "example.com")
		set(8, "施工服务,市政工程")
		set(9, "土建施工,道路工程")
		set(10, "建筑工程施工总承包")
		set(11, "二级")
		set(12, 4.5) // overall score → rating per row
		set(13, 4.0)
		set(14, 5.0)
		set(15, 4.0)
		set(16, "合作顺利")
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	data := buf.Bytes()

	store, err := sqlite.Open(filepath.Join(t.TempDir(), "perf_rich.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	svc := supplier.NewService(store)

	insp, err := importer.Inspect(data)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	start := time.Now()
	items, err := importer.Build(data, insp.Suggested, importer.Defaults{Owner: "u"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rep := svc.Import(context.Background(), items, supplier.ImportOptions{})
	elapsed := time.Since(start)

	if rep.Created != n {
		t.Fatalf("created = %d, want %d (failed=%d, first=%v)",
			rep.Created, n, rep.Failed, rep.Errors[:min(3, len(rep.Errors))])
	}
	t.Logf("imported %d rich rows in %s (%.0f rows/s)", n, elapsed, float64(n)/elapsed.Seconds())
	if elapsed > 3*time.Second {
		t.Fatalf("rich import took %s, exceeds 3s red line", elapsed)
	}
}
