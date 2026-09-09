package supplier_test

// Tests for the visibility policy-tightening disposition flow
// (可见性策略收紧数据处置): flag 待调整 → 7-day buffer → owner adjustment /
// appeal (pauses countdown) / timeout auto-downgrade, plus admin appeal
// resolution (grant = exception, deny = immediate downgrade).

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// visPolicy is the personal-tier policy: levels 0/1 only, 7-day buffer.
var visPolicy = supplier.VisibilityPolicy{MaxLevel: domain.VisNamedUsers, BufferDays: 7}

// newVisService returns a service with a mutable clock so buffer deadlines
// are deterministic.
func newVisService(clock *time.Time) *supplier.Service {
	svc := supplier.NewService(memory.New())
	svc.WithClock(func() time.Time { return *clock })
	return svc
}

func visInput(vis int) supplier.CreateInput {
	in := cleanInput()
	in.Visibility = vis
	return in
}

func TestVisibilityEnforceFlagsThenTimeoutDowngrades(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	svc := newVisService(&now)
	ctx := context.Background()

	doc, err := svc.Create(ctx, visInput(domain.VisCompany)) // level 4 > cap 1
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	compliant, err := svc.Create(ctx, visInput(domain.VisSelf)) // level 0: untouched
	if err != nil {
		t.Fatalf("Create compliant: %v", err)
	}

	// Pre-enforce scan: one raw violation, nothing mutated yet.
	vs, err := svc.ScanVisibilityViolations(ctx, visPolicy)
	if err != nil || len(vs) != 1 || vs[0].State != supplier.ViolationNew {
		t.Fatalf("scan before enforce: %+v err=%v", vs, err)
	}

	// First sweep flags the violator and starts the buffer (visibility is
	// NOT changed yet — the owner gets 7 days).
	rep, err := svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if rep.Flagged != 1 || rep.Pending != 0 || rep.Downgraded != 0 || len(rep.Items) != 1 {
		t.Fatalf("first sweep report wrong: %+v", rep)
	}
	flagged, _ := svc.Get(ctx, doc.ID)
	if flagged.VisEnforcement == nil || !flagged.VisEnforcement.Pending {
		t.Fatalf("flagged doc missing vis_enforcement: %+v", flagged.VisEnforcement)
	}
	if flagged.Visibility != domain.VisCompany {
		t.Errorf("flagging must not change visibility, got %d", flagged.Visibility)
	}
	if d := flagged.VisEnforcement.Deadline; !d.Equal(now.Add(7 * 24 * time.Hour)) {
		t.Errorf("deadline should be now+7d, got %v", d)
	}
	if flagged.VisEnforcement.PreviousVisibility != domain.VisCompany {
		t.Errorf("previous visibility not recorded: %+v", flagged.VisEnforcement)
	}
	// Summary badge for the list page.
	if sum := domain.ToSummary(flagged); !sum.VisPending {
		t.Errorf("summary should carry vis_pending badge: %+v", sum)
	}
	// System action is auditable.
	foundLog := false
	for _, c := range flagged.ChangeLog {
		if c.Field == "vis_enforcement" && c.Source == domain.SourceSystem {
			foundLog = true
		}
	}
	if !foundLog {
		t.Errorf("change_log missing system vis_enforcement entry")
	}

	// Same-day re-sweep is idempotent: the record is pending, not re-flagged.
	rep2, _ := svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if rep2.Flagged != 0 || rep2.Pending != 1 || rep2.Downgraded != 0 {
		t.Fatalf("same-day re-sweep wrong: %+v", rep2)
	}
	vs2, _ := svc.ScanVisibilityViolations(ctx, visPolicy)
	if len(vs2) != 1 || vs2[0].State != supplier.ViolationPending || vs2[0].DaysLeft != 7 {
		t.Fatalf("scan after flag wrong: %+v", vs2)
	}

	// Day 8: buffer expired → auto-downgrade to the policy cap.
	now = now.Add(8 * 24 * time.Hour)
	vs3, _ := svc.ScanVisibilityViolations(ctx, visPolicy)
	if len(vs3) != 1 || vs3[0].State != supplier.ViolationOverdue || vs3[0].DaysLeft != -1 {
		t.Fatalf("overdue scan wrong: %+v", vs3)
	}
	rep3, err := svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if err != nil || rep3.Downgraded != 1 || rep3.Flagged != 0 || len(rep3.Items) != 0 {
		t.Fatalf("timeout sweep wrong: %+v err=%v", rep3, err)
	}
	done, _ := svc.Get(ctx, doc.ID)
	if done.Visibility != domain.VisNamedUsers || done.VisEnforcement != nil {
		t.Errorf("after timeout: visibility=%d enf=%+v, want 1/nil", done.Visibility, done.VisEnforcement)
	}
	sawDowngrade := false
	for _, c := range done.ChangeLog {
		// change_log values round-trip through JSON (memory clone / SQLite
		// JSONB), so numbers come back as float64.
		if c.Field == "visibility" && c.Source == domain.SourceSystem &&
			numOf(c.Old) == float64(domain.VisCompany) && numOf(c.New) == float64(domain.VisNamedUsers) {
			sawDowngrade = true
		}
	}
	if !sawDowngrade {
		t.Errorf("change_log missing system visibility downgrade entry")
	}

	// The compliant supplier was never touched.
	ok, _ := svc.Get(ctx, compliant.ID)
	if ok.VisEnforcement != nil || ok.Visibility != domain.VisSelf {
		t.Errorf("compliant supplier disturbed: %+v", ok.VisEnforcement)
	}
}

