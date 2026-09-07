package supplier_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func newSvc() *supplier.Service {
	return supplier.NewService(memory.New())
}

func validInput() supplier.CreateInput {
	return supplier.CreateInput{
		Owner: "user_001",
		BasicInfo: domain.BasicInfo{
			CompanyName: "杭州一建有限公司",
			Region:      domain.Region{Province: "浙江", City: "杭州", District: "余杭区"},
		},
		Categories:   []string{"施工服务"},
		Visibility:   domain.VisSelf,
		CustomFields: map[string]any{"垫资能力": "500万以内"},
	}
}

func TestCreateGeneratesIDAndLog(t *testing.T) {
	svc := newSvc()
	doc, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(doc.ID, "sup_20") {
		t.Errorf("id = %q, want sup_<year>_...", doc.ID)
	}
	if doc.Status != domain.StatusActive {
		t.Errorf("status = %q", doc.Status)
	}
	if len(doc.ChangeLog) != 1 || doc.ChangeLog[0].Field != "_created" {
		t.Errorf("change log = %+v", doc.ChangeLog)
	}
}

func TestCreateRequiresNameAndRegion(t *testing.T) {
	svc := newSvc()
	in := validInput()
	in.BasicInfo.CompanyName = "  "
	if _, err := svc.Create(context.Background(), in); err == nil {
		t.Fatal("expected error for missing company name")
	}

	in = validInput()
	in.BasicInfo.Region.City = ""
	if _, err := svc.Create(context.Background(), in); err == nil {
		t.Fatal("expected error for missing city")
	}
}

func TestUpdateAppendsChangeLog(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	doc, _ := svc.Create(ctx, validInput())

	lp := "张三"
	upd, err := svc.Update(ctx, doc.ID, supplier.UpdateInput{
		BasicInfo: &domain.BasicInfo{
			CompanyName: "杭州一建有限公司",
			LegalPerson: lp,
			Region:      domain.Region{Province: "浙江", City: "杭州", District: "余杭区"},
		},
		Source: domain.SourceAPISync,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Creation entry + exactly one field change.
	if len(upd.ChangeLog) != 2 {
		t.Fatalf("change log len = %d, want 2: %+v", len(upd.ChangeLog), upd.ChangeLog)
	}
	entry := upd.ChangeLog[1]
	if entry.Field != "basic_info.legal_person" || entry.Source != domain.SourceAPISync {
		t.Errorf("change entry = %+v", entry)
	}
	if entry.Old != nil || entry.New != "张三" {
		t.Errorf("old/new = %v -> %v", entry.Old, entry.New)
	}
}

func TestNoOpUpdateTouchesNothing(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	doc, _ := svc.Create(ctx, validInput())

	same := validInput().BasicInfo
	upd, err := svc.Update(ctx, doc.ID, supplier.UpdateInput{BasicInfo: &same})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(upd.ChangeLog) != 1 {
		t.Fatalf("identical update appended %d change entries", len(upd.ChangeLog)-1)
	}
}

func TestArchiveAndRestore(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	doc, _ := svc.Create(ctx, validInput())

	if err := svc.Archive(ctx, doc.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	// Archived docs disappear from the default list.
	page, err := svc.List(ctx, datamodel.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("archived supplier listed: %d items", len(page.Items))
	}
	// ...but remain readable by id.
	got, err := svc.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get archived: %v", err)
	}
	if got.Status != domain.StatusArchived || got.ArchivedAt == nil {
		t.Errorf("archived state wrong: %s / %v", got.Status, got.ArchivedAt)
	}

	restored, err := svc.Restore(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Status != domain.StatusActive || restored.ArchivedAt != nil {
		t.Errorf("restore state wrong: %s / %v", restored.Status, restored.ArchivedAt)
	}
}

func TestListReturnsSummaries(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	doc, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	page, err := svc.List(ctx, datamodel.Query{Filter: datamodel.SupplierFilter{City: "杭州"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %d", len(page.Items))
	}
	sum := page.Items[0]
	if sum.ID != doc.ID || sum.Name != "杭州一建有限公司" || sum.City != "杭州" {
		t.Errorf("summary = %+v", sum)
	}
}

func TestRatingRecomputedFromPerformance(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	doc, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	perfs := []domain.Performance{
		{Project: "A项目", Score: 4.0},
		{Project: "B项目", Score: 5.0},
	}
	upd, err := svc.Update(ctx, doc.ID, supplier.UpdateInput{Performance: &perfs})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Mean of 4.0 and 5.0 = 4.5, rounded to 2 decimal places.
	if upd.Rating != 4.5 {
		t.Errorf("rating = %v, want 4.5", upd.Rating)
	}

	// Clearing performance history resets the aggregate.
	empty := []domain.Performance{}
	upd, err = svc.Update(ctx, doc.ID, supplier.UpdateInput{Performance: &empty})
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if upd.Rating != 0 {
		t.Errorf("rating after clear = %v, want 0", upd.Rating)
	}
}

func TestArchiveNotFound(t *testing.T) {
	svc := newSvc()
	err := svc.Archive(context.Background(), "sup_missing_000001")
	if !errors.Is(err, datamodel.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestExportWalksAllPages proves export crosses the 100-row page boundary:
// it walks keyset pages internally until the cursor is exhausted, returning
// full documents (not summaries).
func TestExportWalksAllPages(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	const n = 205 // two full pages + a partial third
	for i := 0; i < n; i++ {
		in := validInput()
		in.BasicInfo.CompanyName = fmt.Sprintf("测试导出供应商%03d", i)
		if _, err := svc.Create(ctx, in); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	docs, err := svc.Export(ctx, datamodel.SupplierFilter{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(docs) != n {
		t.Fatalf("exported %d docs, want %d (cursor walk broken?)", len(docs), n)
	}
	// Export returns full documents: change_log must be present.
	if len(docs[0].ChangeLog) == 0 {
		t.Error("export returned summary-shaped document (change_log missing)")
	}

	// Filters apply to export the same way as to list.
	filtered, err := svc.Export(ctx, datamodel.SupplierFilter{Keyword: "测试导出供应商001"})
	if err != nil {
		t.Fatalf("Export filtered: %v", err)
	}
	if len(filtered) == 0 || len(filtered) > 20 {
		// Keyword matches "001", "001x" (0010-0019), "101" style names do
		// not contain the literal substring; expect a small subset, never all.
		t.Errorf("keyword filter returned %d docs, want a small subset", len(filtered))
	}
}
