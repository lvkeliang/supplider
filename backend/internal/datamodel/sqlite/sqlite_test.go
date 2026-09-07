// SQLite adapter conformance: runs the SAME contract suite every store
// adapter must pass (memory today, MongoDB tomorrow) — filter semantics,
// keyset pagination and archive behavior are pinned against the interface,
// not the implementation.
package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/contract"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
)

// TestSupplierStoreContract runs the shared conformance suite against a
// fresh file-backed SQLite store per case.
func TestSupplierStoreContract(t *testing.T) {
	contract.RunSupplierStoreTests(t, func() datamodel.SupplierStore {
		dir := t.TempDir()
		st, err := sqlite.Open(filepath.Join(dir, "supplider.db"))
		if err != nil {
			t.Fatalf("sqlite.Open: %v", err)
		}
		return st
	})
}

// TestInMemoryStoreContract also runs the suite against the ":memory:"
// DSN (single-connection mode), guarding the ephemeral-sandbox path.
func TestInMemoryStoreContract(t *testing.T) {
	contract.RunSupplierStoreTests(t, func() datamodel.SupplierStore {
		st, err := sqlite.Open(":memory:")
		if err != nil {
			t.Fatalf("sqlite.Open :memory:: %v", err)
		}
		return st
	})
}

// TestPersistenceAcrossReopen is SQLite-specific value: a document written
// to the file must survive Close + reopen with all structured and free-form
// fields intact (this is what makes personal-tier data durable).
func TestPersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "supplider.db")

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	doc := &domain.Supplier{
		ID:     "sup_2026_000100",
		Owner:  "user_a",
		Status: domain.StatusActive,
		BasicInfo: domain.BasicInfo{
			CompanyName: "杭州持久建设有限公司",
			CreditCode:  "91330106MA2XXXXXX1",
			LegalPerson: "王五",
			Region:      domain.Region{Province: "浙江", City: "杭州", District: "西湖区"},
		},
		Categories:     []string{"施工服务", "市政工程"},
		Qualifications: []domain.Qualification{{Type: "建筑工程施工总承包", Level: "一级"}},
		CustomFields:   map[string]any{"垫资能力": "1000万", "设备": []any{"挖掘机", "塔吊"}},
		Rating:         4.5,
		Visibility:     domain.VisSelf,
		CreatedAt:      time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 6, 2, 9, 30, 0, 0, time.UTC),
	}
	if err := st.Put(ctx, doc); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen the same file — everything must still be there.
	st2, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	got, err := st2.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.BasicInfo.CompanyName != "杭州持久建设有限公司" {
		t.Errorf("name = %q", got.BasicInfo.CompanyName)
	}
	if got.CustomFields["垫资能力"] != "1000万" {
		t.Errorf("custom field lost: %+v", got.CustomFields)
	}
	if len(got.Qualifications) != 1 || got.Qualifications[0].Level != "一级" {
		t.Errorf("qualifications lost: %+v", got.Qualifications)
	}

	// Denormalized projections must survive reopen too: region + category
	// + qual-rank filters should hit via the side table / columns.
	page, err := st2.List(ctx, datamodel.Query{Filter: datamodel.SupplierFilter{
		Province:    "浙江",
		City:        "杭州",
		Categories:  []string{"市政工程"},
		MinQualRank: domain.QualRank("一级"),
		MinRating:   4.0,
	}})
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != doc.ID {
		t.Fatalf("filtered list after reopen = %d items", len(page.Items))
	}
}

// TestArchivePersistsAcrossReopen checks archive semantics land in the
// durable columns (archived docs are hidden from default List after reopen).
func TestArchivePersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "supplider.db")

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	doc := &domain.Supplier{
		ID:        "sup_2026_000200",
		Owner:     "user_a",
		Status:    domain.StatusActive,
		BasicInfo: domain.BasicInfo{CompanyName: "将归档公司", Region: domain.Region{Province: "浙江", City: "杭州"}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := st.Put(ctx, doc); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.Delete(ctx, doc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st2, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	got, err := st2.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("archived doc should remain readable: %v", err)
	}
	if got.Status != domain.StatusArchived || got.ArchivedAt == nil {
		t.Fatalf("archive state not persisted: %q / %v", got.Status, got.ArchivedAt)
	}
	page, err := st2.List(ctx, datamodel.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, item := range page.Items {
		if item.ID == doc.ID {
			t.Fatal("archived document must not appear in default List after reopen")
		}
	}
}

// TestKeywordLikeMetacharacters guards the ESCAPE clause: a keyword
// containing LIKE wildcards must be treated literally.
func TestKeywordLikeMetacharacters(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	doc := &domain.Supplier{
		ID:        "sup_2026_000300",
		Owner:     "u",
		Status:    domain.StatusActive,
		BasicInfo: domain.BasicInfo{CompanyName: "杭州100%建设公司", Region: domain.Region{Province: "浙江", City: "杭州"}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := st.Put(ctx, doc); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// "100%" as a keyword must match literally (the % is not a wildcard).
	page, err := st.List(ctx, datamodel.Query{Filter: datamodel.SupplierFilter{Keyword: "100%"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("literal %% keyword = %d items, want 1", len(page.Items))
	}

	// A wildcard-only keyword must not match everything.
	page, err = st.List(ctx, datamodel.Query{Filter: datamodel.SupplierFilter{Keyword: "%"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("'%%' keyword should be literal and still match the name, got %d", len(page.Items))
	}

	// A keyword present in no document must return nothing.
	page, err = st.List(ctx, datamodel.Query{Filter: datamodel.SupplierFilter{Keyword: "不存在的词"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("absent keyword = %d items, want 0", len(page.Items))
	}
}
