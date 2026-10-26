package paperlibrary

import (
	"context"
	"encoding/json"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func viewTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := orm.OpenTestDB(t)
	if err := db.AutoMigrate(&orm.PaperLibrary{}, &orm.Document{}, &orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.AcademicDocumentState{}, &orm.AcademicUserPaperState{}, &orm.DocumentProcessingState{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}); err != nil {
		t.Fatal(err)
	}
	return db.DB
}

func TestResolvedLibraryPreservesPaperAcrossVersionsAndPersonalMarks(t *testing.T) {
	db := viewTestDB(t)
	graph := Empty()
	SetObjects(graph, "folders", []Entity{{"id": "document-folder-child", "name": "Topic/Nested"}})
	paper := Entity{"id": "paper", "academicWorkId": "work", "title": "Edited title", "authors": "Original author", "year": 2026, "venue": "My venue", "folderId": "document-folder-child", "status": "reading", "important": true, "tags": []any{"my-tag"}, "metadata": Entity{"identifiers": Entity{}, "confidence": Entity{}}, "attachmentIds": []any{"primary"}, "noteIds": []any{"note"}, "annotationIds": []any{}, "collectionIds": []any{}, "relatedPaperIds": []any{}}
	SetObjects(graph, "papers", []Entity{paper})
	SetObjects(graph, "attachments", []Entity{{"id": "primary", "paperId": "paper", "name": "v3.pdf", "kind": "pdf", "source": "arxiv", "addedAt": "", "contentRef": "v3", "isPrimary": true, "sourceVersion": "v3"}})
	SetObjects(graph, "notes", []Entity{{"id": "note", "paperId": "paper", "content": "Keep my note"}})
	encoded, _ := json.Marshal(graph)
	for _, row := range []any{
		&orm.PaperLibrary{DatasetID: "papers", Revision: 1, DataJSON: encoded},
		&orm.AcademicWork{ID: "work", CanonicalTitle: "Automatic title", AuthorsJSON: json.RawMessage(`["Author"]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`), ArxivIDBase: "2404.06654"},
		&orm.Document{ID: "parent", DatasetID: "papers", DisplayName: "Topic", DocumentType: "FOLDER"},
		&orm.Document{ID: "child", DatasetID: "papers", DisplayName: "Nested", DocumentType: "FOLDER", PID: "parent"},
		&orm.Document{ID: "empty", DatasetID: "papers", DisplayName: "Empty real folder", DocumentType: "FOLDER", PID: "parent"},
		&orm.Document{ID: "v3", DatasetID: "papers", DisplayName: "v3.pdf", DocumentType: "PDF", PID: "child"},
		&orm.Document{ID: "v1", DatasetID: "papers", DisplayName: "v1.pdf", DocumentType: "PDF"},
		&orm.AcademicDocumentState{DocumentID: "v3", DatasetID: "papers", PaperID: "work", Kind: "paper", Status: "succeeded", MetadataJSON: json.RawMessage(`{}`)},
		&orm.AcademicDocumentState{DocumentID: "v1", DatasetID: "papers", PaperID: "work", Kind: "paper", Status: "succeeded", MetadataJSON: json.RawMessage(`{"title":"Old version title"}`)},
		&orm.AcademicWorkDocument{AcademicWorkID: "work", DatasetID: "papers", DocumentID: "v3", SourceProvider: "arxiv", SourceVersion: "v3", ContentSHA256: "v3-hash"},
		&orm.AcademicWorkDocument{AcademicWorkID: "work", DatasetID: "papers", DocumentID: "v1", SourceProvider: "arxiv", SourceVersion: "v1", ContentSHA256: "v1-hash"},
		&orm.AcademicUserPaperState{UserID: "alice", PaperID: "work", IsImportant: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	views, err := LoadMany(context.Background(), db, []string{"papers"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	view := views["papers"]
	if view.PaperCount != 1 || view.PDFAttachmentCount != 2 || !view.PaperImportance["paper"] {
		t.Fatalf("incorrect paper/file totals or mark: %+v", view)
	}
	resolved, _ := Decode(view.Data)
	got := Objects(resolved, "papers")[0]
	if Text(got, "title") != "Edited title" || Text(got, "folderId") != "document-folder-child" || Text(got, "status") != "reading" || len(StringList(got["noteIds"])) != 1 {
		t.Fatalf("new PDF replaced editable paper: %+v", got)
	}
	for _, attachment := range Objects(resolved, "attachments") {
		if Text(attachment, "contentRef") == "v1" && attachment["isPrimary"] == true {
			t.Fatal("older PDF replaced the selected primary")
		}
	}
	folders := Objects(resolved, "folders")
	if len(folders) != 3 {
		t.Fatalf("lost empty or ancestor folders: %+v", folders)
	}
	for _, folder := range folders {
		if Text(folder, "id") == "document-folder-child" && Text(folder, "parentId") != "document-folder-parent" {
			t.Fatal("lost folder hierarchy")
		}
	}
	other, err := LoadMany(context.Background(), db, []string{"papers"}, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if other["papers"].PaperImportance["paper"] {
		t.Fatal("another user's importance leaked")
	}
	var stored orm.PaperLibrary
	db.Take(&stored, "dataset_id=?", "papers")
	if string(stored.DataJSON) != string(encoded) {
		t.Fatal("reading the resolved view changed the saved graph")
	}
}

func TestPendingUploadsAndRemovedOriginalsStayOutOfThePaperView(t *testing.T) {
	db := viewTestDB(t)
	for _, row := range []any{
		&orm.Document{ID: "upload", DatasetID: "papers", DisplayName: "paper.pdf", DocumentType: "PDF"},
		&orm.PaperImportBatch{ID: "batch", EntryType: "pdf_upload", TargetDatasetID: "papers", CreatedBy: "alice", SourceContextJSON: json.RawMessage(`{}`), SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)},
		&orm.PaperImportItem{ID: "item", BatchID: "batch", DocumentID: "upload", Stage: "awaiting_association", ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`)},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	views, err := LoadMany(context.Background(), db, []string{"papers"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if views["papers"].PaperCount != 0 || views["papers"].PDFAttachmentCount != 0 {
		t.Fatal("unconfirmed upload appeared as a paper")
	}
	graph := Empty()
	graph["hiddenDocumentIds"] = []string{"upload"}
	raw, _ := json.Marshal(graph)
	if err := db.Create(&orm.PaperLibrary{DatasetID: "papers", DataJSON: raw, Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.PaperImportItem{}).Where("id=?", "item").Update("stage", "done")
	views, err = LoadMany(context.Background(), db, []string{"papers"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if views["papers"].PaperCount != 0 {
		t.Fatal("removed original was resurrected on refresh")
	}
}

func TestFolderValidationRejectsDanglingAndCyclicParents(t *testing.T) {
	graph := Empty()
	SetObjects(graph, "folders", []Entity{{"id": "a", "name": "A", "parentId": "b"}, {"id": "b", "name": "B", "parentId": "a"}})
	if err := ValidateRelations(graph); err == nil {
		t.Fatal("accepted a directory cycle")
	}
	SetObjects(graph, "folders", []Entity{{"id": "a", "name": "A", "parentId": "missing"}})
	if err := ValidateRelations(graph); err == nil {
		t.Fatal("accepted a missing parent")
	}
}

func TestSupplementAndExternalLinkAreNotStoredPaperPDFs(t *testing.T) {
	db := viewTestDB(t)
	graph := Empty()
	SetObjects(graph, "papers", []Entity{{"id": "paper", "title": "Metadata and material", "attachmentIds": []any{"material", "link"}}})
	SetObjects(graph, "attachments", []Entity{
		{"id": "material", "paperId": "paper", "name": "supplement.pdf", "kind": "supplement", "mimeType": "application/pdf", "contentRef": "original"},
		{"id": "link", "paperId": "paper", "kind": "pdf", "sourceUrl": "https://arxiv.org/pdf/2404.06654"},
	})
	raw, _ := json.Marshal(graph)
	for _, row := range []any{&orm.PaperLibrary{DatasetID: "papers", DataJSON: raw}, &orm.Document{ID: "original", DatasetID: "papers", DisplayName: "supplement.pdf", DocumentType: "PDF"}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	views, err := LoadMany(context.Background(), db, []string{"papers"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if views["papers"].PaperCount != 1 || views["papers"].PDFAttachmentCount != 0 {
		t.Fatalf("material/link counted as full-text PDF: %+v", views["papers"])
	}
}
