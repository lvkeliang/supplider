package sqlite_test

// End-to-end visibility disposition lifecycle on REAL SQLite storage: the
// vis_enforcement sub-document (deadline, appeal flags) must survive JSONB
// round-trips and close/reopen, and the timed auto-downgrade must persist
// with its change_log. The state-machine semantics themselves are pinned in
// internal/supplier/visibility_test.go; here we prove nothing is lost
// through the storage adapter.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestVisibilityLifecyclePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supplider.db")
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	policy := supplier.VisibilityPolicy{MaxLevel: domain.VisNamedUsers, BufferDays: 7}

	openSvc := func() (*supplier.Service, func()) {
		t.Helper()
		st, err := sqlite.Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		svc := supplier.NewService(st).WithClock(func() time.Time { return now })
		return svc, func() { _ = st.Close() }
	}

	// Day 0: create a level-4 supplier and flag it under a level-1 cap.
	svc, closeSvc := openSvc()
	doc, err := svc.Create(ctx, supplier.CreateInput{
		Owner: "u1",
		BasicInfo: domain.BasicInfo{
			CompanyName: "杭州可见性持久化测试有限公司",
			Region:      domain.Region{Province: "浙江", City: "杭州"},
		},
		Visibility: domain.VisCompany,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rep, err := svc.EnforceVisibilityPolicy(ctx, policy)
	if err != nil || rep.Flagged != 1 {
		t.Fatalf("first enforce: %+v err=%v", rep, err)
	}
	deadline := now.Add(7 * 24 * time.Hour)
	flagged, _ := svc.Get(ctx, doc.ID)
	if flagged.VisEnforcement == nil || !flagged.VisEnforcement.Pending ||
		!flagged.VisEnforcement.Deadline.Equal(deadline) {
		t.Fatalf("flag state wrong: %+v", flagged.VisEnforcement)
	}
	// An open appeal must also survive the round-trip (paused countdown).
	if _, err := svc.AppealVisibility(ctx, doc.ID, "需要全公司可见"); err != nil {
		t.Fatalf("appeal: %v", err)
	}
	closeSvc()

	// Reopen on day 30 (past the deadline): an appealed record stays put.
	now = now.Add(30 * 24 * time.Hour)
	svc, closeSvc = openSvc()
	defer closeSvc()
	reopened, err := svc.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("reopen get: %v", err)
	}
	enf := reopened.VisEnforcement
	if enf == nil || !enf.Pending || !enf.Appealed || enf.AppealNote != "需要全公司可见" {
		t.Fatalf("appeal state lost across reopen: %+v", enf)
	}
	if !enf.Deadline.Equal(deadline) {
		t.Errorf("deadline drifted across reopen: %v want %v", enf.Deadline, deadline)
	}
	rep2, err := svc.EnforceVisibilityPolicy(ctx, policy)
	if err != nil || rep2.Appealed != 1 || rep2.Downgraded != 0 {
		t.Fatalf("appealed record must be untouched: %+v err=%v", rep2, err)
	}

	// Admin denies the appeal: immediate downgrade persists across another
	// reopen, audited in change_log (numbers round-trip as float64).
	if _, err := svc.ResolveVisibilityAppeal(ctx, doc.ID, false, policy); err != nil {
		t.Fatalf("deny: %v", err)
	}
	closeSvc()
	svc, closeSvc = openSvc()
	defer closeSvc()
	done, err := svc.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("final reopen get: %v", err)
	}
	if done.Visibility != domain.VisNamedUsers || done.VisEnforcement != nil {
		t.Fatalf("after deny + reopen: vis=%d enf=%+v, want 1/nil",
			done.Visibility, done.VisEnforcement)
	}
	sawDowngrade := false
	for _, c := range done.ChangeLog {
		if c.Field == "visibility" && c.Source == domain.SourceManual &&
			numOf(c.Old) == float64(domain.VisCompany) &&
			numOf(c.New) == float64(domain.VisNamedUsers) {
			sawDowngrade = true
		}
	}
	if !sawDowngrade {
		t.Error("manual downgrade change_log entry lost across reopen")
	}
	if domain.ToSummary(done).VisPending {
		t.Error("summary badge must clear after enforcement ends")
	}
}

// numOf reads a change_log number (JSONB round-trips ints as float64).
func numOf(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return -1
}
