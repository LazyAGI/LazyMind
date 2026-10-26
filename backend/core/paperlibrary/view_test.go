package paperlibrary

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func TestLibraryReadAndStatisticsUseExplicitMembership(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	for _, row := range []any{
		&orm.Dataset{ID: "papers", Scene: "academic_papers", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`)},
		&orm.AcademicWork{ID: "a", CanonicalTitle: "A", AuthorsJSON: json.RawMessage(`["Author A"]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)},
		&orm.PaperLibraryEntry{DatasetID: "papers", ID: "entry", AcademicWorkID: "a", Status: "unread", TagsJSON: json.RawMessage(`[]`), MetadataJSON: json.RawMessage(`{"title":"Confirmed title","authors":["Author A","Author B"]}`), Revision: 1},
		&orm.Document{ID: "pdf", DatasetID: "papers", DisplayName: "paper.pdf"},
		&orm.Document{ID: "unconfirmed", DatasetID: "papers", DisplayName: "pending.pdf"},
		&orm.PaperLibraryAttachment{DatasetID: "papers", ID: "attachment", PaperID: "entry", DocumentID: "pdf", Kind: "pdf", Name: "paper.pdf", CreatedAt: time.Now()},
		&orm.AcademicWorkDocument{AcademicWorkID: "a", DatasetID: "papers", DocumentID: "pdf", SourceProvider: "arxiv", SourceVersion: "v2", ContentSHA256: "hash"},
	} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
	view, e := Load(t.Context(), db.DB, "papers", "", nil, true)
	if e != nil {
		t.Fatal(e)
	}
	counts, e := Statistics(t.Context(), db.DB, []string{"papers"})
	if e != nil {
		t.Fatal(e)
	}
	if len(view.Data.Papers) != 1 || view.Data.Papers[0].Metadata.Title != "Confirmed title" || len(view.Data.Papers[0].Metadata.Authors) != 2 || counts["papers"].PaperCount != 1 || counts["papers"].PDFAttachmentCount != 1 {
		t.Fatal("membership or explicit metadata was overwritten", view, counts)
	}
	if view.Data.Attachments[0].SourceVersion != "v2" || view.Data.Attachments[0].ContentHash != "hash" {
		t.Fatal("file facts were not projected")
	}
	now := time.Now().UTC()
	if e = db.Model(&orm.PaperLibraryAttachment{}).Where("dataset_id='papers'").Update("detached_at", now).Error; e != nil {
		t.Fatal(e)
	}
	view, e = Load(t.Context(), db.DB, "papers", "", nil, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(view.Data.Papers[0].AttachmentIDs) != 0 || len(view.Data.Attachments) != 0 {
		t.Fatal("detached raw file reappeared")
	}
}
