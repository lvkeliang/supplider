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
	rep := svc.Import(ctx, items)
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
