package supplier_test

// Tests for the persisted visibility policy (可见性策略配置): the admin-set
// cap/buffer lives in the store's settings so it survives restarts, is
// shared by every process opening the library (sidecar / MCP / CLI via
// API), and drives the automatic disposition sweep.

import (
	"context"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// Unconfigured store reports the normalized fallback, not "configured".
func TestLoadVisibilityPolicyFallback(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	p, configured, err := svc.LoadVisibilityPolicy(ctx, supplier.VisibilityPolicy{MaxLevel: 1})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if configured {
		t.Errorf("fresh store must report configured=false")
	}
	// Fallback is normalized: zero buffer days become the PRD 7-day default.
	if p.MaxLevel != 1 || p.BufferDays != supplier.DefaultBufferDays {
		t.Errorf("fallback policy wrong: %+v", p)
	}
}

// Saved policy round-trips and is normalized; a second service over the
// same store sees it (simulates sidecar restart / MCP opening the library).
func TestSaveLoadVisibilityPolicy(t *testing.T) {
	store := memory.New()
	ctx := context.Background()

	svc1 := supplier.NewService(store)
	saved, err := svc1.SaveVisibilityPolicy(ctx, supplier.VisibilityPolicy{MaxLevel: 0})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.BufferDays != supplier.DefaultBufferDays {
		t.Errorf("save should normalize buffer days, got %d", saved.BufferDays)
	}

	// A different service instance over the SAME store picks up the
	// persisted policy (sidecar restart / multi-process).
	svc2 := supplier.NewService(store)
	p, configured, err := svc2.LoadVisibilityPolicy(ctx, supplier.VisibilityPolicy{MaxLevel: 4})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !configured {
		t.Errorf("saved policy must report configured=true")
	}
	if p.MaxLevel != 0 || p.BufferDays != supplier.DefaultBufferDays {
		t.Errorf("persisted policy mismatch: %+v", p)
	}
}

// The persisted policy is what scan/enforce now default to when no
// explicit override is passed — a cap tightened to 0 flags an L1 record
// through a fresh service with no per-call policy argument.
func TestPersistedPolicyDrivesEnforce(t *testing.T) {
	store := memory.New()
	svc := supplier.NewService(store)
	ctx := context.Background()

	doc, err := svc.Create(ctx, namedInput("策略收紧测试公司", "91330100MA27X3001K", "杭州"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Personal-tier L1 (shared) record.
	doc, err = svc.Update(ctx, doc.ID, supplier.UpdateInput{Visibility: intPtr(1)})
	if err != nil {
		t.Fatalf("set visibility: %v", err)
	}

	// Tighten the persisted cap to L0 (仅自己).
	if _, err := svc.SaveVisibilityPolicy(ctx, supplier.VisibilityPolicy{MaxLevel: 0}); err != nil {
		t.Fatalf("save policy: %v", err)
	}

	// Load through the same seam the sidecar/HTTP/MCP use, then enforce.
	policy, _, err := svc.LoadVisibilityPolicy(ctx, supplier.VisibilityPolicy{MaxLevel: 4})
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	rep, err := svc.EnforceVisibilityPolicy(ctx, policy)
	if err != nil {
		t.Fatalf("enforce: %v", err)
	}
	if rep.Flagged != 1 {
		t.Errorf("persisted cap L0 should flag the L1 record, flagged=%d", rep.Flagged)
	}
}

func intPtr(n int) *int { return &n }
