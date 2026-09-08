package importer_test

// Excel import of 官网 (website) and 主营产品 (products, comma-separated):
// bulk trade ledgers carry product lines and a web site; these must land
// on BasicInfo.Website and ProductsServices like the manual form.

import (
	"bytes"
	"context"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/importer"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestImportWebsiteAndProducts(t *testing.T) {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	set := func(col, row int, v string) {
		cell, _ := excelize.CoordinatesToCellName(col, row)
		_ = f.SetCellValue(sheet, cell, v)
	}
	headers := []string{"公司名称", "省份", "城市", "官网", "主营产品"}
	for i, h := range headers {
		set(i+1, 1, h)
	}
	set(1, 2, "多产品贸易公司")
	set(2, 2, "浙江")
	set(3, 2, "杭州")
	set(4, 2, "https://example.com")
	set(5, 2, "水泥,黄沙、商品混凝土")

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}

	insp, err := importer.Inspect(buf.Bytes())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Suggested[3] != "website" {
		t.Errorf("官网 mapped to %q, want website", insp.Suggested[3])
	}
	if insp.Suggested[4] != "products" {
		t.Errorf("主营产品 mapped to %q, want products", insp.Suggested[4])
	}

	items, err := importer.Build(buf.Bytes(), insp.Suggested, importer.Defaults{Owner: "u"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	in := items[0].Input
	if in.BasicInfo.Website != "https://example.com" {
		t.Errorf("website = %q", in.BasicInfo.Website)
	}
	names := []string{}
	for _, p := range in.Products {
		names = append(names, p.Name)
	}
	// Comma + 、 split into three product lines.
	if len(names) != 3 || names[0] != "水泥" || names[1] != "黄沙" || names[2] != "商品混凝土" {
		t.Errorf("products = %v", names)
	}

	// Persist through the service and read back.
	svc := supplier.NewService(memory.New())
	rep := svc.Import(context.Background(), items, supplier.ImportOptions{})
	if rep.Created != 1 {
		t.Fatalf("Created = %d (errors %+v)", rep.Created, rep.Errors)
	}
	doc, err := svc.Get(context.Background(), rep.IDs[0])
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if doc.BasicInfo.Website != "https://example.com" {
		t.Errorf("persisted website = %q", doc.BasicInfo.Website)
	}
	if len(doc.ProductsServices) != 3 {
		t.Errorf("persisted products = %d", len(doc.ProductsServices))
	}
}
