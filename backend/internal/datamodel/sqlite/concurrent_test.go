package sqlite_test

// Multi-process write concurrency. The docs (docs/mcp/SETUP.md) promise the
// Tauri sidecar and a separately-running srm-mcp can read/write the SAME
// SQLite file concurrently ("WAL, multi-process safe"). In-process this is
// two *Store handles opened on one file; WAL allows one writer at a time and
// busy_timeout(5000) makes the loser wait instead of failing with
// SQLITE_BUSY. This pins that the DSN pragmas actually deliver the claim
// under interleaved writes (every Put updates rows + categories + FTS).

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
)

func TestConcurrentWritesTwoHandlesSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open a: %v", err)
	}
	defer a.Close()
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	defer b.Close()

	now := time.Now().UTC()
	mk := func(i int) *domain.Supplier {
		return &domain.Supplier{
			ID:     fmt.Sprintf("sup_%04d", i),
			Owner:  "u",
			Status: domain.StatusActive,
			BasicInfo: domain.BasicInfo{
				CompanyName: fmt.Sprintf("并发公司%04d", i),
				Region:      domain.Region{Province: "浙江", City: "杭州"},
			},
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	const n = 40
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		store := a
		if i%2 == 0 {
			store = b
		}
		go func(s datamodel.SupplierStore, i int) {
			defer wg.Done()
			errs <- s.Put(context.Background(), mk(i))
		}(store, i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Put failed (SQLITE_BUSY?): %v", err)
		}
	}

	// Both handles must see every committed row.
	page, err := a.List(context.Background(), datamodel.Query{
		Filter: datamodel.SupplierFilter{IncludeArchived: true},
		Limit:  datamodel.MaxPageSize,
	})
	if err != nil {
		t.Fatalf("list after concurrent writes: %v", err)
	}
	if len(page.Items) != n {
		t.Fatalf("rows = %d, want %d (a concurrent Put was lost)", len(page.Items), n)
	}

	got, err := b.Get(context.Background(), "sup_0001")
	if err != nil {
		t.Fatalf("second handle cannot read committed row: %v", err)
	}
	if got.BasicInfo.CompanyName != "并发公司0001" {
		t.Errorf("read-back name = %q", got.BasicInfo.CompanyName)
	}
}
