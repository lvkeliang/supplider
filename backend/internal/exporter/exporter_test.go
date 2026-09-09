package exporter_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/exporter"
	"github.com/supplider/supplider/backend/internal/importer"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// sampleDocs creates two suppliers through the real service path (so IDs,
// change logs and ratings are genuine) for round-trip tests.
func sampleDocs(t *testing.T) []*domain.Supplier {
	t.Helper()
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	inputs := []supplier.CreateInput{
		{
			Owner: "u1", Visibility: domain.VisSelf,
			BasicInfo: domain.BasicInfo{
				CompanyName: "杭州一建有限公司", LegalPerson: "张三",
				Website: "www.hzyijian.example",
				Region:  domain.Region{Province: "浙江", City: "杭州", District: "余杭区"},
			},
			Categories: []string{"施工服务", "市政工程"},
			Products: []domain.ProductService{
				{Name: "土建施工"}, {Name: "道路工程"},
			},
			Qualifications: []domain.Qualification{
				{Type: "建筑工程施工总承包", Level: "三级"},
				{Type: "市政公用工程施工总承包", Level: "二级"}, // higher rank
			},
			CustomFields: map[string]any{"垫资能力": "500万以内"},
		},
		{
			Owner: "u1", Visibility: domain.VisSelf,
			BasicInfo: domain.BasicInfo{
				CompanyName: "宁波建材贸易有限公司",
				Region:      domain.Region{Province: "浙江", City: "宁波"},
			},
			Categories:   []string{"建材贸易"},
			CustomFields: map[string]any{"品牌": "海螺水泥", "垫资能力": "不垫资"},
		},
	}
	docs := make([]*domain.Supplier, 0, len(inputs))
	for _, in := range inputs {
		d, err := svc.Create(ctx, in)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		docs = append(docs, d)
	}
	return docs
}

// TestXLSXRoundTripThroughImporter verifies the exchange guarantee: an
// exported workbook must re-enter the system through the importer with the
// auto-suggested mapping (no manual column work) and keep fixed fields,
// categories, the highest-ranked qualification and custom fields.
func TestXLSXRoundTripThroughImporter(t *testing.T) {
	docs := sampleDocs(t)
	data, err := exporter.XLSX(docs)
	if err != nil {
		t.Fatalf("XLSX: %v", err)
	}

	insp, err := importer.Inspect(data)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	items, err := importer.Build(data, insp.Suggested, importer.Defaults{Owner: "u2", Visibility: domain.VisSelf})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d import items, want 2", len(items))
	}

	byName := map[string]supplier.CreateInput{}
	for _, it := range items {
		byName[it.Input.BasicInfo.CompanyName] = it.Input
	}

	one, ok := byName["杭州一建有限公司"]
	if !ok {
		t.Fatal("exported workbook lost 杭州一建有限公司")
	}
	if one.BasicInfo.Region.City != "杭州" || one.BasicInfo.Region.District != "余杭区" ||
		one.BasicInfo.LegalPerson != "张三" {
		t.Errorf("fixed fields lost on round-trip: %+v", one.BasicInfo)
	}
	if len(one.Categories) != 2 || one.Categories[0] != "施工服务" {
		t.Errorf("categories = %v, want [施工服务 市政工程]", one.Categories)
	}
	// Website and product lines round-trip (regression: once omitted from
	// the workbook, an export→re-import silently dropped them).
	if one.BasicInfo.Website != "www.hzyijian.example" {
		t.Errorf("website lost on round-trip: %q", one.BasicInfo.Website)
	}
	if len(one.Products) != 2 || one.Products[0].Name != "土建施工" || one.Products[1].Name != "道路工程" {
		t.Errorf("products lost on round-trip: %+v", one.Products)
	}
	// The template has a single qualification slot: the highest-ranked one.
	if len(one.Qualifications) != 1 || one.Qualifications[0].Level != "二级" {
		t.Errorf("qualifications = %+v, want single 二级 (highest rank)", one.Qualifications)
	}
	if one.CustomFields["垫资能力"] != "500万以内" {
		t.Errorf("custom field lost: %v", one.CustomFields)
	}

	two, ok := byName["宁波建材贸易有限公司"]
	if !ok {
		t.Fatal("exported workbook lost 宁波建材贸易有限公司")
	}
	// 品牌 exists only on the second supplier: its union column must still
	// line up column-wise (empty cell for supplier one).
	if two.CustomFields["品牌"] != "海螺水泥" {
		t.Errorf("union custom column lost/misaligned: %v", two.CustomFields)
	}
	if two.CustomFields["垫资能力"] != "不垫资" {
		t.Errorf("shared custom column misaligned: %v", two.CustomFields)
	}
}

// TestJSONBundle verifies the full-fidelity backup container: explicit
// format/version header, count, and every document field (change_log) is
// present — this bundle is the personal→small-business restore format.
func TestJSONBundle(t *testing.T) {
	docs := sampleDocs(t)
	when := time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)
	data, err := exporter.JSON(docs, when)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	var b exporter.Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if b.Format != exporter.BundleFormat || b.Version != exporter.BundleVersion {
		t.Errorf("bundle header = %q v%d, want %q v%d", b.Format, b.Version, exporter.BundleFormat, exporter.BundleVersion)
	}
	if b.Count != 2 || len(b.Suppliers) != 2 {
		t.Errorf("count = %d / suppliers = %d, want 2/2", b.Count, len(b.Suppliers))
	}
	if len(b.Suppliers[0].ChangeLog) == 0 {
		t.Error("full-fidelity export must retain change_log")
	}
	if !b.ExportedAt.Equal(when) {
		t.Errorf("exported_at = %v, want %v", b.ExportedAt, when)
	}
}

// TestXLSXEmpty verifies an empty export still yields a valid workbook
// (header row only) rather than failing.
func TestXLSXEmpty(t *testing.T) {
	data, err := exporter.XLSX(nil)
	if err != nil {
		t.Fatalf("XLSX(nil): %v", err)
	}
	insp, err := importer.Inspect(data)
	if err != nil {
		t.Fatalf("Inspect empty export: %v", err)
	}
	if len(insp.Headers) == 0 || insp.TotalDataRows != 0 {
		t.Errorf("empty export: headers=%d rows=%d", len(insp.Headers), insp.TotalDataRows)
	}
}
