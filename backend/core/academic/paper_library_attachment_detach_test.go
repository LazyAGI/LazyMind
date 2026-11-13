package academic

import (
	"lazymind/core/common/orm"
	"testing"
)

func TestDetachLibraryAttachmentPreservesPaperOtherAttachmentsNotesAndScope(t *testing.T) {
	db := paperLibraryTestDB(t)
	paper := importDraft(t, "papers", "detach-main", libraryDraft("paper", "Paper", "10.1234/main")).Data.Papers[0]
	importDraft(t, "papers", "detach-neighbor", libraryDraft("neighbor", "Neighbor", "10.1234/neighbor"))
	importDraft(t, "other", "detach-other", libraryDraft("paper", "Other library", "10.1234/other"))
	for _, attachment := range []orm.PaperLibraryAttachment{
		{DatasetID: "papers", ID: "remove", PaperID: "paper", DocumentID: "original", Kind: "pdf", IsPrimary: true},
		{DatasetID: "papers", ID: "keep", PaperID: "paper", DocumentID: "supplement", Kind: "pdf"},
		{DatasetID: "papers", ID: "neighbor", PaperID: "neighbor", DocumentID: "neighbor-original", Kind: "pdf"},
		{DatasetID: "other", ID: "remove", PaperID: "paper", DocumentID: "other-original", Kind: "pdf", IsPrimary: true},
	} {
		if err := db.Create(&attachment).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"original", "supplement", "neighbor-original", "other-original"} {
		if err := db.Create(&orm.Document{ID: id, DatasetID: "papers", DisplayName: id + ".pdf"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	note := orm.PaperLibraryNote{DatasetID: "papers", ID: "note", PaperID: "paper", Content: "Keep source note", SourceJSON: `{"attachmentId":"remove","page":2,"quote":"Evidence"}`}
	if err := db.Create(&note).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, paper, attachment string
		revision                int64
		status                  int
	}{
		{"stranger", "paper", "remove", paper.Revision, 403},
		{"owner", "neighbor", "remove", 1, 404},
		{"owner", "paper", "neighbor", paper.Revision, 404},
		{"owner", "paper", "remove", paper.Revision + 1, 409},
	} {
		w := libraryRequest(t, "POST", "/library/papers/"+tc.paper+"/attachments/"+tc.attachment+":detach", "papers", tc.user, map[string]any{"revision": tc.revision})
		if w.Code != tc.status {
			t.Fatalf("rejected detach %+v: %d %s", tc, w.Code, w.Body)
		}
	}
	for i := 0; i < 2; i++ {
		w := libraryRequest(t, "POST", "/library/papers/paper/attachments/remove:detach", "papers", "owner", map[string]any{"revision": paper.Revision})
		if w.Code != 200 {
			t.Fatalf("detach/replay: %d %s", w.Code, w.Body)
		}
	}
	var removed orm.PaperLibraryAttachment
	if err := db.Where("dataset_id=? AND id=?", "papers", "remove").Take(&removed).Error; err != nil || removed.DetachedAt == nil || removed.IsPrimary {
		t.Fatalf("association not detached: %+v %v", removed, err)
	}
	for _, scope := range [][2]string{{"papers", "keep"}, {"papers", "neighbor"}, {"other", "remove"}} {
		var kept orm.PaperLibraryAttachment
		if err := db.Where("dataset_id=? AND id=?", scope[0], scope[1]).Take(&kept).Error; err != nil || kept.DetachedAt != nil {
			t.Fatalf("unrelated attachment changed: %v %+v %v", scope, kept, err)
		}
	}
	var keptNote orm.PaperLibraryNote
	if err := db.Where("dataset_id=? AND id=?", "papers", "note").Take(&keptNote).Error; err != nil || keptNote.Content != note.Content || keptNote.SourceJSON != note.SourceJSON {
		t.Fatalf("note changed: %+v %v", keptNote, err)
	}
	var count int64
	if err := db.Model(&orm.Document{}).Where("id IN ?", []string{"original", "supplement", "neighbor-original", "other-original"}).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("original documents changed: %d %v", count, err)
	}
	view := readLibrary(t, "papers")
	if view.PaperCount != 2 || view.PDFAttachmentCount != 2 {
		t.Fatalf("library membership changed: papers=%d attachments=%d", view.PaperCount, view.PDFAttachmentCount)
	}
	var entry orm.PaperLibraryEntry
	if err := db.Where("dataset_id=? AND id=?", "papers", "paper").Take(&entry).Error; err != nil || entry.RemovedAt != nil || entry.Revision != paper.Revision+1 {
		t.Fatalf("paper removed or replay mutated revision: %+v %v", entry, err)
	}
}
