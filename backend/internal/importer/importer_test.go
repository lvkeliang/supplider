package importer_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/importer"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// TestTemplateInspectBuild round-trips the generated template: the header
// aliases must auto-map, and the example row must import cleanly.
func TestTemplateInspectBuild(t *testing.T) {
	tpl, err := importer.Template()
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	insp, err := importer.Inspect(tpl)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(insp.Headers) == 0 {
		t.Fatal("template has no headers")
	}
	// The 公司名称 header (0) must auto-map to company_name.
	if insp.Suggested[0] != "company_name" {
		t.Errorf("company_name mapping = %q", insp.Suggested[0])
	}
	// Required fields should appear in the field list.
	var sawCompany bool
	for _, f := range insp.Fields {
		if f.Key == "company_name" && f.Required {
			sawCompany = true
		}
	}
	if !sawCompany {
		t.Error("company_name not marked required in field options")
	}

	items, err := importer.Build(tpl, insp.Suggested, importer.Defaults{Owner: "u", Visibility: domain.VisSelf})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Template has one example data row.
	if len(items) != 1 {
		t.Fatalf("expected 1 import item, got %d", len(items))
	}
	it := items[0]
	if it.Input.BasicInfo.CompanyName != "杭州示例建设有限公司" {
		t.Errorf("name = %q", it.Input.BasicInfo.CompanyName)
	}
	if it.Input.BasicInfo.Region.Province != "浙江" || it.Input.BasicInfo.Region.City != "杭州" {
		t.Errorf("region = %+v", it.Input.BasicInfo.Region)
	}
	if len(it.Input.Categories) != 2 || it.Input.Categories[0] != "施工服务" {
		t.Errorf("categories split = %v", it.Input.Categories)
	}
	if len(it.Input.Qualifications) != 1 || it.Input.Qualifications[0].Level != "二级" {
		t.Errorf("qual = %+v", it.Input.Qualifications)
	}

	// End-to-end through the service: the example row imports for real.
	svc := supplier.NewService(memory.New())
	rep := svc.Import(context.Background(), items, supplier.ImportOptions{})
	if rep.Created != 1 || rep.Failed != 0 {
		t.Fatalf("import report = %+v", rep)
	}
}

// TestCustomColumnsAndValidation builds a sheet with an unrecognized column
// (must become a custom_field) and a row missing required fields (must be
// reported as a failed row without aborting the valid row).
func TestCustomColumnsAndValidation(t *testing.T) {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	set := func(col, row int, v string) {
		cell, _ := excelize.CoordinatesToCellName(col, row)
		_ = f.SetCellValue(sheet, cell, v)
	}
	// Headers: name, province, city, + an industry-specific custom column.
	set(1, 1, "公司名称")
	set(2, 1, "省份")
	set(3, 1, "城市")
	set(4, 1, "垫资能力")
	// Valid row.
	set(1, 2, "杭州贸易公司")
	set(2, 2, "浙江")
	set(3, 2, "杭州")
	set(4, 2, "500万以内")
	// Blank row (should be skipped entirely).
	// Invalid row: missing name.
	set(2, 4, "江苏")
	set(3, 4, "南京")
	set(4, 4, "无")

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}

	insp, err := importer.Inspect(buf.Bytes())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Suggested[3] != "custom" {
		t.Errorf("unknown column should map to custom, got %q", insp.Suggested[3])
	}
	if insp.TotalDataRows != 2 { // blank row excluded
		t.Errorf("TotalDataRows = %d, want 2", insp.TotalDataRows)
	}

	items, err := importer.Build(buf.Bytes(), insp.Suggested, importer.Defaults{Owner: "u"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items (blank skipped), got %d", len(items))
	}
	if got := items[0].Input.CustomFields["垫资能力"]; got != "500万以内" {
		t.Errorf("custom field = %v", got)
	}

	svc := supplier.NewService(memory.New())
	rep := svc.Import(context.Background(), items, supplier.ImportOptions{})
	if rep.Created != 1 {
		t.Errorf("Created = %d, want 1", rep.Created)
	}
	if rep.Failed != 1 {
		t.Errorf("Failed = %d, want 1 (missing name row)", rep.Failed)
	}
	if len(rep.Errors) != 1 || rep.Errors[0].Row != 4 {
		t.Errorf("errors = %+v, want one error on row 4", rep.Errors)
	}
}

// TestInvalidFile ensures a non-xlsx payload is rejected cleanly.
func TestInvalidFile(t *testing.T) {
	if _, err := importer.Inspect([]byte("not an xlsx")); err == nil {
		t.Fatal("Inspect should reject non-xlsx bytes")
	}
	if _, err := importer.Build([]byte("not an xlsx"), nil, importer.Defaults{}); err == nil {
		t.Fatal("Build should reject non-xlsx bytes")
	}
}
