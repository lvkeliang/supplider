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

// After a merge the duplicate's attachment is reference-unioned onto the
// master (bytes stay keyed under the duplicate's id). The object must not
// be deleted while either document references it — restoring the duplicate
// and removing the file there used to leave the master with a dangling URL.
func TestAttachmentReferenceCountedAcrossMerge(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	master, err := svc.Create(ctx, namedInput("引用计数主体公司", "91330100MA27Z2001A", "杭州"))
	if err != nil {
		t.Fatal(err)
	}
	dup, err := svc.Create(ctx, namedInput("引用计数重复公司", "91330100MA27Z2002B", "杭州"))
	if err != nil {
		t.Fatal(err)
	}
	// Keyed under the duplicate's id, as a real upload would be.
	attURL := "/api/v1/attachments/" + dup.ID + "/license.jpg"
	if _, err := svc.AddAttachment(ctx, dup.ID, domain.Attachment{Name: "license.jpg", URL: attURL, Size: 9}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.MergeSuppliers(ctx, master.ID, dup.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// Both the master (merged ref) and the archived duplicate reference it.
	if ok, _ := svc.AttachmentReferenced(ctx, attURL); !ok {
		t.Fatal("attachment should be referenced right after merge")
	}

	// Restore the (now archived) duplicate and remove the attachment there.
	restored, err := svc.Restore(ctx, dup.ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, _, err := svc.RemoveAttachment(ctx, restored.ID, attURL); err != nil {
		t.Fatalf("remove from restored dup: %v", err)
	}
	// Master still holds the merged reference — bytes must NOT be GC'd.
	ok, err := svc.AttachmentReferenced(ctx, attURL)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("attachment must remain referenced by the master after the dup drops it")
	}
	masterDoc, _ := svc.Get(ctx, master.ID)
	if !hasAttachmentURL(masterDoc, attURL) {
		t.Fatal("master lost its merged attachment reference")
	}

	// Remove from the master too — now it is unreferenced and deletable.
	if _, _, err := svc.RemoveAttachment(ctx, master.ID, attURL); err != nil {
		t.Fatalf("remove from master: %v", err)
	}
	ok, _ = svc.AttachmentReferenced(ctx, attURL)
	if ok {
		t.Fatal("attachment should be unreferenced after both documents drop it")
	}
}

// A plain single-document attachment is unreferenced immediately after its
// record is removed (the normal GC case).
func TestAttachmentReferencedFalseAfterSingleRemove(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()
	doc, err := svc.Create(ctx, namedInput("单个附件公司", "91330100MA27Z3001C", "杭州"))
	if err != nil {
		t.Fatal(err)
	}
	att := domain.Attachment{Name: "a.pdf", URL: "/api/v1/attachments/" + doc.ID + "/a.pdf", Size: 1}
	if _, err := svc.AddAttachment(ctx, doc.ID, att); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.RemoveAttachment(ctx, doc.ID, att.URL); err != nil {
		t.Fatal(err)
	}
	ok, _ := svc.AttachmentReferenced(ctx, att.URL)
	if ok {
		t.Fatal("single-doc attachment should be unreferenced after removal")
	}
	if ok, _ := svc.AttachmentReferenced(ctx, "  "); ok {
		t.Fatal("blank URL should report unreferenced")
	}
}

func hasAttachmentURL(d *domain.Supplier, url string) bool {
	for _, a := range d.Attachments {
		if a.URL == url {
			return true
		}
	}
	return false
}
