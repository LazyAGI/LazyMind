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
		&orm.PaperLibraryEntry{DatasetID: "papers", ID: "entry", AcademicWorkID: "a", TagsJSON: json.RawMessage(`[]`), MetadataJSON: json.RawMessage(`{"title":"Confirmed title","authors":["Author A","Author B"]}`), Revision: 1},
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

func TestAttachmentImportUsesItsOwnReceiptAndLeavesMissingOriginsUnknown(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	for _, row := range []any{
		&orm.AcademicWork{ID: "work", CanonicalTitle: "Paper", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)},
		&orm.PaperLibraryEntry{DatasetID: "papers", ID: "paper", AcademicWorkID: "work", ImportMethod: "bibliography", Source: "local", SourceURL: "https://publisher.example/record", MetadataJSON: json.RawMessage(`{}`), TagsJSON: json.RawMessage(`[]`), Revision: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ entry, method string }{{"pdf_upload", "upload"}, {"conversation_sources", "conversation"}, {"future_type", ""}, {"", ""}} {
		id := "attachment-" + test.entry
		attachment := orm.PaperLibraryAttachment{ID: id, DatasetID: "papers", PaperID: "paper", Name: id, Kind: "supplement"}
		if test.entry != "" {
			batch := orm.PaperImportBatch{ID: id, TargetDatasetID: "papers", EntryType: test.entry, SourceContextJSON: json.RawMessage(`{}`), SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)}
			item := orm.PaperImportItem{ID: id, BatchID: id, ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`)}
			for _, row := range []any{&batch, &item} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			attachment.ImportItemID = id
		}
		if err := db.Create(&attachment).Error; err != nil {
			t.Fatal(err)
		}
		view, err := Load(t.Context(), db.DB, "papers", "", nil, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, got := range view.Data.Attachments {
			if got.ID == id && got.ImportMethod != test.method {
				t.Fatalf("attachment borrowed paper provenance: %+v", got)
			}
		}
		if view.Data.Papers[0].ImportMethod != "bibliography" || view.Data.Papers[0].SourceURL != "https://publisher.example/record" {
			t.Fatal("attachment replaced the first paper import", view.Data.Papers[0])
		}
	}
}