func TestVisibilityAppealPausesCountdownGrantIsException(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	svc := newVisService(&now)
	ctx := context.Background()

	doc, _ := svc.Create(ctx, visInput(domain.VisCompany))
	if _, err := svc.EnforceVisibilityPolicy(ctx, visPolicy); err != nil {
		t.Fatalf("Enforce: %v", err)
	}

	// Owner appeals: countdown pauses.
	if _, err := svc.AppealVisibility(ctx, doc.ID, "该供应商资料需全公司共享"); err != nil {
		t.Fatalf("Appeal: %v", err)
	}
	appealed, _ := svc.Get(ctx, doc.ID)
	if !appealed.VisEnforcement.Appealed || appealed.VisEnforcement.AppealNote == "" {
		t.Fatalf("appeal state wrong: %+v", appealed.VisEnforcement)
	}
	vs, _ := svc.ScanVisibilityViolations(ctx, visPolicy)
	if len(vs) != 1 || vs[0].State != supplier.ViolationAppealed {
		t.Fatalf("scanned state should be appealed: %+v", vs)
	}
	// Re-appealing is a no-op.
	if _, err := svc.AppealVisibility(ctx, doc.ID, "again"); err != nil {
		t.Fatalf("idempotent appeal: %v", err)
	}

	// Past the original deadline: an appealed record is NOT downgraded.
	now = now.Add(30 * 24 * time.Hour)
	rep, _ := svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if rep.Appealed != 1 || rep.Downgraded != 0 || rep.Flagged != 0 {
		t.Fatalf("appealed sweep wrong: %+v", rep)
	}
	still, _ := svc.Get(ctx, doc.ID)
	if still.Visibility != domain.VisCompany || still.VisEnforcement == nil {
		t.Fatalf("appealed record must keep its level: vis=%d", still.Visibility)
	}

	// Admin grants the appeal: the level is kept as an approved exception.
	granted, err := svc.ResolveVisibilityAppeal(ctx, doc.ID, true, visPolicy)
	if err != nil {
		t.Fatalf("Resolve grant: %v", err)
	}
	if !granted.VisException || granted.VisEnforcement != nil || granted.Visibility != domain.VisCompany {
		t.Fatalf("grant state wrong: exception=%v enf=%+v vis=%d",
			granted.VisException, granted.VisEnforcement, granted.Visibility)
	}
	// Exception records are skipped by later sweeps and absent from scans.
	rep2, _ := svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if rep2.Flagged != 0 || len(rep2.Items) != 0 {
		t.Fatalf("exception must not be re-flagged: %+v", rep2)
	}

	// But a subsequent visibility edit voids the exception (it applied to
	// the approved level) — the new level re-enters the flow.
	edited, err := svc.Update(ctx, doc.ID, supplier.UpdateInput{
		Visibility: ptr(domain.VisDepartment), // level 3, still above cap
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if edited.VisException {
		t.Errorf("vis_exception must clear when visibility is edited")
	}
	rep3, _ := svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if rep3.Flagged != 1 {
		t.Fatalf("re-edited record should be flagged anew: %+v", rep3)
	}
}

func TestVisibilityAppealDeniedDowngradesImmediately(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	svc := newVisService(&now)
	ctx := context.Background()

	doc, _ := svc.Create(ctx, visInput(domain.VisCompany))
	_, _ = svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if _, err := svc.AppealVisibility(ctx, doc.ID, "please"); err != nil {
		t.Fatalf("Appeal: %v", err)
	}

	// Deny: immediate downgrade to the cap, appeal closed.
	denied, err := svc.ResolveVisibilityAppeal(ctx, doc.ID, false, visPolicy)
	if err != nil {
		t.Fatalf("Resolve deny: %v", err)
	}
	if denied.Visibility != domain.VisNamedUsers || denied.VisEnforcement != nil || denied.VisException {
		t.Fatalf("deny state wrong: vis=%d enf=%+v exception=%v",
			denied.Visibility, denied.VisEnforcement, denied.VisException)
	}
	sawDeny := false
	for _, c := range denied.ChangeLog {
		if c.Field == "vis_appeal" && c.New == "denied_downgraded" {
			sawDeny = true
		}
	}
	if !sawDeny {
		t.Errorf("change_log missing appeal-denied entry")
	}
	vs, _ := svc.ScanVisibilityViolations(ctx, visPolicy)
	if len(vs) != 0 {
		t.Errorf("downgraded record must leave the scan: %+v", vs)
	}

	// Resolving with no open appeal is an error.
	if _, err := svc.ResolveVisibilityAppeal(ctx, doc.ID, true, visPolicy); err == nil {
		t.Errorf("resolving without an appeal must error")
	}
	// Appealing with nothing pending is an error too.
	if _, err := svc.AppealVisibility(ctx, doc.ID, "x"); err == nil {
		t.Errorf("appealing a non-pending record must error")
	}
}

func TestVisibilityOwnerAdjustmentClearsFlag(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	svc := newVisService(&now)
	ctx := context.Background()

	doc, _ := svc.Create(ctx, visInput(domain.VisCompany))
	_, _ = svc.EnforceVisibilityPolicy(ctx, visPolicy)

	// Owner lowers visibility to a compliant level during the buffer.
	if _, err := svc.Update(ctx, doc.ID, supplier.UpdateInput{Visibility: ptr(domain.VisSelf)}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Next sweep notices and clears the flag (resolved, NOT downgraded).
	rep, err := svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if rep.Resolved != 1 || rep.Downgraded != 0 || rep.Flagged != 0 || len(rep.Items) != 0 {
		t.Fatalf("owner-adjustment sweep wrong: %+v", rep)
	}
	done, _ := svc.Get(ctx, doc.ID)
	if done.VisEnforcement != nil || done.Visibility != domain.VisSelf {
		t.Errorf("flag should be cleared after owner fix: enf=%+v vis=%d",
			done.VisEnforcement, done.Visibility)
	}

	// Advance well past the old deadline: nothing to downgrade anymore.
	now = now.Add(30 * 24 * time.Hour)
	rep2, _ := svc.EnforceVisibilityPolicy(ctx, visPolicy)
	if rep2.Downgraded != 0 || rep2.Flagged != 0 {
		t.Fatalf("late sweep must stay quiet: %+v", rep2)
	}
}

func TestVisibilityAppealMootedWhenPolicyLoosened(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	svc := newVisService(&now)
	ctx := context.Background()
	tight := visPolicy // cap 1
	loose := supplier.VisibilityPolicy{MaxLevel: domain.VisCompany, BufferDays: 7}

	doc, _ := svc.Create(ctx, visInput(domain.VisCompany))
	_, _ = svc.EnforceVisibilityPolicy(ctx, tight)
	if _, err := svc.AppealVisibility(ctx, doc.ID, "需要全公司可见"); err != nil {
		t.Fatalf("Appeal: %v", err)
	}

	// Admin loosens the policy while the appeal is open: the record is
	// compliant again, so the open enforcement/appeal closes as moot —
	// NEVER mislabeled as an owner adjustment (the owner did nothing).
	rep, err := svc.EnforceVisibilityPolicy(ctx, loose)
	if err != nil {
		t.Fatalf("Enforce loose: %v", err)
	}
	if rep.Resolved != 1 || rep.Appealed != 0 || len(rep.Items) != 0 {
		t.Fatalf("moot sweep report wrong: %+v", rep)
	}
	done, _ := svc.Get(ctx, doc.ID)
	if done.VisEnforcement != nil || done.Visibility != domain.VisCompany || done.VisException {
		t.Errorf("mooted record state wrong: enf=%+v vis=%d exc=%v",
			done.VisEnforcement, done.Visibility, done.VisException)
	}
	sawMoot := false
	for _, c := range done.ChangeLog {
		if c.Field == "vis_enforcement" && c.Old == "appealed" {
			if c.New != "resolved_appeal_moot_compliant" {
				t.Errorf("closure label = %v, want resolved_appeal_moot_compliant", c.New)
			}
			sawMoot = true
		}
	}
	if !sawMoot {
		t.Error("change_log missing appeal-moot closure entry")
	}
	// Nothing left to resolve once auto-closed.
	if _, err := svc.ResolveVisibilityAppeal(ctx, doc.ID, true, loose); err == nil {
		t.Error("resolving the auto-closed appeal must error")
	}
}

func TestVisibilityAppealDeniedWhileCompliantDoesNotFabricateChange(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	svc := newVisService(&now)
	ctx := context.Background()
	tight := visPolicy
	loose := supplier.VisibilityPolicy{MaxLevel: domain.VisCompany, BufferDays: 7}

	doc, _ := svc.Create(ctx, visInput(domain.VisCompany))
	_, _ = svc.EnforceVisibilityPolicy(ctx, tight)
	_, _ = svc.AppealVisibility(ctx, doc.ID, "x")

	// Admin denies the appeal, but the policy was loosened in the meantime:
	// the record is compliant, so only close the appeal — no 4→4 no-op
	// visibility entry and no "downgraded" label.
	denied, err := svc.ResolveVisibilityAppeal(ctx, doc.ID, false, loose)
	if err != nil {
		t.Fatalf("Resolve deny: %v", err)
	}
	if denied.Visibility != domain.VisCompany || denied.VisEnforcement != nil {
		t.Errorf("compliant record must keep its level: vis=%d enf=%+v",
			denied.Visibility, denied.VisEnforcement)
	}
	var labels []string
	sawClose := false
	for _, c := range denied.ChangeLog {
		labels = append(labels, c.Field+":"+fmt.Sprint(c.Old)+"→"+fmt.Sprint(c.New))
		if c.Field == "visibility" {
			t.Errorf("no visibility change may be logged: %v → %v", c.Old, c.New)
		}
		if c.Field == "vis_appeal" && c.Old == "appealed" {
			sawClose = true
			if c.New != "denied_closed_compliant" {
				t.Errorf("appeal label = %v, want denied_closed_compliant", c.New)
			}
		}
	}
	if !sawClose {
		t.Error("change_log missing the appealed→closed entry")
	}
	t.Logf("change log: %v", labels)
}

// ptr returns a pointer to v (pointer fields distinguish "unchanged" from
// "set to zero").
func ptr[T any](v T) *T { return &v }

// numOf reads a change_log Old/New number, which round-trips through JSON
// storage as float64 (int in-process).
func numOf(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return -1
}
