package sqlite_test

// Keyset pagination with the local-preference ranking is the most complex
// SQL path in the adapter (prio CASE repeats across the keyset predicate
// and ORDER BY, with interwoven bind args). This property test pages a
// mixed-region library through small pages on every sort direction and
// asserts: no error, no duplicate/missing row, and every local supplier
// precedes every non-local one.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
)

func TestLocalPreferencePaginationCompleteness(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const n = 30
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		prov, city := "浙江", "杭州"
		if i%2 == 1 {
			prov, city = "江苏", "南京"
		}
		doc := &domain.Supplier{
			ID: fmt.Sprintf("sup_%03d", i), Owner: "u", Status: domain.StatusActive,
			BasicInfo: domain.BasicInfo{
				CompanyName: fmt.Sprintf("公司%03d", i),
				Region:      domain.Region{Province: prov, City: city},
			},
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
			UpdatedAt: base.Add(time.Duration(i) * time.Hour),
		}
		if err := st.Put(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}

	pref := datamodel.SupplierFilter{PreferProvince: "浙江", PreferCity: "杭州"}
	for _, field := range []datamodel.SortField{
		datamodel.SortCreatedAt, datamodel.SortRating, datamodel.SortName,
	} {
		for _, order := range []datamodel.SortOrder{datamodel.OrderDesc, datamodel.OrderAsc} {
			t.Run(fmt.Sprintf("%s/%s", field, order), func(t *testing.T) {
				cursor := ""
				seen := map[string]bool{}
				var orderedIDs []string
				pages := 0
				for {
					page, err := st.List(ctx, datamodel.Query{
						Limit: 7, Cursor: cursor,
						Sort:   datamodel.Sort{Field: field, Order: order},
						Filter: pref,
					})
					if err != nil {
						t.Fatalf("page %d: %v", pages, err)
					}
					for _, d := range page.Items {
						if seen[d.ID] {
							t.Fatalf("duplicate row %s across pages", d.ID)
						}
						seen[d.ID] = true
						orderedIDs = append(orderedIDs, d.ID)
					}
					pages++
					if page.NextCursor == "" {
						break
					}
					cursor = page.NextCursor
					if pages > n {
						t.Fatal("pagination did not terminate")
					}
				}
				if len(seen) != n {
					t.Fatalf("got %d unique rows, want %d", len(seen), n)
				}
				// Exactly n/2 local suppliers must lead the whole result.
				for i := 0; i < n/2; i++ {
					d, _ := st.Get(ctx, orderedIDs[i])
					if d.BasicInfo.Region.City != "杭州" {
						t.Fatalf("position %d (%s) is not local — local-first ordering broken", i, orderedIDs[i])
					}
				}
			})
		}
	}
}
