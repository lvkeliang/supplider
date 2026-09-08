package importer_test

// Excel import of performance-score columns (综合/交付/质量/配合度 + 评价):
// a row with scores produces one Performance record; dimension-only rows
// feed the rating via the dimension-mean rule; non-numeric/out-of-range
// score cells are ignored rather than failing the import.

import (
	"bytes"
	"context"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/importer"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestImportPerformanceColumns(t *testing.T) {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	set := func(col, row int, v string) {
		cell, _ := excelize.CoordinatesToCellName(col, row)
		_ = f.SetCellValue(sheet, cell, v)
	}
	headers := []string{"公司名称", "省份", "城市", "综合评分", "交付评分", "质量评分", "配合度评分", "合作评价"}
	for i, h := range headers {
		set(i+1, 1, h)
	}
	// Row 2: overall score only.
	set(1, 2, "综合分贸易公司")
	set(2, 2, "浙江")
	set(3, 2, "杭州")
	set(4, 2, "5")
	set(8, 2, "总评五分")
	// Row 3: dimensions only (交付4/质量5/配合3 → mean 4.0).
	set(1, 3, "三维施工公司")
	set(2, 3, "浙江")
	set(3, 3, "宁波")
	set(5, 3, "4")
	set(6, 3, "5")
	set(7, 3, "3")
	// Row 4: invalid score (out of range) must be ignored, import still works.
	set(1, 4, "坏分贸易公司")
	set(2, 4, "江苏")
	set(3, 4, "南京")
	set(4, 4, "9分")

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}

	insp, err := importer.Inspect(buf.Bytes())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	// Suggested mapping must recognize the new columns by alias.
	want := map[int]string{4: "score", 5: "delivery", 6: "quality", 7: "cooperation", 8: "feedback"}
	for col, key := range want {
		if insp.Suggested[col-1] != key {
			t.Errorf("column %d (%s) mapped to %q, want %q", col, headers[col-1], insp.Suggested[col-1], key)
		}
	}

	items, err := importer.Build(buf.Bytes(), insp.Suggested, importer.Defaults{Owner: "u"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if len(items[0].Input.Performance) != 1 || items[0].Input.Performance[0].Score != 5.0 {
		t.Errorf("row 2 performance wrong: %+v", items[0].Input.Performance)
	}
	if items[0].Input.Performance[0].Feedback != "总评五分" {
		t.Errorf("feedback not imported: %+v", items[0].Input.Performance[0])
	}
	p := items[1].Input.Performance[0]
	if p.Delivery != 4.0 || p.Quality != 5.0 || p.Cooperation != 3.0 || p.Score != 0 {
		t.Errorf("row 3 dimensions wrong: %+v", p)
	}
	if len(items[2].Input.Performance) != 1 || items[2].Input.Performance[0].Score != 0 {
		t.Errorf("row 4 invalid score should yield empty-score record, got %+v", items[2].Input.Performance)
	}

	svc := supplier.NewService(memory.New())
	rep := svc.Import(context.Background(), items, supplier.ImportOptions{})
	if rep.Created != 3 {
		t.Errorf("Created = %d, want 3 (errors: %+v)", rep.Created, rep.Errors)
	}
	// The dimension-only supplier rates 4.0 (mean of 4/5/3).
	doc, err := svc.Get(context.Background(), rep.IDs[1])
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if doc.Rating < 3.99 || doc.Rating > 4.01 {
		t.Errorf("dimension-only imported rating = %.2f, want 4.0", doc.Rating)
	}
}
