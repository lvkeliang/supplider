// Package contract holds the SupplierStore conformance suite.
//
// EVERY storage adapter must pass this exact suite against the SAME test
// data — semantics (filtering, keyset pagination, archive behavior) are
// pinned here so the three tiers cannot drift:
//
//	func TestSupplierStoreContract(t *testing.T) {
//	    contract.RunSupplierStoreTests(t, func() datamodel.SupplierStore {
//	        return memory.New()
//	    })
//	}
//
// The suite is written against the interface only; adapters supply a
// fresh store per case.
package contract

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
)

// NewStore builds a fresh, empty store for one test case.
type NewStore func() datamodel.SupplierStore

// RunSupplierStoreTests executes the full SupplierStore contract.
func RunSupplierStoreTests(t *testing.T, newStore NewStore) {
	t.Run("PutAndGet", func(t *testing.T) { testPutAndGet(t, newStore()) })
	t.Run("GetNotFound", func(t *testing.T) { testGetNotFound(t, newStore()) })
	t.Run("UpsertReplaces", func(t *testing.T) { testUpsertReplaces(t, newStore()) })
	t.Run("DeleteIsArchive", func(t *testing.T) { testDeleteIsArchive(t, newStore()) })
	t.Run("DeleteNotFound", func(t *testing.T) { testDeleteNotFound(t, newStore()) })
	t.Run("PaginationKeyset", func(t *testing.T) { testPaginationKeyset(t, newStore()) })
	t.Run("LimitCappedAt100", func(t *testing.T) { testLimitCapped(t, newStore()) })
	t.Run("FilterRegion", func(t *testing.T) { testFilterRegion(t, newStore()) })
	t.Run("FilterCategories", func(t *testing.T) { testFilterCategories(t, newStore()) })
	t.Run("FilterMinQualRank", func(t *testing.T) { testFilterMinQualRank(t, newStore()) })
	t.Run("FilterRatingRange", func(t *testing.T) { testFilterRatingRange(t, newStore()) })
	t.Run("FilterOwner", func(t *testing.T) { testFilterOwner(t, newStore()) })
	t.Run("FilterKeyword", func(t *testing.T) { testFilterKeyword(t, newStore()) })
	t.Run("StoredDocumentIsIndependent", func(t *testing.T) { testStoredCopyIndependent(t, newStore()) })
	t.Run("Ping", func(t *testing.T) { testPing(t, newStore()) })
}

// ---------- fixtures ----------

