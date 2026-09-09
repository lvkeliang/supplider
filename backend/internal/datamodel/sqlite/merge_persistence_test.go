package sqlite_test

// Merge (合并重复供应商) on REAL SQLite storage: the absorbed score-only
// performance records, enriched qualification and reference-unioned
// attachment URL must survive JSONB round-trips, and the archived duplicate
// stays hidden from the default list while remaining retrievable. Merge
// semantics themselves are pinned in internal/supplier/merge_test.go.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/datamodel/sqlite"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestMergePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supplider.db")
	ctx := context.Background()

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	svc := supplier.NewService(st)

	masterIn := supplier.CreateInput{
		Owner: "u1",
		BasicInfo: domain.BasicInfo{
			CompanyName: "合并持久化主体有限公司",
			Region:      domain.Region{Province: "浙江", City: "杭州"},
		},
		Qualifications: []domain.Qualification{{Type: "建筑工程施工总承包", Level: "一级"}},
		Performance:    []domain.Performance{{Score: 4.0}}, // score-only
	}
	master, err := svc.Create(ctx, masterIn)
	if err != nil {
		t.Fatalf("create master: %v", err)
	}
	dupIn := supplier.CreateInput{
		Owner: "u1",
		BasicInfo: domain.BasicInfo{
			CompanyName: "合并持久化主体公司",
			Region:      domain.Region{Province: "浙江", City: "杭州"},
		},
		Performance: []domain.Performance{{Score: 2.0, Feedback: "配合度差"}},
	}
	dup, err := svc.Create(ctx, dupIn)
	if err != nil {
		t.Fatalf("create duplicate: %v", err)
	}
	dup, err = svc.AddAttachment(ctx, dup.ID, domain.Attachment{
		Name: "营业执照.jpg", URL: "/api/v1/attachments/" + dup.ID + "/license.jpg", Size: 12,
	})
	if err != nil {
		t.Fatalf("add attachment: %v", err)
	}
	// The numbered cert enriches the master's unnumbered same type+level.
	dup, err = svc.Update(ctx, dup.ID, supplier.UpdateInput{
		Qualifications: &[]domain.Qualification{{
			Type: "建筑工程施工总承包", Level: "一级", CertNo: "D133012345", Expiry: "2027-06-30",
		}},
	})
	if err != nil {
		t.Fatalf("update dup quals: %v", err)
	}

	_, res, err := svc.MergeSuppliers(ctx, master.ID, dup.ID)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.PerformanceAdded != 1 || res.AttachmentsAdded != 1 || res.QualsAdded != 0 {
		t.Errorf("merge counts wrong: %+v", res)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen: every absorbed datum must have persisted.
	st2, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	svc2 := supplier.NewService(st2)

	got, err := svc2.Get(ctx, master.ID)
	if err != nil {
		t.Fatalf("get master after reopen: %v", err)
	}
	if len(got.PerformanceHistory) != 2 {
		t.Errorf("score-only performance lost across reopen: %+v", got.PerformanceHistory)
	}
	if len(got.Attachments) != 1 || !strings.HasPrefix(got.Attachments[0].URL, "/api/v1/attachments/"+dup.ID+"/") {
		t.Errorf("absorbed attachment URL lost: %+v", got.Attachments)
	}
	if len(got.Qualifications) != 1 || got.Qualifications[0].CertNo != "D133012345" ||
		got.Qualifications[0].Expiry != "2027-06-30" {
		t.Errorf("qual enrichment lost across reopen: %+v", got.Qualifications)
	}

	// Duplicate stays archived after reopen and is hidden by default.
	archived, err := svc2.Get(ctx, dup.ID)
	if err != nil || archived.Status != domain.StatusArchived {
		t.Fatalf("duplicate should remain archived: status=%s err=%v", archived.Status, err)
	}
	page, err := svc2.List(ctx, datamodel.Query{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != master.ID {
		t.Errorf("default list = %d rows, want only the master", len(page.Items))
	}
}
