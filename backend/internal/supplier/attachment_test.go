package supplier_test

// Attachment lifecycle: AddAttachment appends a record; RemoveAttachment
// detaches exactly the matching record (by URL), writes a change_log entry
// and refuses to edit archived suppliers.

import (
	"context"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

func TestRemoveAttachment(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	doc, err := svc.Create(ctx, namedInput("附件删除测试公司", "91330100MA27Y1001A", "杭州"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	att1 := domain.Attachment{Name: "营业执照.jpg", URL: "/api/v1/attachments/" + doc.ID + "/1_a.jpg", Size: 100}
	att2 := domain.Attachment{Name: "资质.pdf", URL: "/api/v1/attachments/" + doc.ID + "/2_b.pdf", Size: 200}
	if _, err := svc.AddAttachment(ctx, doc.ID, att1); err != nil {
		t.Fatalf("add att1: %v", err)
	}
	doc, err = svc.AddAttachment(ctx, doc.ID, att2)
	if err != nil {
		t.Fatalf("add att2: %v", err)
	}
	if len(doc.Attachments) != 2 {
		t.Fatalf("want 2 attachments, got %d", len(doc.Attachments))
	}

	got, removed, err := svc.RemoveAttachment(ctx, doc.ID, att1.URL)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed == nil || removed.Name != "营业执照.jpg" {
		t.Errorf("removed record wrong: %+v", removed)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].URL != att2.URL {
		t.Errorf("only att1 should be removed: %+v", got.Attachments)
	}
	// change_log records the removal (Old = name, no New).
	sawRemoval := false
	for _, c := range got.ChangeLog {
		if c.Field == "attachments" && c.Old == "营业执照.jpg" && c.New == nil {
			sawRemoval = true
		}
	}
	if !sawRemoval {
		t.Errorf("change_log missing attachment-removal entry")
	}

	// Removing a non-existent URL errors.
	if _, _, err := svc.RemoveAttachment(ctx, doc.ID, att1.URL); err == nil {
		t.Errorf("re-removing the same attachment must error")
	}
	// Empty URL errors.
	if _, _, err := svc.RemoveAttachment(ctx, doc.ID, "  "); err == nil {
		t.Errorf("empty url must error")
	}
}

func TestRemoveAttachmentArchivedRejected(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	doc, err := svc.Create(ctx, namedInput("归档附件公司", "91330100MA27Y1002B", "杭州"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	att := domain.Attachment{Name: "x.pdf", URL: "/api/v1/attachments/" + doc.ID + "/1_x.pdf"}
	doc, _ = svc.AddAttachment(ctx, doc.ID, att)
	if err := svc.Archive(ctx, doc.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	_, _, err = svc.RemoveAttachment(ctx, doc.ID, att.URL)
	if err == nil || !strings.Contains(err.Error(), "archived") {
		t.Errorf("removing attachment on archived supplier must be rejected, got %v", err)
	}
}