func supplier(id, name, province, city, owner string, opts ...func(*domain.Supplier)) *domain.Supplier {
	now := time.Now().UTC()
	s := &domain.Supplier{
		ID:     id,
		Owner:  owner,
		Status: domain.StatusActive,
		BasicInfo: domain.BasicInfo{
			CompanyName: name,
			Region:      domain.Region{Province: province, City: city, District: "西湖区"},
		},
		Visibility: domain.VisSelf,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func withCategories(cats ...string) func(*domain.Supplier) {
	return func(s *domain.Supplier) { s.Categories = cats }
}

func withQual(level string) func(*domain.Supplier) {
	return func(s *domain.Supplier) {
		s.Qualifications = append(s.Qualifications, domain.Qualification{
			Type: "建筑工程施工总承包", Level: level,
		})
	}
}

func withRating(r float64) func(*domain.Supplier) {
	return func(s *domain.Supplier) { s.Rating = r }
}

func withCreatedAt(ts time.Time) func(*domain.Supplier) {
	return func(s *domain.Supplier) {
		s.CreatedAt = ts
		s.UpdatedAt = ts
	}
}

func mustPut(t *testing.T, st datamodel.SupplierStore, docs ...*domain.Supplier) {
	t.Helper()
	ctx := context.Background()
	for _, d := range docs {
		if err := st.Put(ctx, d); err != nil {
			t.Fatalf("Put(%s): %v", d.ID, err)
		}
	}
}

// ---------- cases ----------

func testPutAndGet(t *testing.T, st datamodel.SupplierStore) {
	ctx := context.Background()
	doc := supplier("sup_t_000001", "杭州一建有限公司", "浙江", "杭州", "user_a",
		withCategories("施工服务", "市政工程"),
		withQual("二级"),
	)
	doc.CustomFields = map[string]any{"垫资能力": "500万以内"}
	mustPut(t, st, doc)

	got, err := st.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BasicInfo.CompanyName != "杭州一建有限公司" {
		t.Errorf("name = %q", got.BasicInfo.CompanyName)
	}
	if got.BasicInfo.Region.City != "杭州" {
		t.Errorf("city = %q", got.BasicInfo.Region.City)
	}
	if len(got.Qualifications) != 1 || got.Qualifications[0].Level != "二级" {
		t.Errorf("qualifications = %+v", got.Qualifications)
	}
	if got.CustomFields["垫资能力"] != "500万以内" {
		t.Errorf("custom fields = %+v", got.CustomFields)
	}
	if got.Status != domain.StatusActive {
		t.Errorf("status = %q", got.Status)
	}
}

func testGetNotFound(t *testing.T, st datamodel.SupplierStore) {
	if _, err := st.Get(context.Background(), "sup_does_not_exist"); !errors.Is(err, datamodel.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func testUpsertReplaces(t *testing.T, st datamodel.SupplierStore) {
	ctx := context.Background()
	doc := supplier("sup_t_000002", "旧名称", "浙江", "宁波", "user_a")
	mustPut(t, st, doc)

	doc.BasicInfo.CompanyName = "新名称"
	doc.BasicInfo.LegalPerson = "张三"
	mustPut(t, st, doc)

	got, err := st.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BasicInfo.CompanyName != "新名称" || got.BasicInfo.LegalPerson != "张三" {
		t.Errorf("upsert did not replace: %+v", got.BasicInfo)
	}
}

func testDeleteIsArchive(t *testing.T, st datamodel.SupplierStore) {
	ctx := context.Background()
	doc := supplier("sup_t_000003", "将归档有限公司", "浙江", "温州", "user_a")
	mustPut(t, st, doc)

	if err := st.Delete(ctx, doc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// History retained: Get still returns the document...
	got, err := st.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("archived doc should remain readable: %v", err)
	}
	if got.Status != domain.StatusArchived || got.ArchivedAt == nil {
		t.Errorf("expected archived status + timestamp, got %q / %v", got.Status, got.ArchivedAt)
	}

	// ...but default List hides it.
	page, err := st.List(ctx, datamodel.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, item := range page.Items {
		if item.ID == doc.ID {
			t.Fatal("archived document must not appear in default List")
		}
	}

	// IncludeArchived surfaces it.
	page, err = st.List(ctx, datamodel.Query{Filter: datamodel.SupplierFilter{IncludeArchived: true}})
	if err != nil {
		t.Fatalf("List include-archived: %v", err)
	}
	found := false
	for _, item := range page.Items {
		if item.ID == doc.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("archived document missing with IncludeArchived")
	}
}

func testDeleteNotFound(t *testing.T, st datamodel.SupplierStore) {
	if err := st.Delete(context.Background(), "sup_missing"); !errors.Is(err, datamodel.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func testPaginationKeyset(t *testing.T, st datamodel.SupplierStore) {
	// 25 documents with strictly increasing creation time; walk pages
	// of 10 and require every id exactly once, in order.
	docs := make([]*domain.Supplier, 0, 25)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 25; i++ {
		docs = append(docs, supplier(
			fmt.Sprintf("sup_page_%06d", i+1),
			fmt.Sprintf("分页测试公司%02d", i+1),
			"浙江", "杭州", "user_a",
			withCreatedAt(base.Add(time.Duration(i)*time.Hour)),
		))
	}
	mustPut(t, st, docs...)

	ctx := context.Background()
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	var prevID string
	for {
		page, err := st.List(ctx, datamodel.Query{Limit: 10, Cursor: cursor})
		if err != nil {
			t.Fatalf("List page %d: %v", pages, err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("id %s returned twice", item.ID)
			}
			seen[item.ID] = true
			// Descending creation order: newest first.
			if prevID != "" && item.ID > prevID {
				t.Fatalf("order violated: %s after %s (expected newest-first)", item.ID, prevID)
			}
			prevID = item.ID
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 25 {
		t.Fatalf("walked %d/25 documents", len(seen))
	}
	if pages != 3 {
		t.Fatalf("expected 3 pages (10/10/5), got %d", pages)
	}
}

func testLimitCapped(t *testing.T, st datamodel.SupplierStore) {
	docs := make([]*domain.Supplier, 0, 105)
	for i := 0; i < 105; i++ {
		docs = append(docs, supplier(
			fmt.Sprintf("sup_cap_%06d", i+1),
			fmt.Sprintf("上限测试公司%03d", i+1),
			"江苏", "南京", "user_a",
			withCreatedAt(time.Date(2026, 3, 1, 0, i, 0, 0, time.UTC)),
		))
	}
	mustPut(t, st, docs...)

	page, err := st.List(context.Background(), datamodel.Query{Limit: 999})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != datamodel.MaxPageSize {
		t.Fatalf("page size = %d, want hard cap %d", len(page.Items), datamodel.MaxPageSize)
	}
}

func testFilterRegion(t *testing.T, st datamodel.SupplierStore) {
	mustPut(t, st,
		supplier("sup_r_1", "杭州公司", "浙江", "杭州", "u"),
		supplier("sup_r_2", "宁波公司", "浙江", "宁波", "u"),
		supplier("sup_r_3", "南京公司", "江苏", "南京", "u"),
	)
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Province: "浙江", City: "杭州"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "sup_r_1" {
		t.Fatalf("region filter = %d items", len(page.Items))
	}
}

func testFilterCategories(t *testing.T, st datamodel.SupplierStore) {
	mustPut(t, st,
		supplier("sup_c_1", "施工商", "浙江", "杭州", "u", withCategories("施工服务", "市政工程")),
		supplier("sup_c_2", "贸易商", "浙江", "杭州", "u", withCategories("货物采购")),
		supplier("sup_c_3", "开发商", "浙江", "杭州", "u", withCategories("定制开发")),
	)
	// OR semantics within the slice: either tag matches.
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Categories: []string{"市政工程", "货物采购"}},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("category OR filter = %d items, want 2", len(page.Items))
	}
}

func testFilterMinQualRank(t *testing.T, st datamodel.SupplierStore) {
	mustPut(t, st,
		supplier("sup_q_1", "特级资质商", "浙江", "杭州", "u", withQual("特级")),
		supplier("sup_q_2", "二级资质商", "浙江", "杭州", "u", withQual("二级")),
		supplier("sup_q_3", "无资质商", "浙江", "杭州", "u"),
	)
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{MinQualRank: domain.QualRank("一级")},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "sup_q_1" {
		t.Fatalf("qual-rank filter = %d items, want only 特级", len(page.Items))
	}
}

func testFilterRatingRange(t *testing.T, st datamodel.SupplierStore) {
	mustPut(t, st,
		supplier("sup_s_1", "高分商", "浙江", "杭州", "u", withRating(4.8)),
		supplier("sup_s_2", "中分商", "浙江", "杭州", "u", withRating(3.5)),
		supplier("sup_s_3", "低分商", "浙江", "杭州", "u", withRating(2.0)),
	)
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{MinRating: 4.0},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "sup_s_1" {
		t.Fatalf("min-rating filter = %d items", len(page.Items))
	}

	page, err = st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{MinRating: 3.0, MaxRating: 4.0},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "sup_s_2" {
		t.Fatalf("rating-range filter = %d items", len(page.Items))
	}
}

func testFilterOwner(t *testing.T, st datamodel.SupplierStore) {
	mustPut(t, st,
		supplier("sup_o_1", "甲的供应商", "浙江", "杭州", "user_a"),
		supplier("sup_o_2", "乙的供应商", "浙江", "杭州", "user_b"),
	)
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{OwnerID: "user_b"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "sup_o_2" {
		t.Fatalf("owner filter = %d items", len(page.Items))
	}
}

func testFilterKeyword(t *testing.T, st datamodel.SupplierStore) {
	mustPut(t, st,
		supplier("sup_k_1", "杭州土石方工程有限公司", "浙江", "杭州", "u",
			withCategories("施工服务")),
		supplier("sup_k_2", "杭州建材贸易有限公司", "浙江", "杭州", "u",
			withCategories("货物采购")),
	)
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Keyword: "土石方"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "sup_k_1" {
		t.Fatalf("keyword filter = %d items", len(page.Items))
	}
}

func testStoredCopyIndependent(t *testing.T, st datamodel.SupplierStore) {
	doc := supplier("sup_ind_000001", "原始名称", "浙江", "杭州", "u")
	mustPut(t, st, doc)

	// Mutate the caller's object after Put.
	doc.BasicInfo.CompanyName = "调用方篡改"
	doc.Categories = append(doc.Categories, "幽灵品类")

	got, err := st.Get(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BasicInfo.CompanyName != "原始名称" {
		t.Errorf("stored document aliases caller memory: name = %q", got.BasicInfo.CompanyName)
	}
	if len(got.Categories) != 0 {
		t.Errorf("stored document aliases caller slice: %v", got.Categories)
	}

	// Mutate the returned object; a fresh Get must be unaffected.
	got.BasicInfo.CompanyName = "读取方篡改"
	again, err := st.Get(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.BasicInfo.CompanyName != "原始名称" {
		t.Errorf("returned document aliases stored memory: name = %q", again.BasicInfo.CompanyName)
	}
}

func testPing(t *testing.T, st datamodel.SupplierStore) {
	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
