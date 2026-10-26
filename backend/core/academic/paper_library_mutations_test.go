package academic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
)

func TestPaperLibraryAttachmentLifecycleIsAtomic(t *testing.T) {
	db := paperLibraryTestDB(t)
	ctx := context.Background()
	graph, _ := paperlibrary.Decode(json.RawMessage(paperLibraryFixture("My confirmed title")))
	paper := paperlibrary.Objects(graph, "papers")[0]
	paper["arxiv"], paper["arxivVersion"] = "2404.06654", "v3"
	raw, _ := json.Marshal(graph)
	if saved, _, err := saveLibraryMutation(ctx, db, "papers", "owner", 0, raw, nil, nil); err != nil || !saved {
		t.Fatalf("save metadata: saved=%v error=%v", saved, err)
	}
	for i, version := range []string{"v3", "v1", "duplicate"} {
		id, key := "pdf-"+version, "upload-"+version
		hash := version + "-hash"
		if version == "duplicate" {
			hash = "v1-hash"
		}
		for _, row := range []any{
			&orm.Document{ID: id, DatasetID: "papers", DisplayName: version + ".pdf", DocumentType: "PDF"},
			&orm.PaperImportBatch{ID: key, EntryType: "pdf_upload", TargetDatasetID: "papers", CreatedBy: "owner", IdempotencyKey: key, Status: "awaiting_association", SourceContextJSON: json.RawMessage(`{}`), SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)},
			&orm.PaperImportItem{ID: key, BatchID: key, DocumentID: id, Stage: "awaiting_association", ContentSHA256: hash, ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`)},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		views, err := paperlibrary.LoadMany(ctx, db, []string{"papers"}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if views["papers"].PDFAttachmentCount != int64(i) {
			t.Fatal("unconfirmed upload entered statistics")
		}
		graph, _ = paperlibrary.Decode(views["papers"].Data)
		paper = paperlibrary.Objects(graph, "papers")[0]
		paper["attachmentIds"] = append(paperlibrary.StringList(paper["attachmentIds"]), id)
		attachments := paperlibrary.Objects(graph, "attachments")
		attachments = append(attachments, paperlibrary.Entity{"id": id, "paperId": "paper", "name": version + ".pdf", "contentRef": id, "kind": "pdf", "source": "local", "addedAt": "", "sourceVersion": version, "isPrimary": true})
		paperlibrary.SetObjects(graph, "attachments", attachments)
		raw, _ = json.Marshal(graph)
		request := &LibraryAttachmentContext{Operation: "attach", PaperID: "paper", DocumentID: id, AttachmentID: id, Confirmed: true, IdempotencyKey: key}
		if saved, _, err := saveLibraryMutation(ctx, db, "papers", "owner", int64(i+1), raw, nil, request); err != nil || !saved {
			t.Fatalf("attach %s: saved=%v error=%v", version, saved, err)
		}
		if saved, replay, err := saveLibraryMutation(ctx, db, "papers", "owner", int64(i+1), raw, nil, request); err != nil || !saved || !replay {
			t.Fatalf("lost response replay: %v %v %v", saved, replay, err)
		}
		var receipt orm.PaperImportItem
		db.Take(&receipt, "id=?", key)
		if receipt.Stage != "done" || receipt.LibraryPaperID != "paper" {
			t.Fatalf("receipt not acknowledged: %+v", receipt)
		}
	}
	view, err := resolvePaperLibrary(ctx, db, "papers", "owner")
	if err != nil {
		t.Fatal(err)
	}
	graph, _ = paperlibrary.Decode(view.Data)
	paper = paperlibrary.Objects(graph, "papers")[0]
	if view.PaperCount != 1 || view.PDFAttachmentCount != 2 || paperlibrary.Text(paper, "title") != "My confirmed title" || paperlibrary.Text(paper, "arxivVersion") != "v3" || paperlibrary.Text(paper, "folderId") != "folder" || len(paperlibrary.Objects(graph, "notes")) != 1 {
		t.Fatalf("adding an old version changed the paper: %+v", paper)
	}
	for _, attachment := range paperlibrary.Objects(graph, "attachments") {
		if paperlibrary.Text(attachment, "id") == "pdf-v1" && attachment["isPrimary"] == true {
			t.Fatal("old version replaced primary PDF")
		}
	}
	paper["attachmentIds"] = []string{"pdf-v3"}
	raw, _ = json.Marshal(graph)
	if saved, _, err := saveLibraryMutation(ctx, db, "papers", "owner", view.Revision, raw, nil, &LibraryAttachmentContext{Operation: "detach", PaperID: "paper", DocumentID: "pdf-v1", AttachmentID: "pdf-v1", Confirmed: true, IdempotencyKey: "detach-v1"}); err != nil || !saved {
		t.Fatalf("detach: %v %v", saved, err)
	}
	view, err = resolvePaperLibrary(ctx, db, "papers", "owner")
	if err != nil || view.PDFAttachmentCount != 1 {
		t.Fatalf("detached file resurrected: %+v %v", view, err)
	}
	var count int64
	db.Model(&orm.Document{}).Where("id=?", "pdf-v1").Count(&count)
	if count != 1 {
		t.Fatal("detaching deleted the original")
	}
}

func TestPaperLibraryRejectsUnconfirmedForeignAndConflictingAttachments(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "foreign", "conflict", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			db := paperLibraryTestDB(t)
			ctx := context.Background()
			graph, _ := paperlibrary.Decode(json.RawMessage(paperLibraryFixture("Confirmed paper")))
			paperlibrary.Objects(graph, "papers")[0]["arxiv"] = "2404.06654"
			raw, _ := json.Marshal(graph)
			if saved, _, err := saveLibraryMutation(ctx, db, "papers", "owner", 0, raw, nil, nil); err != nil || !saved {
				t.Fatal(err)
			}
			library, _ := resolvePaperLibrary(ctx, db, "papers", "owner")
			graph, _ = paperlibrary.Decode(library.Data)
			dataset := "papers"
			if scenario == "foreign" {
				dataset = "other"
			}
			if err := db.Create(&orm.Document{ID: "pdf", DatasetID: dataset, DisplayName: "paper.pdf", DocumentType: "PDF"}).Error; err != nil {
				t.Fatal(err)
			}
			if scenario == "conflict" {
				for _, row := range []any{&orm.AcademicWork{ID: "other-work", CanonicalTitle: "Other paper", ArxivIDBase: "2501.12345", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}, &orm.AcademicDocumentState{DocumentID: "pdf", DatasetID: "papers", PaperID: "other-work", Kind: "paper", Status: "succeeded", MetadataJSON: json.RawMessage(`{}`)}} {
					if err := db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			paperlibrary.Objects(graph, "papers")[0]["attachmentIds"] = []string{"attachment"}
			paperlibrary.SetObjects(graph, "attachments", []paperlibrary.Entity{{"id": "attachment", "paperId": "paper", "name": "paper.pdf", "contentRef": "pdf", "kind": "pdf", "source": "local", "addedAt": ""}})
			raw, _ = json.Marshal(graph)
			request := &LibraryAttachmentContext{Operation: "attach", PaperID: "paper", DocumentID: "pdf", AttachmentID: "attachment", Confirmed: scenario != "unconfirmed", IdempotencyKey: "upload"}
			revision := int64(1)
			if scenario == "stale" {
				revision = 0
			}
			saved, _, err := saveLibraryMutation(ctx, db, "papers", "owner", revision, raw, nil, request)
			if saved || (scenario != "stale" && err == nil) {
				t.Fatalf("invalid association accepted: %v %v", saved, err)
			}
			var link orm.AcademicWorkDocument
			if err := db.Take(&link, "document_id=?", "pdf").Error; !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatal("failed transaction bound the document")
			}
			var stored orm.PaperLibrary
			db.Take(&stored, "dataset_id=?", "papers")
			if stored.Revision != 1 {
				t.Fatal("failed association advanced revision")
			}
		})
	}
}

func TestPaperLibraryMetadataOnlyImportanceIsPersonal(t *testing.T) {
	db := paperLibraryTestDB(t)
	if w := paperLibraryRequest(t, "PUT", "papers", "owner", `{"revision":0,"data":`+paperLibraryFixture("Metadata only")+`}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest("PUT", "/academic/library/papers/paper/importance?target_dataset_id=papers", strings.NewReader(`{"revision":1,"is_important":true}`))
	r.Header.Set("X-User-Id", "owner")
	r = mux.SetURLVars(r, map[string]string{"library_paper_id": "paper"})
	w := httptest.NewRecorder()
	SetLibraryImportance(w, r)
	if w.Code != 200 {
		t.Fatalf("metadata-only star: %d %s", w.Code, w.Body.String())
	}
	owner, err := resolvePaperLibrary(context.Background(), db, "papers", "owner")
	if err != nil {
		t.Fatal(err)
	}
	other, err := resolvePaperLibrary(context.Background(), db, "papers", "other-user")
	if err != nil {
		t.Fatal(err)
	}
	if !owner.PaperImportance["paper"] || other.PaperImportance["paper"] || owner.PDFAttachmentCount != 0 {
		t.Fatal("metadata-only star leaked or created a PDF")
	}
}
