package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDocumentMetadataUsesCurrentParserTaskBeforeConversion(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	now := time.Now().UTC()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "ds", "kb", "general_algo", "user-1", now)
	row := orm.Document{ID: "doc", DatasetID: "ds", LazyllmDocID: "external", PDFConvertResult: "NONE", Ext: mustJSON(documentExt{ConvertStatus: "NONE"}), BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Task{ID: "current", DatasetID: "ds", DocID: "doc", LazyllmTaskID: "current-external", BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "current-external", DocID: "external", KbID: "kb", Status: "SUCCESS", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	svc := mustDocumentService(t, db)
	rec, err := svc.loadRecord(context.Background(), "user-1", "ds", "doc", DatasetCatalogCaller{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.metadata().ParseStatus; got != "SUCCESS" {
		t.Fatalf("conversion NONE masks actual parser success: %s", got)
	}
	// A newer Core task is authoritative even if an old parser task finishes later.
	later := now.Add(time.Minute)
	if err := db.Create(&orm.Task{ID: "new", DatasetID: "ds", DocID: "doc", LazyllmTaskID: "new-external", BaseModel: orm.BaseModel{CreatedAt: later, UpdatedAt: later}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "new-external", DocID: "external", KbID: "kb", Status: "RUNNING", CreatedAt: later, UpdatedAt: later}).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("task_id = ?", "current-external").Update("updated_at", later.Add(time.Minute))
	db.Model(&orm.Document{}).Where("id = ?", "doc").Update("pdf_convert_result", "SUCCEEDED")
	rec, err = svc.loadRecord(context.Background(), "user-1", "ds", "doc", DatasetCatalogCaller{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.metadata().ParseStatus; got != "RUNNING" {
		t.Fatalf("old success masks current parser: %s", got)
	}
}

func TestStoredOriginalBindingIsTerminalWithoutSubmission(t *testing.T) {
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "stored", "kb", "general_algo", "user-1", time.Now())
	db.Model(&orm.Dataset{}).Where("id = ?", "stored").Update("processing_level", ProcessingLevelStored)
	svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	caller := DatasetCatalogCaller{UserID: "user-1"}
	// Reuse a controlled acquisition; binding alone is the stored cross-move path.
	ref, err := svc.AcquireOriginal(context.Background(), caller, AcquireOriginalRequest{Filename: "note.txt", Reader: strings.NewReader("fixture")})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := svc.PrepareOriginalBinding(context.Background(), caller, ImportOriginalRequest{FileRef: ref.FileRef, DatasetID: "stored"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := PrepareResourceAuthorization(context.Background(), caller, prepared.AuthorizationRequests())
	if err != nil {
		t.Fatal(err)
	}
	var result OriginalImportResult
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bindErr error
		result, bindErr = svc.BindOriginalTx(ctx, tx, caller, prepared)
		return bindErr
	})
	if err != nil {
		t.Fatal(err)
	}
	var ext taskExt
	json.Unmarshal(result.Task.Ext, &ext)
	if ext.TaskState != string(TaskStateSucceeded) {
		t.Fatalf("stored binding incorrectly appears uploading: %q", ext.TaskState)
	}
}

func TestProcessingObservationUsesExactBindingAndSubmittedRevision(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Now().UTC()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "ds", "kb", "general_algo", "user-1", now)
	var dataset orm.Dataset
	db.Where("id = ?", "ds").Take(&dataset)
	dataset.ProcessingLevel = ProcessingLevelParsed
	db.Model(&dataset).Update("processing_level", dataset.ProcessingLevel)
	row := orm.Document{ID: "doc", DatasetID: "ds", LazyllmDocID: "external", ResourceVersion: 1, BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	state := newDocumentProcessingState("ds", "doc", now)
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.Task{ID: "task", DatasetID: "ds", DocID: "doc", LazyllmTaskID: "external-task", Ext: mustJSON(taskExt{ProcessingLevel: ProcessingLevelParsed, ProcessingRevision: dataset.ProcessingRevision}), BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	external := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "external-task", DocID: "external", KbID: "kb", Status: "SUCCESS", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&external).Error; err != nil {
		t.Fatal(err)
	}
	got, _, _, err := ObserveDocumentProcessingDetails(context.Background(), db.DB, db.DB, row)
	if err != nil || got.ParseStatus != "succeeded" || got.ChunkStatus != "unknown" || got.IndexStatus != "unknown" {
		t.Fatalf("parsed stage overclaimed: %+v %v", got, err)
	}
	coverage, err := datasetProcessingCoverage(context.Background(), db.DB, db.DB, dataset)
	if err != nil || coverage.Available != 1 || coverage.Processing != 0 {
		t.Fatalf("coverage remains pending: %+v %v", coverage, err)
	}
	// The read overlay does not rewrite processing state or its revision.
	var stored orm.DocumentProcessingState
	db.Where("document_id = ?", "doc").Take(&stored)
	if stored.ParseStatus != "pending" || stored.Revision != state.Revision {
		t.Fatalf("read mutated state: %+v", stored)
	}
	// An exact task ID associated with another document cannot publish success.
	db.Model(&external).Update("doc_id", "foreign")
	got, raw, _, err := ObserveDocumentProcessingDetails(context.Background(), db.DB, db.DB, row)
	if err != nil || got.ParseStatus != "unknown" || raw != "UNKNOWN" {
		t.Fatalf("foreign binding accepted: %+v %q %v", got, raw, err)
	}
	db.Model(&external).Update("doc_id", "external")
	// Same for another KB, even with the same task/document strings.
	db.Model(&external).Update("kb_id", "foreign-kb")
	got, _, _, err = ObserveDocumentProcessingDetails(context.Background(), db.DB, db.DB, row)
	if err != nil || got.ParseStatus != "unknown" {
		t.Fatalf("foreign KB accepted: %+v %v", got, err)
	}
	db.Model(&external).Update("kb_id", "kb")
	// Current default cannot turn an old parsed task into successful indexing.
	previousRevision := dataset.ProcessingRevision
	db.Model(&dataset).Updates(map[string]any{"processing_level": ProcessingLevelIndexed, "processing_revision": previousRevision + 1})
	db.Model(&task).Update("ext", mustJSON(taskExt{ProcessingLevel: ProcessingLevelIndexed, ProcessingRevision: previousRevision}))
	got, _, _, err = ObserveDocumentProcessingDetails(context.Background(), db.DB, db.DB, row)
	if err != nil || got.ParseStatus != "succeeded" || got.IndexStatus != "unknown" {
		t.Fatalf("stale revision inferred indexing: %+v %v", got, err)
	}
	// Historical task with no level still proves only parsing.
	db.Model(&task).Update("ext", mustJSON(taskExt{}))
	got, _, _, err = ObserveDocumentProcessingDetails(context.Background(), db.DB, db.DB, row)
	if err != nil || got.ParseStatus != "succeeded" || got.IndexStatus != "unknown" {
		t.Fatalf("historical task overclaimed: %+v %v", got, err)
	}
}

func TestProcessingObservationMissingEvidenceIsUnknown(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Now().UTC()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "ds", "kb", "general_algo", "user-1", now)
	row := orm.Document{ID: "doc", DatasetID: "ds", LazyllmDocID: "external", ResourceVersion: 1}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Task{ID: "task", DatasetID: "ds", DocID: "doc", LazyllmTaskID: "missing", Ext: mustJSON(taskExt{TaskState: "RUNNING"})}).Error; err != nil {
		t.Fatal(err)
	}
	for _, lazy := range []*gorm.DB{db.DB, nil} {
		state, raw, _, err := ObserveDocumentProcessingDetails(context.Background(), db.DB, lazy, row)
		if err != nil || raw != "UNKNOWN" || state.ParseStatus != "unknown" {
			t.Fatalf("missing task invented status: %+v %q %v", state, raw, err)
		}
	}
	db.Model(&row).Update("resource_version", 2)
	row.ResourceVersion = 1
	state, raw, _, err := ObserveDocumentProcessingDetails(context.Background(), db.DB, db.DB, row)
	if err != nil || raw != "UNKNOWN" || state.ParseStatus != "unknown" || state.ChunkStatus != "unknown" || state.IndexStatus != "unknown" {
		t.Fatalf("changed binding invented pending: %+v %q %v", state, raw, err)
	}
}

func TestProcessingCoverageBatchesOwnedTasks(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Now().UTC()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "ds", "kb", "general_algo", "user-1", now)
	var dataset orm.Dataset
	db.Where("id = ?", "ds").Take(&dataset)
	dataset.ProcessingLevel = ProcessingLevelParsed
	db.Model(&dataset).Update("processing_level", dataset.ProcessingLevel)
	const documentCount = 501
	for i := 0; i < documentCount; i++ {
		id := fmt.Sprintf("doc-%02d", i)
		for _, value := range []any{
			&orm.Document{ID: id, DatasetID: "ds", LazyllmDocID: id},
			&orm.Task{ID: "task-" + id, DatasetID: "ds", DocID: id, LazyllmTaskID: "external-" + id, BaseModel: orm.BaseModel{CreatedAt: now}},
			&orm.Task{ID: "old-" + id, DatasetID: "ds", DocID: id, LazyllmTaskID: "old-external-" + id, BaseModel: orm.BaseModel{CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(time.Hour)}},
			&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "external-" + id, DocID: id, KbID: "kb", Status: "SUCCESS"},
		} {
			if err := db.Create(value).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	queries := 0
	const callback = "test:processing-query-count"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(*gorm.DB) { queries++ }); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove(callback)
	coverage, err := datasetProcessingCoverage(context.Background(), db.DB, db.DB, dataset)
	if err != nil || coverage.Available != documentCount || coverage.Processing != 0 || coverage.Total != documentCount {
		t.Fatalf("wrong coverage: %+v %v", coverage, err)
	}
	if queries != 6 {
		t.Fatalf("expected 4 Core queries and 2 bounded external batches, got %d queries for %d documents", queries, documentCount)
	}
}

func TestIndexedSubmissionRecordsLevelRevisionAndReturnsAllObservedStages(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Now().UTC()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "indexed", "kb", "general_algo", "user-1", now)
	db.Model(&orm.Dataset{}).Where("id = ?", "indexed").Updates(map[string]any{"processing_level": "indexed", "processing_revision": int64(7)})
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("actual indexed submission fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	document := orm.Document{ID: "doc", DatasetID: "indexed", DisplayName: "note.txt", Ext: mustJSON(documentExt{StoredPath: path, StoredName: "note.txt", ContentType: "text/plain"})}
	task := orm.Task{ID: "task", DatasetID: "indexed", DocID: "doc", TaskType: string(TaskTypeParseUploaded), Ext: mustJSON(taskExt{})}
	for _, row := range []any{&document, &task} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := createDocumentProcessingState(db.DB, "indexed", "doc", now); err != nil {
		t.Fatal(err)
	}
	submitted := false
	installDocumentServiceTransport(t, func(req *http.Request) (int, string) {
		if req.URL.Path == "/v1/docs/add" {
			var payload addRequest
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.ProcessingLevel != "indexed" {
				t.Fatalf("wrong submitted level: %s", payload.ProcessingLevel)
			}
			submitted = true
			return 200, `{"items":[{"task_id":"external-task","doc_id":"external","core_task_id":"task","core_document_id":"doc"}]}`
		}
		return 404, `{}`
	})
	results, err := startParseTasksAtLevel(context.Background(), "user-1", "indexed", []string{"task"}, "")
	if err != nil || !submitted || len(results) != 1 {
		t.Fatalf("submission failed: %+v %v", results, err)
	}
	db.Where("id = ?", "task").Take(&task)
	var ext taskExt
	json.Unmarshal(task.Ext, &ext)
	if ext.ProcessingLevel != "indexed" || ext.ProcessingRevision != 7 {
		t.Fatalf("actual submission proof lost: %+v", ext)
	}
	if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: task.LazyllmTaskID, DocID: "external", KbID: "kb", Status: "SUCCESS"}).Error; err != nil {
		t.Fatal(err)
	}
	metadata, err := mustDocumentService(t, db).GetDocumentMetadata(context.Background(), DocumentGetRequest{UserID: "user-1", DatasetID: "indexed", DocumentID: "doc"})
	if err != nil || metadata.ParseStatus != "SUCCESS" || metadata.ProcessingLevel != "indexed" || metadata.ProcessingState.ParseStatus != "succeeded" || metadata.ProcessingState.ChunkStatus != "succeeded" || metadata.ProcessingState.IndexStatus != "succeeded" {
		t.Fatalf("full observed metadata missing: %+v %v", metadata, err)
	}
}
