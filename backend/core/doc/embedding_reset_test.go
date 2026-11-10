package doc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

func TestClearedChunksCanBeManuallyParsedWithoutRAG(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "false")
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.UserUIPreferences{}); err != nil {
		t.Fatal(err)
	}
	installOriginalSourceTransport(t)
	now := time.Now().UTC()
	source := filepath.Join(t.TempDir(), "original.txt")
	if err := os.WriteFile(source, []byte("retained original"), 0600); err != nil {
		t.Fatal(err)
	}
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	document := orm.Document{ID: "doc-1", DatasetID: "dataset-1", Ext: mustJSON(documentExt{StoredPath: source})}
	state := newDocumentProcessingState("dataset-1", "doc-1", now)
	state.EmbeddingVersion = embeddingVersionReembedManual
	task := orm.Task{ID: "manual-parse", DatasetID: "dataset-1", DocID: "doc-1", TaskType: string(TaskTypeReparse)}
	for _, row := range []any{&document, &state, &task} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	prepares := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/scan/internal/source-access/by-dataset:batch" {
			return previous.RoundTrip(r)
		}
		if r.URL.Path != "/internal/documents:prepare" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		prepares++
		return testJSONResponse(200, string(mustJSON(map[string]any{
			"artifact_ref": source + baseArtifactSuffix, "source_sha256": "test-sha",
			"parser_version": "native-reader-v1", "chunker_version": "baseline-v1",
		}))), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", "user-1")
	oldTask := task
	oldTask.TaskType = string(TaskTypeParse)
	if err := prepareBaseDocument(req, "dataset-1", oldTask); err == nil || prepares != 0 {
		t.Fatal("old automatic parsing resumed after cleanup")
	}
	results, err := startTasksInternal(req, "dataset-1", []string{task.ID})
	if err != nil || len(results) != 1 || results[0].Status != "STARTED" || prepares != 1 {
		t.Fatalf("manual native parsing failed: %+v prepares=%d err=%v", results, prepares, err)
	}
	if err := db.Where("document_id = ?", document.ID).Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.ParseStatus != "succeeded" || state.ChunkStatus != "succeeded" || state.EmbeddingVersion != "" {
		t.Fatalf("manual native parsing did not restore chunks: %+v", state)
	}
	if original, err := os.ReadFile(source); err != nil || string(original) != "retained original" {
		t.Fatal("original file was changed")
	}
}

func TestClearedChunksRequireManualReparseAndIgnoreOldSuccess(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := newDocumentTestDB(t)
	seedSelectedEmbeddingForDocTest(t, db, "user-1")
	if err := db.AutoMigrate(&orm.EmbeddingIndexState{}, &orm.UserUIPreferences{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	seedDocumentServiceLazyDocument(t, db, "dataset-1", "doc-1", "lazy-doc-1", "user-1")
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset-1").Update("processing_level", ProcessingLevelIndexed).Error; err != nil {
		t.Fatal(err)
	}
	state := newDocumentProcessingState("dataset-1", "doc-1", now)
	state.EmbeddingVersion, state.Revision = embeddingVersionReembedManual, 2
	oldTask := orm.Task{ID: "old-task", DatasetID: "dataset-1", DocID: "doc-1", LazyllmTaskID: "old-remote",
		TaskType: string(TaskTypeReparse), Ext: mustJSON(taskExt{ProcessingLevel: ProcessingLevelIndexed}),
		BaseModel: orm.BaseModel{CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}}
	for _, row := range []any{&state, &oldTask,
		&readonlyorm.LazyLLMDocRow{DocID: "lazy-doc-1", Filename: "original.txt"},
		&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "old-remote", DocID: "lazy-doc-1", KbID: "kb-dataset-1", Status: "SUCCESS", UpdatedAt: now.Add(time.Minute)},
		&orm.EmbeddingIndexState{Scope: "global", Generation: 8, Phase: "ready", Signature: "v1:3:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64),
			ActiveModelID: "embedding-model", ActiveSource: "own", ActiveModelName: "text-embedding-3-small", ActiveProviderName: "OpenAI"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", "user-1")
	installOriginalSourceTransport(t)
	previous := http.DefaultTransport
	submissions := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/scan/internal/source-access/by-dataset:batch" {
			return previous.RoundTrip(r)
		}
		if r.URL.Path != "/v1/docs/reparse" {
			t.Fatalf("unexpected request after cleanup: %s", r.URL.Path)
		}
		submissions++
		var body reparseRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Strategy != "rebuild" || len(body.NgNames) != 0 || body.ModelConfig["_embedding_generation"] != float64(8) {
			t.Fatalf("manual reparse did not rebuild with current model generation: %+v", body)
		}
		return testJSONResponse(200, `{"task_ids":["new-remote"]}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })

	syncProcessingTaskStates(req, "dataset-1")
	reconcileDeferredIndexingWithReady(context.Background(), func(context.Context, string) (bool, error) { return true, nil })
	_, err := mustDocumentService(t, db).EnsureDocumentParsed(req, EnsureDocumentParsedRequest{
		UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1",
	})
	if err == nil || submissions != 0 {
		t.Fatalf("reading a cleared document scheduled parsing: submissions=%d err=%v", submissions, err)
	}
	var document orm.Document
	if err := db.First(&document, "id = ?", "doc-1").Error; err != nil {
		t.Fatal(err)
	}
	observed, _, _, err := ObserveDocumentProcessingDetails(context.Background(), db.DB, db.DB, document)
	if err != nil || observed.ParseStatus != "pending" || observed.IndexStatus != "pending" {
		t.Fatalf("old success restored cleared chunks: %+v err=%v", observed, err)
	}

	assertListedStage := func(want string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/datasets/dataset-1/documents:search", strings.NewReader(`{"page_size":10}`))
		request.Header.Set("X-User-Id", "user-1")
		request = mux.SetURLVars(request, map[string]string{"dataset": "dataset-1"})
		response := httptest.NewRecorder()
		SearchDocuments(response, request)
		var list ListDocumentsResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list.Documents) != 1 {
			t.Fatalf("document search failed: %d %s", response.Code, response.Body.String())
		}
		if got := list.Documents[0].DocumentStage; got != want {
			t.Fatalf("listed document stage = %q, want %q", got, want)
		}
	}
	assertListedStage("DOCUMENT_REPARSE_REQUIRED")

	newTask := orm.Task{ID: "new-task", DatasetID: "dataset-1", DocID: "doc-1", TaskType: string(TaskTypeReparse),
		BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&newTask).Error; err != nil {
		t.Fatal(err)
	}
	results, err := startReparseTasksInternal(req, "dataset-1", []string{newTask.ID})
	if err != nil || submissions != 1 || len(results) != 1 || results[0].Status != "STARTED" {
		t.Fatalf("manual reparse failed: %+v submissions=%d err=%v", results, submissions, err)
	}
	current := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "new-remote", DocID: "lazy-doc-1", KbID: "kb-dataset-1", Status: "WORKING", UpdatedAt: now}
	if err := db.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	assertListedStage("DOCUMENT_PARSING")
	if err := db.Model(&current).Update("status", "SUCCESS").Error; err != nil {
		t.Fatal(err)
	}
	syncProcessingTaskStates(req, "dataset-1")
	if err := db.Where("dataset_id = ? AND document_id = ?", "dataset-1", "doc-1").Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.ParseStatus != "succeeded" || state.ChunkStatus != "succeeded" || state.IndexStatus != "succeeded" || state.EmbeddingVersion != "" {
		t.Fatalf("manual parsing did not restore document: %+v", state)
	}
	assertListedStage("SUCCESS")
}
