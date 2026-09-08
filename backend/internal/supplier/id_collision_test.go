package supplier_test

// Create must never silently overwrite another supplier on an ID collision.
// Put is an upsert in every adapter (Update relies on that), so the service
// probes Get before writing and regenerates the id. A 6-digit random suffix
// makes collisions reachable in real databases (~50% around 1.2k rows), so
// this is not purely theoretical — an earlier version retried only on
// ErrConflict, which no adapter returns, and overwrote the existing doc.

import (
	"context"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestCreateIDCollisionRegenerates(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	first, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	// First generated id collides with the existing supplier; the next
	// one is free. Without the Get guard the upsert would overwrite
	// `first` (same name, one row total).
	calls := 0
	svc.WithIDGenerator(func() string {
		calls++
		if calls == 1 {
			return first.ID
		}
		return "sup_2026_999999"
	})

	in := validInput()
	in.BasicInfo.CompanyName = "全新供应商不覆盖旧档"
	second, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create after collision: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("colliding id was reused: %s", second.ID)
	}
	if second.ID != "sup_2026_999999" {
		t.Fatalf("expected regenerated id, got %s", second.ID)
	}
	// The pre-existing supplier must be untouched.
	got, err := svc.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get seeded supplier: %v", err)
	}
	if got.ID != first.ID || got.BasicInfo.CompanyName != validInput().BasicInfo.CompanyName {
		t.Fatalf("seeded supplier was overwritten: id=%s name=%q", got.ID, got.BasicInfo.CompanyName)
	}
	if second.BasicInfo.CompanyName != "全新供应商不覆盖旧档" {
		t.Fatalf("new supplier fields wrong: %q", second.BasicInfo.CompanyName)
	}
}

func TestCreateIDExhaustionErrors(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	first, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	// Every generated id is taken → retries run out and Create fails
	// instead of overwriting.
	svc.WithIDGenerator(func() string { return first.ID })

	if _, err := svc.Create(ctx, validInput()); err == nil ||
		!strings.Contains(err.Error(), "unique id") {
		t.Fatalf("want unique-id exhaustion error, got %v", err)
	}
	got, _ := svc.Get(ctx, first.ID)
	if got.BasicInfo.CompanyName != validInput().BasicInfo.CompanyName {
		t.Fatal("existing supplier must remain intact after failed create")
	}
}
