package importer_test

// Duplicate-column mapping robustness (column→field is not injective):
// real workbooks can carry two headers matching the same fixed field
// (e.g. "名称" and "公司名称"). Importing the same file MUST be
// deterministic — Go map iteration otherwise made the winning value random.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/supplider/supplider/backend/internal/importer"
)

func buildXLSX(t *testing.T, headers []string, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	put := func(col, row int, v string) {
		c, _ := excelize.CoordinatesToCellName(col, row)
		_ = f.SetCellValue(sheet, c, v)
	}
	for i, h := range headers {
		put(i+1, 1, h)
	}
	for ri, row := range rows {
		for ci, v := range row {
			put(ci+1, ri+2, v)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	f.Close()
	return buf.Bytes()
}

func TestDuplicateFixedFieldColumnIsDeterministic(t *testing.T) {
	data := buildXLSX(t,
		[]string{"名称", "公司名称", "省份", "城市"},
		[][]string{{"左列公司名", "右列公司名", "浙江", "杭州"}},
	)
	insp, err := importer.Inspect(data)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	// Only the FIRST matching column owns the fixed field; the second
	// "公司名称" column is preserved as a custom field, not silently lost.
	if insp.Suggested[0] != "company_name" || insp.Suggested[1] != "custom" {
		t.Fatalf("suggested mapping wrong: %v", insp.Suggested)
	}

	// Explicitly mapping BOTH columns to the field must also be stable:
	// the leftmost (column order) non-empty value always wins.
	dupMapping := map[int]string{0: "company_name", 1: "company_name", 2: "province", 3: "city"}
	for i := 0; i < 50; i++ {
		items, err := importer.Build(data, dupMapping, importer.Defaults{Owner: "u"})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if got := items[0].Input.BasicInfo.CompanyName; got != "左列公司名" {
			t.Fatalf("run %d: company name = %q, want deterministic leftmost value", i, got)
		}
	}
}

func TestDuplicateListAndCustomColumnsUnionAndJoin(t *testing.T) {
	data := buildXLSX(t,
		[]string{"公司名称", "省份", "城市", "分类", "品类", "备注", "备注"},
		[][]string{{"并集测试公司", "浙江", "杭州", "施工", "市政", "先备注", "后备注"}},
	)
	mapping := map[int]string{
		0: "company_name", 1: "province", 2: "city",
		3: "categories", 4: "categories", // two columns → union, no overwrite
		5: "custom", 6: "custom", // duplicate custom header → join
	}
	for i := 0; i < 20; i++ {
		items, err := importer.Build(data, mapping, importer.Defaults{Owner: "u"})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		in := items[0].Input
		if len(in.Categories) != 2 || in.Categories[0] != "施工" || in.Categories[1] != "市政" {
			t.Fatalf("categories = %v, want stable [施工 市政]", in.Categories)
		}
		if got := in.CustomFields["备注"]; got != "先备注；后备注" {
			t.Fatalf("custom join = %v, want 先备注；后备注", got)
		}
	}
}

func TestUnknownFieldKeyRejected(t *testing.T) {
	data := buildXLSX(t,
		[]string{"公司名称", "省份", "城市"},
		[][]string{{"类型错误公司", "浙江", "杭州"}},
	)
	// A typo must fail loudly rather than silently dropping the column.
	mapping := map[int]string{0: "compny_name", 1: "province", 2: "city"}
	_, err := importer.Build(data, mapping, importer.Defaults{Owner: "u"})
	if err == nil || !strings.Contains(err.Error(), "unknown field key") {
		t.Fatalf("unknown key should error, got %v", err)
	}
}
