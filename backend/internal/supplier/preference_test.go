package supplier_test

// Tests for 本地供应商偏好 (local-first ranking): the persisted home-region
// preference is auto-filled by Service.List so HTTP/CLI/MCP share one
// behavior. It RANKS same-region suppliers first, never filters others out;
// explicit query hints override the saved preference and the per-query
// opt-out (NoLocalPreference) disables it.

import (
	"context"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// prefInput builds a clean create input in the given region.
func prefInput(name, province, city string) supplier.CreateInput {
	in := cleanInput()
	in.BasicInfo.CompanyName = name
	in.BasicInfo.Region = domain.Region{Province: province, City: city}
	in.Qualifications = nil
	in.Categories = nil
	return in
}

func TestPreferenceSaveLoadClear(t *testing.T) {
	st := memory.New()
	svc := supplier.NewService(st)
	ctx := context.Background()

	// Province is mandatory; trimming is applied.
	if _, err := svc.SaveLocalPreference(ctx, supplier.LocalPreference{Province: "  "}); err == nil {
		t.Fatal("empty province must be rejected")
	}
	p, err := svc.SaveLocalPreference(ctx, supplier.LocalPreference{Province: " 浙江 ", City: " 杭州 "})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if p.Province != "浙江" || p.City != "杭州" {
		t.Fatalf("trimmed preference wrong: %+v", p)
	}
	got, err := svc.LoadLocalPreference(ctx)
	if err != nil || got.Province != "浙江" || got.City != "杭州" {
		t.Fatalf("Load round-trip wrong: %+v err=%v", got, err)
	}

	// Clear collapses to an empty (inert) preference.
	if err := svc.ClearLocalPreference(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, _ = svc.LoadLocalPreference(ctx)
	if !got.Empty() {
		t.Fatalf("after clear preference must be empty, got %+v", got)
	}

	// A corrupt raw setting must not break listing or wedge the feature.
	if err := st.PutSetting(ctx, "local_preference", "{not json"); err != nil {
		t.Fatalf("seed corrupt setting: %v", err)
	}
	got, err = svc.LoadLocalPreference(ctx)
	if err != nil || !got.Empty() {
		t.Fatalf("corrupt preference must collapse to empty, got %+v err=%v", got, err)
	}
}

func TestPreferenceListAutoApply(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	// Create LOCAL first (older) — under plain created_at-desc it would
	// sort last; local-first must still bring it to the top.
	local, err := svc.Create(ctx, prefInput("杭州本地公司", "浙江", "杭州"))
	if err != nil {
		t.Fatal(err)
	}
	prov, err := svc.Create(ctx, prefInput("宁波同省公司", "浙江", "宁波"))
	if err != nil {
		t.Fatal(err)
	}
	far, err := svc.Create(ctx, prefInput("南京外省公司", "江苏", "南京"))
	if err != nil {
		t.Fatal(err)
	}

	// Before configuring a preference: newest first, no local effect.
	page, err := svc.List(ctx, datamodel.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ID != far.ID {
		t.Fatalf("without preference expected newest first, got %s", page.Items[0].Name)
	}

	if _, err := svc.SaveLocalPreference(ctx, supplier.LocalPreference{Province: "浙江", City: "杭州"}); err != nil {
		t.Fatal(err)
	}

	// City preference: 杭州 leads; the out-of-province row is still present
	// (ranking, never filtering).
	page, err = svc.List(ctx, datamodel.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("preference must not filter rows, got %d", len(page.Items))
	}
	if page.Items[0].ID != local.ID {
		t.Fatalf("local supplier must rank first, got %q", page.Items[0].Name)
	}
	// 宁波 is same-province but NOT same city: prio 0 like the 江苏 row,
	// so those two tie and fall back to newest-first (南京 then 宁波).
	if page.Items[1].ID != far.ID || page.Items[2].ID != prov.ID {
		t.Fatalf("non-city-local ties should keep date order, got %q %q %q",
			page.Items[0].Name, page.Items[1].Name, page.Items[2].Name)
	}

	// Per-query opt-out restores the plain created_at order.
	page, err = svc.List(ctx, datamodel.Query{Limit: 10, NoLocalPreference: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ID != far.ID {
		t.Fatalf("NoLocalPreference must restore newest-first, got %q", page.Items[0].Name)
	}

	// Explicit query hint OVERRIDES the saved preference (saved 杭州, query
	// prefers the whole 浙江 province): both 浙江 docs lead 江苏.
	page, err = svc.List(ctx, datamodel.Query{
		Limit: 10,
		Filter: datamodel.SupplierFilter{
			PreferProvince: "江苏", // explicit hint wins: 江苏 ranks first now
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ID != far.ID {
		t.Fatalf("explicit PreferProvince must override saved preference, got %q", page.Items[0].Name)
	}

	// Province-only saved preference (city cleared): every 浙江 doc leads.
	if _, err := svc.SaveLocalPreference(ctx, supplier.LocalPreference{Province: "浙江"}); err != nil {
		t.Fatal(err)
	}
	page, err = svc.List(ctx, datamodel.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[2].ID != far.ID {
		t.Fatalf("province-only preference must rank both 浙江 docs above 江苏, order=%v/%v/%v",
			page.Items[0].Name, page.Items[1].Name, page.Items[2].Name)
	}
	if page.Items[0].ID != prov.ID && page.Items[1].ID != prov.ID {
		t.Fatal("same-province 宁波 doc must be in the leading tier")
	}
}
