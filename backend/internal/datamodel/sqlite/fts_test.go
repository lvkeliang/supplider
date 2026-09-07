package sqlite_test

// FTS5 full-text search behavior (personal tier transition search
// engine): trigram substring matching over Chinese text, pinyin lookups
// (full syllables and initials), index-time synonyms, short-term LIKE
// fallback and index sync on updates. These complement the shared
// contract suite (which only pins naive keyword semantics).

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
)

func ftsStore(t *testing.T) datamodel.SupplierStore {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "supplider.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func putSupplier(t *testing.T, st datamodel.SupplierStore, id, name string, opts ...func(*domain.Supplier)) *domain.Supplier {
	t.Helper()
	now := time.Now().UTC()
	doc := &domain.Supplier{
		ID:     id,
		Owner:  "u",
		Status: domain.StatusActive,
		BasicInfo: domain.BasicInfo{
			CompanyName: name,
			Region:      domain.Region{Province: "浙江", City: "杭州", District: "西湖区"},
		},
		Visibility: domain.VisSelf,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	for _, o := range opts {
		o(doc)
	}
	if err := st.Put(context.Background(), doc); err != nil {
		t.Fatalf("Put %s: %v", id, err)
	}
	return doc
}

func searchIDs(t *testing.T, st datamodel.SupplierStore, kw string) []string {
	t.Helper()
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Keyword: kw},
	})
	if err != nil {
		t.Fatalf("List keyword=%q: %v", kw, err)
	}
	ids := make([]string, 0, len(page.Items))
	for _, d := range page.Items {
		ids = append(ids, d.ID)
	}
	return ids
}

// TestFTSSetup indexes two suppliers with distinctive names.
func ftsFixture(t *testing.T, st datamodel.SupplierStore) {
	t.Helper()
	putSupplier(t, st, "sup_fts_1", "杭州一建建设有限公司",
		func(d *domain.Supplier) { d.Categories = []string{"施工服务"} })
	putSupplier(t, st, "sup_fts_2", "宁波建材贸易有限公司",
		func(d *domain.Supplier) {
			d.Categories = []string{"货物采购"}
			d.BasicInfo.BusinessScope = "水泥、砂石批发"
			d.BasicInfo.Region = domain.Region{Province: "浙江", City: "宁波", District: "鄞州区"}
		})
}

func TestFTSTrigramSubstring(t *testing.T) {
	st := ftsStore(t)
	ftsFixture(t, st)

	// ≥3-char Chinese substring (trigram MATCH).
	ids := searchIDs(t, st, "杭州一建")
	if len(ids) != 1 || ids[0] != "sup_fts_1" {
		t.Errorf(`"杭州一建" = %v, want [sup_fts_1]`, ids)
	}
	// Mid-name substring works without word boundaries.
	ids = searchIDs(t, st, "建材贸易")
	if len(ids) != 1 || ids[0] != "sup_fts_2" {
		t.Errorf(`"建材贸易" = %v, want [sup_fts_2]`, ids)
	}
}

func TestFTSShortTermLikeFallback(t *testing.T) {
	st := ftsStore(t)
	ftsFixture(t, st)

	// 2-char terms are below the trigram minimum and fall back to LIKE:
	// "水泥" lives in business_scope and MUST be findable.
	ids := searchIDs(t, st, "水泥")
	if len(ids) != 1 || ids[0] != "sup_fts_2" {
		t.Errorf(`"水泥" = %v, want [sup_fts_2] (short-term LIKE fallback)`, ids)
	}
	ids = searchIDs(t, st, "杭州")
	if len(ids) != 1 || ids[0] != "sup_fts_1" {
		t.Errorf(`"杭州" = %v, want [sup_fts_1]`, ids)
	}
}

func TestFTSPinyin(t *testing.T) {
	st := ftsStore(t)
	ftsFixture(t, st)

	// Full concatenated pinyin (substring of the per-field token).
	ids := searchIDs(t, st, "hangzhouyijian")
	if len(ids) != 1 || ids[0] != "sup_fts_1" {
		t.Errorf(`pinyin "hangzhouyijian" = %v, want [sup_fts_1]`, ids)
	}
	// Initial-letter abbreviation 杭州一建 → hzyj.
	ids = searchIDs(t, st, "hzyj")
	if len(ids) != 1 || ids[0] != "sup_fts_1" {
		t.Errorf(`initials "hzyj" = %v, want [sup_fts_1]`, ids)
	}
	// Case-insensitive Latin.
	ids = searchIDs(t, st, "NINGBO")
	if len(ids) != 1 || ids[0] != "sup_fts_2" {
		t.Errorf(`pinyin "NINGBO" = %v, want [sup_fts_2]`, ids)
	}
}

