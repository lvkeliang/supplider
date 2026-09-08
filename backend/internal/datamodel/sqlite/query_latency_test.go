package sqlite_test

// Search/query performance red lines (PRD 性能红线): keyword full-text
// search < 200ms, structured condition query P99 < 100ms, on a realistic
// library. We seed a few thousand suppliers through the service (real
// inserts with FTS indexing) and measure the live List/search path.
//
// Thresholds carry generous headroom over the red line because CI/vm
// hardware varies; the point is to catch an accidental full table scan or
// missing-index regression, not to micro-benchmark a warm cache.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// seedLibrary inserts n suppliers spread across cities/categories, with
// Chinese names containing common search terms.
func seedLibrary(t *testing.T, svc *supplier.Service, n int) {
	t.Helper()
	ctx := context.Background()
	cities := []struct{ prov, city string }{
		{"浙江", "杭州"}, {"浙江", "宁波"}, {"浙江", "温州"},
		{"江苏", "南京"}, {"江苏", "苏州"},
	}
	cats := [][]string{
		{"施工服务"}, {"市政工程"}, {"建材贸易"}, {"商品混凝土"},
	}
	for i := 0; i < n; i++ {
		c := cities[i%len(cities)]
		cat := cats[i%len(cats)]
		in := supplier.CreateInput{
			BasicInfo: domain.BasicInfo{
				CompanyName: fmt.Sprintf("%s%s供应商%04d", c.city, []string{"建设", "商砼", "混凝土", "建材"}[i%4], i),
				Region:      domain.Region{Province: c.prov, City: c.city},
				LegalPerson: "法人",
			},
			Categories: cat,
		}
		if i%1000 == 0 {
			t.Logf("seeded %d/%d ...", i, n)
		}
		if _, err := svc.Create(ctx, in); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

func pct(ds []int64, p float64) int64 {
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	idx := int(float64(len(ds)-1) * p)
	return ds[idx]
}

func TestSearchAndFilterLatency(t *testing.T) {
	// Opt-in (seeds a 5k library — ~25s): run with
	// SUPPLIDER_PERF=1 go test -tags personal -run TestSearchAndFilterLatency ./internal/datamodel/sqlite/
	if os.Getenv("SUPPLIDER_PERF") == "" {
		t.Skip("latency guard skipped; set SUPPLIDER_PERF=1 to run")
	}
	const n = 5000
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "latency.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	svc := supplier.NewService(store)
	ctx := context.Background()

	seedLibrary(t, svc, n)

	measure := func(query datamodel.Query, iters int) (p50, p99 int64) {
		durs := make([]int64, 0, iters)
		for i := 0; i < iters; i++ {
			query.Limit = 20
			start := time.Now()
			if _, err := svc.List(ctx, query); err != nil {
				t.Fatalf("list: %v", err)
			}
			durs = append(durs, time.Since(start).Nanoseconds())
		}
		return pct(durs, 0.50), pct(durs, 0.99)
	}

	// Keyword FTS search (a term present in ~1/4 of the library).
	_, searchP99 := measure(datamodel.Query{Filter: datamodel.SupplierFilter{Keyword: "混凝土"}}, 200)
	// Structured filters (region + category).
	_, filterP99 := measure(datamodel.Query{Filter: datamodel.SupplierFilter{
		Province:   "浙江",
		City:       "杭州",
		Categories: []string{"市政工程"},
	}}, 200)

	t.Logf("library=%d  FTS-search p99=%dms  structured-filter p99=%dms",
		n, searchP99/1e6, filterP99/1e6)

	if searchP99 > 200_000_000 { // 200ms
		t.Errorf("FTS search p99 = %dms, exceeds 200ms red line", searchP99/1e6)
	}
	if filterP99 > 100_000_000 { // 100ms
		t.Errorf("structured filter p99 = %dms, exceeds 100ms red line", filterP99/1e6)
	}
}
