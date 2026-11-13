package doc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

func TestDeferredIndexingReembedsLegacyDocumentWithoutNativeArtifact(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := newDocumentTestDB(t)
	seedSelectedEmbeddingForDocTest(t, db, "u1")
	now := time.Now().UTC()
	dataset := orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: ProcessingLevelIndexed,
		Ext:       mustJSON(map[string]any{"algo_id": "general_algo"}),
		BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}}
	document := orm.Document{ID: "core-doc", LazyllmDocID: "remote-doc", DatasetID: dataset.ID, DisplayName: "legacy.txt"}
	state := orm.DocumentProcessingState{DatasetID: dataset.ID, DocumentID: document.ID,
		ParseStatus: "succeeded", ChunkStatus: "succeeded", IndexStatus: "pending",
		EmbeddingVersion: "reembed:pending", Revision: 1, UpdatedAt: now.Add(-10 * time.Minute)}
	oldTask := orm.Task{ID: "old-task", DatasetID: dataset.ID, DocID: document.ID,
		LazyllmTaskID: "old-remote-task", TaskType: string(TaskTypeReparse),
		Ext:       mustJSON(taskExt{ProcessingLevel: ProcessingLevelIndexed, TaskState: string(TaskStateSucceeded)}),
		BaseModel: orm.BaseModel{CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}}
	oldRemote := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: oldTask.LazyllmTaskID,
		DocID: document.LazyllmDocID, KbID: dataset.KbID, Status: "SUCCESS"}
	for _, row := range []any{&dataset, &document, &state, &oldTask, &oldRemote} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/deferred-indexing", nil)
	req.Header.Set("X-User-Id", "u1")
	syncProcessingTaskStates(req, dataset.ID)
	if err := db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.IndexStatus != "pending" || state.ParseStatus != "succeeded" || state.ChunkStatus != "succeeded" {
		t.Fatalf("old SUCCESS completed offline-cleared vectors: %+v", state)
	}
	observed, _, _, err := ObserveDocumentProcessingDetails(context.Background(), db.DB, db.DB, document)
	if err != nil || observed.IndexStatus == "succeeded" {
		t.Fatalf("legacy observation accepted old SUCCESS: %+v, %v", observed, err)
	}

	previous := http.DefaultTransport
	var submitted []reparseRequest
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/kbs/kb":
			return testJSONResponse(200, `{"data":{"algo_ids":["general_algo"]}}`), nil
		case "/v1/docs/reparse":
			var body reparseRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			submitted = append(submitted, body)
			return testJSONResponse(200, `{"task_ids":["new-remote-task"]}`), nil
		default:
			return testJSONResponse(503, `{}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	reconcileDeferredIndexingWithReady(context.Background(), func(context.Context, string) (bool, error) { return true, nil })
	if len(submitted) != 1 {
		t.Fatalf("wanted one reembed submission, got %d", len(submitted))
	}
	if submitted[0].Strategy != "reembed" || !reflect.DeepEqual(submitted[0].NgNames, []string{"block", "line", "code", "doc-summary"}) {
		t.Fatalf("legacy document did not use existing text chunks: %+v", submitted[0])
	}
	if err := db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.IndexArtifactRef == "" || state.IndexArtifactRef == "core-task:old-task" {
		t.Fatalf("new reembed task lacks a generation binding: %+v", state)
	}
	var newTask orm.Task
	if err := db.Where("id = ?", strings.TrimPrefix(state.IndexArtifactRef, "core-task:")).Take(&newTask).Error; err != nil {
		t.Fatal(err)
	}
	if newTask.LazyllmTaskID != "new-remote-task" {
		t.Fatalf("reembed submission was not bound to its remote task: %+v", newTask)
	}
	newRemote := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: newTask.LazyllmTaskID,
		DocID: document.LazyllmDocID, KbID: dataset.KbID, Status: "SUCCESS"}
	if err := db.Create(&newRemote).Error; err != nil {
		t.Fatal(err)
	}
	syncProcessingTaskStates(req, dataset.ID)
	if err := db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state).Error; err != nil ||
		state.IndexStatus != "succeeded" || state.ParseStatus != "succeeded" || state.ChunkStatus != "succeeded" {
		t.Fatalf("new generation did not complete indexing while preserving chunks: %+v err=%v", state, err)
	}
}

func TestDeferredIndexingRebuildsFromOriginalWhenNoRemoteDocumentExists(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := newDocumentTestDB(t)
	seedSelectedEmbeddingForDocTest(t, db, "u1")
	source := filepath.Join(t.TempDir(), "legacy.txt")
	if err := os.WriteFile(source, []byte("original text"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	dataset := orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: ProcessingLevelIndexed,
		Ext:       mustJSON(map[string]any{"algo_id": "general_algo"}),
		BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}}
	document := orm.Document{ID: "core-doc", DatasetID: dataset.ID, DisplayName: "legacy.txt",
		Ext: mustJSON(documentExt{StoredPath: source})}
	state := orm.DocumentProcessingState{DatasetID: dataset.ID, DocumentID: document.ID,
		ParseStatus: "succeeded", ChunkStatus: "succeeded", IndexStatus: "pending",
		EmbeddingVersion: "reembed:pending", Revision: 1, UpdatedAt: now.Add(-10 * time.Minute)}
	for _, row := range []any{&dataset, &document, &state} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	adds := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/kbs/kb":
			return testJSONResponse(200, `{"data":{"algo_ids":["general_algo"]}}`), nil
		case "/v1/docs/add":
			var body addRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ProcessingLevel != ProcessingLevelIndexed || len(body.Items) != 1 || body.Items[0].FilePath != source {
				t.Fatalf("original source was not used for rebuild: %+v", body)
			}
			adds++
			return testJSONResponse(200, `{"items":[{"doc_id":"remote-doc","task_id":"remote-task","core_document_id":"core-doc"}]}`), nil
		default:
			return testJSONResponse(503, `{}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	reconcileDeferredIndexingWithReady(context.Background(), func(context.Context, string) (bool, error) { return true, nil })
	if adds != 1 {
		t.Fatalf("wanted original-source rebuild, got %d submissions", adds)
	}
}