func TestFTSSynonyms(t *testing.T) {
	st := ftsStore(t)
	// Supplier writes 商砼; users search the standard term 混凝土
	// (3 chars → trigram MATCH over the synonym-augmented content).
	putSupplier(t, st, "sup_syn_1", "杭州商砼供应站",
		func(d *domain.Supplier) { d.BasicInfo.BusinessScope = "商砼生产与配送" })

	ids := searchIDs(t, st, "混凝土")
	if len(ids) != 1 || ids[0] != "sup_syn_1" {
		t.Errorf(`"混凝土" = %v, want [sup_syn_1] via synonym expansion`, ids)
	}
	// Reverse direction: searching the colloquial term also works.
	ids = searchIDs(t, st, "商品混凝土")
	if len(ids) != 1 || ids[0] != "sup_syn_1" {
		t.Errorf(`"商品混凝土" = %v, want [sup_syn_1]`, ids)
	}

	// A document written with the STANDARD term must be findable by the
	// 2-char colloquial "商砼" — below the trigram minimum, so this
	// exercises the LIKE fallback against the synonym-augmented
	// search_text (expansions live in BOTH index paths). sup_syn_1 also
	// matches (it literally says 商砼); assert the synonym-only doc hits.
	putSupplier(t, st, "sup_syn_2", "宁波混凝土制品厂",
		func(d *domain.Supplier) { d.BasicInfo.BusinessScope = "混凝土预制构件" })
	ids = searchIDs(t, st, "商砼")
	if !contains(ids, "sup_syn_2") {
		t.Errorf(`"商砼" = %v, want it to include sup_syn_2 via synonym in LIKE fallback`, ids)
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestFTSKeywordCombinedWithFilter(t *testing.T) {
	st := ftsStore(t)
	ftsFixture(t, st)

	// Keyword narrows via FTS; the structured city filter ANDs on top —
	// a Hangzhou pinyin match must not survive a Ningbo city filter.
	page, err := st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Keyword: "hangzhou", City: "宁波"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("FTS keyword + city filter = %d rows, want 0", len(page.Items))
	}

	page, err = st.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{Keyword: "宁波建材", City: "杭州"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// "宁波" is a 2-char LIKE term and "建材" is 2-char too; the Hangzhou
	// city filter excludes the only keyword match.
	if len(page.Items) != 0 {
		t.Errorf("short keyword + city filter = %d rows, want 0", len(page.Items))
	}
}

func TestFTSIndexSyncsOnUpdate(t *testing.T) {
	st := ftsStore(t)
	doc := putSupplier(t, st, "sup_upd_1", "旧名称土石方工程队")

	// Rename via Put (upsert): old name stops matching, new name starts.
	doc.BasicInfo.CompanyName = "新名称智能科技有限公司"
	doc.UpdatedAt = doc.UpdatedAt.Add(time.Second)
	if err := st.Put(context.Background(), doc); err != nil {
		t.Fatalf("Put update: %v", err)
	}
	if ids := searchIDs(t, st, "旧名称土石方"); len(ids) != 0 {
		t.Errorf(`old name still indexed: %v`, ids)
	}
	if ids := searchIDs(t, st, "智能科技"); len(ids) != 1 || ids[0] != "sup_upd_1" {
		t.Errorf(`new name not indexed: %v`, ids)
	}
}

func TestFTSBackfillOnReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supplider.db")

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	putSupplier(t, st, "sup_bf_1", "温州回填测试建设公司")
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: the backfill path runs at Open; keyword search must work.
	st2, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open reopen: %v", err)
	}
	defer st2.Close()
	if ids := searchIDs(t, st2, "回填测试"); len(ids) != 1 || ids[0] != "sup_bf_1" {
		t.Errorf("after reopen keyword = %v, want [sup_bf_1]", ids)
	}
}
