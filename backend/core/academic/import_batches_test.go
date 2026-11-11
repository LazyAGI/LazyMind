package academic

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestImportOperationViewsPageInDatabaseAndKeepDefaultContract(t *testing.T) {
	db := paperLibraryTestDB(t)
	now := time.Now().UTC()
	work := orm.AcademicWork{ID: "work", CanonicalTitle: "Paper", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]any{})}
	if e := db.Create(&work).Error; e != nil {
		t.Fatal(e)
	}
	for i, status := range []string{"succeeded", "failed", "pending"} {
		id := []string{"done", "failed", "active"}[i]
		batch := orm.PaperImportBatch{ID: id, EntryType: "paper_link", TargetDatasetID: "papers", CreatedBy: "owner", Status: status, SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{}), CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if e := db.Create(&batch).Error; e != nil {
			t.Fatal(e)
		}
		for j := 0; j < 5; j++ {
			item := orm.PaperImportItem{ID: id + string(rune('a'+j)), BatchID: id, AcademicWorkID: "work", TitleSnapshot: "Paper", Status: status, Stage: "done", ResultKind: "metadata_only", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{}), CreatedAt: now.Add(time.Duration(j) * time.Millisecond)}
			if status != "succeeded" {
				item.ResultKind = ""
				item.Stage = "acquire"
			}
			if e := db.Create(&item).Error; e != nil {
				t.Fatal(e)
			}
		}
	}
	w := libraryRequest(t, "GET", "/imports?view=operations&limit=1", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page struct {
		Items        []ImportOperation `json:"items"`
		Next         string            `json:"next_cursor"`
		Notification struct {
			Active      int `json:"active"`
			NeedsAction int `json:"needs_action"`
		} `json:"notification"`
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Summary.Total != 5 || page.Next == "" || page.Notification.Active != 1 || page.Notification.NeedsAction != 1 {
		t.Fatalf("incomplete operations or totals: %+v", page)
	}
	w = libraryRequest(t, "GET", "/imports/done?view=operations&limit=2", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var detail struct {
		Operation ImportOperation       `json:"operation"`
		Items     []ImportOperationItem `json:"items"`
		Next      string                `json:"next_cursor"`
	}
	json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Items) != 2 || detail.Operation.Summary.Total != 5 || detail.Next == "" {
		t.Fatalf("detail paging truncated summary: %+v", detail)
	}
	w = libraryRequest(t, "GET", "/imports/done", "papers", "owner", nil)
	var original struct {
		Batch orm.PaperImportBatch  `json:"batch"`
		Items []orm.PaperImportItem `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &original)
	if w.Code != 200 || original.Batch.ID != "done" || len(original.Items) != 5 {
		t.Fatal("main batch status contract changed", w.Body.String())
	}
}

func TestImportOperationsFollowLibraryRemovalAndReimport(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *gorm.DB
			if driver == "postgres" {
				db = paperLibraryTestDB(t, libraryPostgresDB(t))
			} else {
				db = paperLibraryTestDB(t)
			}
			draft := libraryDraft("paper", "Paper", "10.1234/removed")
			paper := importDraft(t, "papers", "metadata", draft).Data.Papers[0]
			db.Model(&orm.PaperImportBatch{}).Where("idempotency_key=?", "metadata").Updates(map[string]any{"status": "succeeded", "completed_items": 1})
			db.Model(&orm.PaperImportItem{}).Where("library_paper_id=?", paper.ID).Updates(map[string]any{"status": "succeeded", "stage": "done", "result_kind": "metadata_only"})
			uploadReceipt(t, db, "papers", "pdf", "upload", paper.ID, "hash")
			path := filepath.Join(t.TempDir(), "paper.pdf")
			if err := os.WriteFile(path, []byte("%PDF fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.Document{}).Where("id='pdf'").Update("ext", marshal(map[string]string{"stored_path": path})).Error; err != nil {
				t.Fatal(err)
			}
			confirmUpload(t, paper, "pdf", "upload", true)
			importDraft(t, "papers", "duplicate", draft)
			var receipts []orm.PaperImportItem
			if err := db.Find(&receipts).Error; err != nil {
				t.Fatal(err)
			}
			originalStatus := map[string]string{}
			originalBatches := map[string]bool{}
			for _, item := range receipts {
				originalStatus[item.ID] = item.Status
				originalBatches[item.BatchID] = true
			}
			check := func(removed bool, operationCount, active int) {
				t.Helper()
				view := readLibrary(t, "papers")
				wantCount := int64(1)
				if removed {
					wantCount = 0
				}
				if view.PaperCount != wantCount || view.PDFAttachmentCount != wantCount {
					t.Fatalf("library membership disagrees: %+v", view.Counts)
				}
				w := libraryRequest(t, "GET", "/imports?view=operations", "papers", "owner", nil)
				var page struct {
					Items        []ImportOperation `json:"items"`
					Notification struct {
						Active      int `json:"active"`
						NeedsAction int `json:"needs_action"`
					} `json:"notification"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Items) != operationCount || page.Notification.Active != active || page.Notification.NeedsAction != 0 {
					t.Fatal(w.Code, w.Body.String())
				}
				for _, operation := range page.Items {
					if !originalBatches[operation.ID] {
						continue
					}
					w = libraryRequest(t, "GET", "/imports/"+operation.ID+"?view=operations", "papers", "owner", nil)
					var detail struct {
						Batch     orm.PaperImportBatch  `json:"batch"`
						Operation ImportOperation       `json:"operation"`
						Items     []ImportOperationItem `json:"items"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || len(detail.Items) != 1 {
						t.Fatal(w.Code, w.Body.String())
					}
					if detail.Operation.Summary != operation.Summary || detail.Operation.Status != operation.Status {
						t.Fatalf("list and detail disagree: %+v %+v", operation, detail.Operation)
					}
					item := detail.Items[0]
					if item.Status != originalStatus[item.ItemID] || detail.Batch.Status != "succeeded" {
						t.Fatalf("historical import result changed: %+v", detail)
					}
					if item.DocumentID != "" && (item.CurrentDocument == nil || item.CurrentDocument.DocumentID != "pdf" || item.DocumentDeleted) {
						t.Fatalf("original PDF is no longer readable: %+v", item)
					}
					if removed {
						if operation.Status != "removed" || operation.Summary.Removed != 1 || operation.Summary.Ready+operation.Summary.MetadataOnly+operation.Summary.Skipped != 0 || item.CurrentState != "removed" || item.LibraryPaperID != "" || item.ResolvedLater || item.CanRetry {
							t.Fatalf("removed paper still appears in the library: %+v %+v", operation, item)
						}
					} else if operation.Summary.Removed != 0 || item.CurrentState == "removed" || item.LibraryPaperID != paper.ID {
						t.Fatalf("active membership was lost: %+v %+v", operation, item)
					}
				}
			}
			check(false, 3, 0)
			paper = readLibrary(t, "papers").Data.Papers[0]
			w := libraryRequest(t, "POST", "/library/papers:remove", "papers", "owner", map[string]any{"items": []libraryTarget{{ID: paper.ID, Revision: paper.Revision}}})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			check(true, 3, 0)
			importDraft(t, "papers", "reimport", draft)
			check(false, 4, 1)
		})
	}
}

func TestLibraryAndImportViewsObserveCurrentProcessingTask(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
			var db *gorm.DB
			if driver == "postgres" {
				db = paperLibraryTestDB(t, libraryPostgresDB(t))
			} else {
				db = paperLibraryTestDB(t)
			}
			if err := db.AutoMigrate(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := db.Model(&orm.Dataset{}).Where("id='papers'").Updates(map[string]any{"kb_id": "remote-papers", "processing_level": "indexed", "processing_revision": 1}).Error; err != nil {
				t.Fatal(err)
			}
			paper := importDraft(t, "papers", "bibliography", libraryDraft("paper", "Paper", "10.1234/current")).Data.Papers[0]
			// This test isolates document processing; finish the new background enrichment.
			db.Model(&orm.PaperImportItem{}).Where("library_paper_id=?", paper.ID).Updates(map[string]any{"status": "succeeded", "stage": "done", "result_kind": "metadata_only"})
			path := filepath.Join(t.TempDir(), "paper.pdf")
			if err := os.WriteFile(path, []byte("%PDF fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			document := orm.Document{ID: "pdf", DatasetID: "papers", LazyllmDocID: "remote-pdf", DisplayName: "paper.pdf", Ext: marshal(map[string]string{"stored_path": path})}
			for _, row := range []any{
				&document,
				&orm.PaperLibraryAttachment{DatasetID: "papers", PaperID: paper.ID, ID: "attachment", DocumentID: "pdf", Name: "paper.pdf", Kind: "pdf", IsPrimary: true},
				&orm.DocumentProcessingState{DatasetID: "papers", DocumentID: "pdf", ParseStatus: "pending", ChunkStatus: "pending", IndexStatus: "pending", UpdatedAt: now},
				&orm.Task{ID: "task", DatasetID: "papers", DocID: "pdf", LazyllmTaskID: "remote-task", Ext: marshal(map[string]any{"processing_level": "parsed", "processing_revision": 1}), BaseModel: orm.BaseModel{CreatedAt: now}},
				&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "remote-task", DocID: "remote-pdf", KbID: "remote-papers", Status: "SUCCESS", CreatedAt: now, UpdatedAt: now},
				&orm.PaperImportBatch{ID: "upload", EntryType: "pdf_upload", TargetDatasetID: "papers", CreatedBy: "owner", Status: "succeeded", SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{}), CreatedAt: now},
				&orm.PaperImportItem{ID: "upload-item", BatchID: "upload", LibraryPaperID: paper.ID, AcademicWorkID: paper.AcademicWorkID, DocumentID: "pdf", Status: "succeeded", Stage: "done", ResultKind: "imported", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{})},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			readState := func() orm.DocumentProcessingState {
				t.Helper()
				var state orm.DocumentProcessingState
				if err := db.Where("dataset_id='papers' AND document_id='pdf'").Take(&state).Error; err != nil {
					t.Fatal(err)
				}
				return state
			}
			assertPersisted := func(read string, before orm.DocumentProcessingState) {
				t.Helper()
				if after := readState(); !reflect.DeepEqual(before, after) {
					t.Fatalf("%s changed persisted processing state: before %+v, after %+v", read, before, after)
				}
			}
			check := func(parse string, active int, operationState, documentState string) {
				t.Helper()
				before := readState()
				wantError := parse == "failed"
				view := readLibrary(t, "papers")
				assertPersisted("library", before)
				paper := view.Data.Papers[0]
				if paper.Parse != parse || view.Active != (active > 0) || (paper.ProcessingError != "") != wantError {
					t.Errorf("library parse=%q active=%t error=%q, want parse=%q active=%t has error=%t", paper.Parse, view.Active, paper.ProcessingError, parse, active > 0, wantError)
				}
				status, reason, err := paperProcessingStatus(t.Context(), db, document)
				assertPersisted("paper processing status", before)
				if err != nil || status != documentState || (reason != "") != wantError {
					t.Errorf("paper processing status=%q reason=%q err=%v, want status=%q has reason=%t", status, reason, err, documentState, wantError)
				}
				w := libraryRequest(t, "GET", "/imports?view=operations", "papers", "owner", nil)
				assertPersisted("import operations list", before)
				var list struct {
					Notification struct{ Active int } `json:"notification"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || list.Notification.Active != active {
					t.Errorf("want %d active import operations, got %d %s", active, w.Code, w.Body.String())
				}
				w = libraryRequest(t, "GET", "/imports/upload?view=operations", "papers", "owner", nil)
				assertPersisted("import operation detail", before)
				var detail struct {
					Items []ImportOperationItem `json:"items"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || len(detail.Items) != 1 {
					t.Fatal(w.Code, w.Body.String())
				}
				item := detail.Items[0]
				current := item.CurrentDocument
				if item.CurrentState != operationState || current == nil || current.ProcessingStatus != documentState || (current.ProcessingError != "") != wantError {
					t.Errorf("operation state=%q current document=%+v, want state=%q document status=%q has error=%t", item.CurrentState, current, operationState, documentState, wantError)
				}
			}
			check("ready", 0, "ready", "ready")
			before := readState()
			staleVersion, staleRemoteID := document, document
			staleVersion.ResourceVersion++
			staleRemoteID.LazyllmDocID = "old-remote-pdf"
			for _, stale := range []orm.Document{staleVersion, staleRemoteID} {
				status, reason, err := paperProcessingStatus(t.Context(), db, stale)
				assertPersisted("stale paper processing status", before)
				if err != nil || status != "unknown" || reason != "" {
					t.Errorf("paper processing accepted stale document version %d or remote ID %q: %q %q %v", stale.ResourceVersion, stale.LazyllmDocID, status, reason, err)
				}
			}
			// A newer attempt wins even when an older successful callback arrives late.
			if err := db.Create(&orm.Task{ID: "retry", DatasetID: "papers", DocID: "pdf", LazyllmTaskID: "retry-remote", Ext: marshal(map[string]any{"processing_level": "parsed", "processing_revision": 1}), BaseModel: orm.BaseModel{CreatedAt: now.Add(time.Minute)}}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "retry-remote", DocID: "remote-pdf", KbID: "remote-papers", Status: "WORKING", CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}).Error; err != nil {
				t.Fatal(err)
			}
			check("parsing", 1, "processing", "running")
			if err := db.Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("task_id='retry-remote'").Update("status", "FAILED").Error; err != nil {
				t.Fatal(err)
			}
			check("failed", 0, "processing_failed", "failed")
			if err := db.Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("task_id='retry-remote'").Update("status", "SUCCESS").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.DocumentProcessingState{}).Where("document_id='pdf'").Updates(map[string]any{"index_status": "failed", "index_error_code": "EMBEDDING_FAILED", "index_error_message": "embedding failed"}).Error; err != nil {
				t.Fatal(err)
			}
			check("ready", 0, "ready", "ready")
			// Resuming the same task must also supersede a persisted failure.
			if err := db.Model(&orm.DocumentProcessingState{}).Where("document_id='pdf'").Updates(map[string]any{"parse_status": "failed", "updated_at": now.Add(2 * time.Minute)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.Task{}).Where("id='retry'").Update("updated_at", now.Add(3*time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("task_id='retry-remote'").Update("status", "WORKING").Error; err != nil {
				t.Fatal(err)
			}
			check("parsing", 1, "processing", "running")
		})
	}
}

func TestImportOperationsPreserveFailureAndRecognizeLaterResolution(t *testing.T) {
	db := paperLibraryTestDB(t)
	path := filepath.Join(t.TempDir(), "paper.pdf")
	if e := os.WriteFile(path, []byte("%PDF fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, row := range []any{
		&orm.AcademicWork{ID: "work", CanonicalTitle: "Paper", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]any{})},
		&orm.Document{ID: "later-pdf", DatasetID: "papers", DisplayName: "paper.pdf", Ext: marshal(map[string]string{"stored_path": path})},
		&orm.AcademicWorkDocument{AcademicWorkID: "work", DatasetID: "papers", DocumentID: "later-pdf", SourceProvider: "arxiv", SourceVersion: "v1"},
		&orm.PaperImportBatch{ID: "failed", EntryType: "paper_link", TargetDatasetID: "papers", CreatedBy: "owner", Status: "failed", SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{})},
		&orm.PaperImportItem{ID: "failed-item", BatchID: "failed", AcademicWorkID: "work", Status: "failed", Stage: "acquire", ResultKind: "failed", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(FulltextCandidate{SourceProvider: "arxiv", Version: "v1"}), ErrorDetailsJSON: marshal(map[string]any{"message": "old failure"})},
	} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
	w := libraryRequest(t, "GET", "/imports/failed?view=operations", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var detail struct {
		Operation ImportOperation       `json:"operation"`
		Items     []ImportOperationItem `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Items) != 1 || detail.Items[0].ResultKind != "failed" || !detail.Items[0].ResolvedLater || detail.Items[0].CanRetry || detail.Operation.Summary.NeedsAction != 0 {
		t.Fatalf("historical failure or current resolution lost: %+v", detail)
	}
}

func TestImportOperationPostgresProcessingProjection(t *testing.T) {
	db := libraryPostgresDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{}, &orm.AcademicWorkDocument{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}, &orm.PaperLibraryEntry{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.Dataset{ID: "papers", ProcessingLevel: "chunked"},
		&orm.Document{ID: "pdf", DatasetID: "papers"},
		&orm.DocumentProcessingState{DatasetID: "papers", DocumentID: "pdf", ParseStatus: "pending", ChunkStatus: "pending"},
		&orm.PaperImportBatch{ID: "batch", TargetDatasetID: "papers", CreatedBy: "owner", Status: "succeeded", SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{})},
		&orm.PaperImportItem{ID: "item", BatchID: "batch", DocumentID: "pdf", Status: "succeeded", Stage: "done", ResultKind: "imported", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{})},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"ready", "processing", "failed"} {
		var rows []struct{ CurrentState string }
		err := operationStates(db, "owner", "papers", nil, nil, []operationProcessing{{DocumentID: "pdf", State: status}}).Scan(&rows).Error
		want := status
		if status == "failed" {
			want = "processing_failed"
		}
		if err != nil || len(rows) != 1 || rows[0].CurrentState != want {
			t.Fatalf("PostgreSQL processing observation lost: %+v %v", rows, err)
		}
	}
}

func TestImportOperationReadsEachDocumentBatchOnce(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *gorm.DB
			if driver == "postgres" {
				db = paperLibraryTestDB(t, libraryPostgresDB(t))
			} else {
				db = paperLibraryTestDB(t)
			}
			paper := importDraft(t, "papers", "bibliography", libraryDraft("paper", "Paper", "10.1234/reads")).Data.Papers[0]
			path := filepath.Join(t.TempDir(), "paper.pdf")
			if err := os.WriteFile(path, []byte("%PDF fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			batch := orm.PaperImportBatch{ID: "batch", TargetDatasetID: "papers", CreatedBy: "owner", Status: "succeeded", SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{})}
			if err := db.Create(&batch).Error; err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 20; i++ {
				document := orm.Document{ID: fmt.Sprintf("pdf-%d", i), DatasetID: "papers", DisplayName: "paper.pdf", Ext: marshal(map[string]string{"stored_path": path})}
				if err := db.Create(&document).Error; err != nil {
					t.Fatal(err)
				}
			}
			counts := map[string]int{}
			recording := false
			callback := "count_import_document_reads"
			if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if recording {
					counts[tx.Statement.Table]++
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Callback().Query().Remove(callback) })
			measure := func(total int, repeated bool) map[string]int {
				t.Helper()
				if err := db.Where("batch_id=?", batch.ID).Delete(&orm.PaperImportItem{}).Error; err != nil {
					t.Fatal(err)
				}
				for i := 0; i < total; i++ {
					document := fmt.Sprintf("pdf-%d", i)
					if repeated {
						document = "pdf-0"
					}
					item := orm.PaperImportItem{ID: fmt.Sprintf("item-%d", i), BatchID: batch.ID, LibraryPaperID: paper.ID, AcademicWorkID: paper.AcademicWorkID, DocumentID: document, Status: "succeeded", Stage: "done", ResultKind: "imported", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{})}
					if err := db.Create(&item).Error; err != nil {
						t.Fatal(err)
					}
				}
				counts = map[string]int{}
				recording = true
				response := libraryRequest(t, "GET", "/imports/batch?view=operations", "papers", "owner", nil)
				recording = false
				if response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
				var result struct {
					Items []ImportOperationItem `json:"items"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Items) != total {
					t.Fatalf("wrong page size %d", len(result.Items))
				}
				for _, item := range result.Items {
					if item.CurrentDocument == nil || item.CurrentState != "ready" || item.CurrentDocument.ProcessingStatus != "ready" {
						t.Fatalf("wrong current document: %+v", item)
					}
				}
				return counts
			}
			single := measure(1, true)
			if single["documents"] == 0 {
				t.Fatal("document read was not measured")
			}
			for _, repeated := range []bool{true, false} {
				batch := measure(20, repeated)
				for _, table := range []string{"documents", "datasets", "tasks", "document_processing_states"} {
					if batch[table] != single[table] {
						t.Fatalf("per-item reads remain for %s, repeated=%v single=%v batch=%v", table, repeated, single, batch)
					}
				}
			}
		})
	}
}