func TestDeferredIndexingMarksMissingOriginalForManualRebuild(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := newDocumentTestDB(t)
	seedSelectedEmbeddingForDocTest(t, db, "u1")
	now := time.Now().UTC()
	dataset := orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: ProcessingLevelIndexed,
		Ext:       mustJSON(map[string]any{"algo_id": "general_algo"}),
		BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}}
	document := orm.Document{ID: "core-doc", DatasetID: dataset.ID, DisplayName: "missing.txt"}
	state := orm.DocumentProcessingState{DatasetID: dataset.ID, DocumentID: document.ID,
		ParseStatus: "succeeded", ChunkStatus: "succeeded", IndexStatus: "pending",
		EmbeddingVersion: "reembed:pending", ParseArtifactRef: "retained-artifact",
		Revision: 1, UpdatedAt: now.Add(-10 * time.Minute)}
	oldTask := orm.Task{ID: "old-task", DatasetID: dataset.ID, DocID: document.ID,
		LazyllmTaskID: "old-remote-task", TaskType: string(TaskTypeReparse),
		Ext:       mustJSON(taskExt{ProcessingLevel: ProcessingLevelIndexed, TaskState: string(TaskStateSucceeded)}),
		BaseModel: orm.BaseModel{CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}}
	oldRemote := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: oldTask.LazyllmTaskID,
		DocID: document.LazyllmDocID, KbID: dataset.KbID, Status: "SUCCESS"}
	for _, row := range []any{&dataset, &document, &state, &oldTask, &oldRemote} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/kbs/kb" {
			return testJSONResponse(200, `{"data":{"algo_ids":["general_algo"]}}`), nil
		}
		return testJSONResponse(503, `{}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	reconcileDeferredIndexingWithReady(context.Background(), func(context.Context, string) (bool, error) { return true, nil })
	if err := db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.IndexStatus != "failed" || state.EmbeddingVersion != "reembed:manual" ||
		state.IndexErrorCode != "MANUAL_REBUILD_REQUIRED" || state.IndexErrorMessage == "" {
		t.Fatalf("missing source should require manual rebuild: %+v", state)
	}
	var taskCount int64
	if err := db.Model(&orm.Task{}).Count(&taskCount).Error; err != nil || taskCount != 1 {
		t.Fatalf("missing source created a futile task: count=%d err=%v", taskCount, err)
	}
	if err := db.Model(&orm.DocumentProcessingState{}).
		Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).
		Update("updated_at", now.Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	reconcileDeferredIndexingWithReady(context.Background(), func(context.Context, string) (bool, error) { return true, nil })
	if err := db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state).Error; err != nil || state.IndexStatus != "failed" {
		t.Fatalf("old SUCCESS resurrected manually flagged vectors: %+v err=%v", state, err)
	}
	if err := db.Model(&orm.Task{}).Count(&taskCount).Error; err != nil || taskCount != 1 {
		t.Fatalf("manual rebuild state retried automatically: count=%d err=%v", taskCount, err)
	}
	if err := db.Model(&orm.DocumentProcessingState{}).
		Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).
		Update("parse_artifact_ref", "").Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/deferred-indexing", nil)
	req.Header.Set("X-User-Id", "u1")
	syncProcessingTaskStates(req, dataset.ID)
	if err := db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state).Error; err != nil || state.IndexStatus != "failed" {
		t.Fatalf("old SUCCESS resurrected manual state without native artifact: %+v err=%v", state, err)
	}
	document.LazyllmDocID = "rebuilt-doc"
	if err := db.Model(&orm.Document{}).Where("id = ?", document.ID).Update("lazyllm_doc_id", document.LazyllmDocID).Error; err != nil {
		t.Fatal(err)
	}
	manualTask := orm.Task{ID: "manual-task", DatasetID: dataset.ID, DocID: document.ID,
		LazyllmTaskID: "manual-remote-task", TaskType: string(TaskTypeParseUploaded),
		Ext:       mustJSON(taskExt{ProcessingLevel: ProcessingLevelIndexed, TaskState: string(TaskStateRunning)}),
		BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	manualRemote := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: manualTask.LazyllmTaskID,
		DocID: document.LazyllmDocID, KbID: dataset.KbID, Status: "SUCCESS"}
	for _, row := range []any{&manualTask, &manualRemote} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	bindNativeIndexTask(context.Background(), dataset.ID, document.ID, manualTask.ID, ProcessingLevelIndexed, 0)
	if err := db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state).Error; err != nil || state.IndexArtifactRef != "core-task:manual-task" {
		t.Fatalf("manual rebuild was not bound to a fresh generation: %+v err=%v", state, err)
	}
	syncProcessingTaskStates(req, dataset.ID)
	if err := db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state).Error; err != nil ||
		state.IndexStatus != "succeeded" || state.IndexErrorCode != "" || state.IndexErrorMessage != "" {
		t.Fatalf("manual rebuild did not complete indexing: %+v err=%v", state, err)
	}
}
