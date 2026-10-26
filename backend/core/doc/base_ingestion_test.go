package doc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

func TestNativeChunksDoNotMasqueradeAsOtherGroups(t *testing.T) {
	artifact := &baseArtifact{Chunks: []baseNode{{ID: "chunk", Text: "text"}},
		Roots: []baseNode{{Text: "root"}}}
	for _, group := range []string{"image", "summary", "qa"} {
		result, err := baseDocumentChunks(artifact, "", 20, group)
		if err != nil || result.TotalSize != 0 || len(result.Chunks) != 0 {
			t.Fatalf("unexpected %s chunks: %+v, %v", group, result, err)
		}
	}
	result, err := baseDocumentChunks(artifact, "", 20, "block")
	if err != nil || result.TotalSize != 1 || result.Chunks[0].Text != "text" {
		t.Fatalf("missing native chunks: %+v, %v", result, err)
	}
}

func TestNativeIngestionWithoutRAGAndDeferredSubmission(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "false")
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-internal-token")
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "native.txt")
	if err := os.WriteFile(source, []byte("saved text"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("saved text"))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	ds := orm.Dataset{ID: "native-kb", KbID: "native-kb", ProcessingLevel: ProcessingLevelIndexed,
		Ext: mustJSON(map[string]any{"algo_id": "general_algo"}), BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}}
	document := orm.Document{ID: "native-doc", DatasetID: ds.ID, DisplayName: "native.txt", Ext: mustJSON(documentExt{StoredPath: source})}
	task := orm.Task{ID: "native-task", DatasetID: ds.ID, DocID: document.ID, TaskType: string(TaskTypeParse),
		BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	state := newDocumentProcessingState(ds.ID, document.ID, now)
	for _, row := range []any{&ds, &document, &task, &state} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	prepares, adds := 0, 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/scan/internal/source-access/by-dataset:batch":
			return previous.RoundTrip(r)
		case "/internal/documents:prepare":
			prepares++
			if r.Header.Get("X-LazyMind-Internal-Token") != "test-internal-token" {
				t.Fatal("missing internal auth")
			}
			artifact := baseArtifact{SourceSHA: hash, ParserVersion: "native-reader-v1", ChunkerVersion: "baseline-v1",
				Roots: []baseNode{{Text: "saved text"}}, Chunks: []baseNode{{ID: "chunk", Text: "saved text"}}}
			if err := os.WriteFile(source+baseArtifactSuffix, mustJSON(artifact), 0600); err != nil {
				t.Fatal(err)
			}
			return testJSONResponse(200, string(mustJSON(map[string]any{
				"artifact_ref": source + baseArtifactSuffix, "source_sha256": hash,
				"parser_version": artifact.ParserVersion, "chunker_version": artifact.ChunkerVersion,
			}))), nil
		case "/v1/kbs/native-kb":
			return testJSONResponse(200, `{"data":{"algo_ids":["general_algo"]}}`), nil
		case "/v1/docs/add":
			adds++
			var body addRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ProcessingLevel != ProcessingLevelIndexed || body.Items[0].FilePath != source {
				t.Fatalf("must index from original with retained roots: %#v", body)
			}
			return testJSONResponse(200, string(mustJSON(map[string]any{"items": []any{map[string]any{
				"doc_id": document.ID, "task_id": "remote-task",
				"core_task_id": body.Items[0].Metadata["core_task_id"], "core_document_id": document.ID,
			}}}))), nil
		default:
			// Model config discovery may be unavailable; submission must still
			// preserve the base artifact and use the selected processing level.
			return testJSONResponse(503, `{}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-User-Id", "u1")
	if _, err := startTasksInternal(req, ds.ID, []string{task.ID}); err != nil {
		t.Fatal(err)
	}
	db.Where("document_id = ?", document.ID).Take(&state)
	if prepares != 1 || adds != 0 || state.ParseStatus != "succeeded" ||
		state.ChunkStatus != "succeeded" || state.IndexStatus != "pending" {
		t.Fatalf("state: %+v", state)
	}
	rec := documentServiceRecord{row: document, ext: documentExt{StoredPath: source}}
	artifact, err := readBaseArtifact(context.Background(), rec)
	if err != nil || artifact == nil {
		t.Fatalf("artifact: %v %v", artifact, err)
	}
	chunks, err := baseDocumentChunks(artifact, "", 10, "block")
	if err != nil || len(chunks.Chunks) != 1 || chunks.Chunks[0].Text != "saved text" {
		t.Fatal(chunks, err)
	}
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db.Model(&state).Update("updated_at", time.Now().UTC().Add(-10*time.Minute))
	reconcileDeferredIndexingWithReady(context.Background(), func(context.Context, string) (bool, error) { return false, nil })
	if adds != 0 {
		t.Fatal("must wait for embedding readiness")
	}
	reconcileDeferredIndexingWithReady(context.Background(), func(context.Context, string) (bool, error) { return true, nil })
	if adds != 1 {
		t.Fatalf("expected one deferred submission, got %d", adds)
	}
	reconcileDeferredIndexingWithReady(context.Background(), func(context.Context, string) (bool, error) { return true, nil })
	if adds != 1 {
		t.Fatal("must not submit twice")
	}
	db.Where("document_id = ?", document.ID).Take(&state)
	if state.ParseStatus != "succeeded" || state.ChunkStatus != "succeeded" || state.ParseArtifactRef == "" {
		t.Fatalf("indexing must preserve native artifacts: %+v", state)
	}
	service, err := NewDocumentService(DocumentServiceDeps{DB: db.DB, LazyDB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	remote := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "remote-task", DocID: document.ID, KbID: ds.ID, Status: "RUNNING"}
	if err := db.Create(&remote).Error; err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ remote, index string }{
		{"RUNNING", "running"}, {"FAILED", "failed"}, {"SUCCESS", "succeeded"},
	} {
		if err := db.Model(&remote).Where("task_id = ?", remote.TaskID).Update("status", tt.remote).Error; err != nil {
			t.Fatal(err)
		}
		read, err := service.GetDocument(context.Background(), DocumentReadRequest{
			UserID: "u1", DatasetID: ds.ID, DocumentID: document.ID, IncludeContent: true, IncludeChunks: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if read.Metadata.ProcessingState.ParseStatus != "succeeded" || read.Metadata.ProcessingState.ChunkStatus != "succeeded" || read.Metadata.ProcessingState.IndexStatus != tt.index {
			t.Fatalf("%s indexing overwrote native stages: %+v", tt.remote, read.Metadata)
		}
		if read.Content == nil || read.Content.Text != "saved text" || read.Chunks == nil || len(read.Chunks.Chunks) != 1 || read.Chunks.Chunks[0].Text != "saved text" {
			t.Fatalf("%s indexing lost native content: %+v", tt.remote, read)
		}
	}
	if err := db.Model(&remote).Where("task_id = ?", remote.TaskID).Update("status", "FAILED").Error; err != nil {
		t.Fatal(err)
	}
	syncProcessingTaskStates(req, ds.ID)
	db.Where("document_id = ?", document.ID).Take(&state)
	if state.IndexStatus != "failed" || state.ParseStatus != "succeeded" || state.ChunkStatus != "succeeded" ||
		!useBaseChunks(context.Background(), ds.ID, document.ID) {
		t.Fatalf("embedding failure must preserve base availability: %+v", state)
	}
	read, err := service.GetDocument(context.Background(), DocumentReadRequest{
		UserID: "u1", DatasetID: ds.ID, DocumentID: document.ID,
	})
	if err != nil || read.Metadata.ProcessingState.ParseStatus != "succeeded" || read.Metadata.ProcessingState.ChunkStatus != "succeeded" || read.Metadata.ProcessingState.IndexStatus != "failed" {
		t.Fatalf("document reader lost native availability: %+v err=%v", read.Metadata, err)
	}
	parsed, err := service.EnsureDocumentParsed(req, EnsureDocumentParsedRequest{
		UserID: "u1", DatasetID: ds.ID, DocumentID: document.ID,
	})
	if err != nil || parsed.Status != "parsed" || prepares != 1 || adds != 1 {
		t.Fatalf("existing native text was not reused: %+v prepares=%d adds=%d err=%v", parsed, prepares, adds, err)
	}
	if err := db.Model(&remote).Where("task_id = ?", remote.TaskID).Update("status", "SUCCESS").Error; err != nil {
		t.Fatal(err)
	}
	syncProcessingTaskStates(req, ds.ID)
	db.Where("document_id = ?", document.ID).Take(&state)
	if state.IndexStatus != "succeeded" || useBaseChunks(context.Background(), ds.ID, document.ID) {
		t.Fatalf("only completed indexing should switch chunk source: %+v", state)
	}
	indexArtifactRef := state.IndexArtifactRef
	db.Model(&state).Updates(map[string]any{"index_status": "pending", "index_artifact_ref": ""})
	syncProcessingTaskStates(req, ds.ID)
	db.Where("document_id = ?", document.ID).Take(&state)
	if state.IndexStatus != "pending" {
		t.Fatal("an old successful task must not complete a newly prepared artifact")
	}
	for _, status := range []string{"SUCCESS", "FAILED"} {
		if err := db.Model(&remote).Where("task_id = ?", remote.TaskID).Update("status", status).Error; err != nil {
			t.Fatal(err)
		}
		metadata, err := service.GetDocumentMetadata(context.Background(), DocumentGetRequest{
			UserID: "u1", DatasetID: ds.ID, DocumentID: document.ID,
		})
		if err != nil || metadata.ProcessingState.ParseStatus != "succeeded" || metadata.ProcessingState.ChunkStatus != "succeeded" || metadata.ProcessingState.IndexStatus != "unknown" || metadata.ProcessingError != nil {
			t.Fatalf("stale %s task affected new native artifact: %+v err=%v", status, metadata, err)
		}
	}
	// Even a matching task binding cannot claim indexing for a new revision.
	if err := db.Model(&state).Update("index_artifact_ref", indexArtifactRef).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ds).Update("processing_revision", ds.ProcessingRevision+1).Error; err != nil {
		t.Fatal(err)
	}
	metadata, err := service.GetDocumentMetadata(context.Background(), DocumentGetRequest{
		UserID: "u1", DatasetID: ds.ID, DocumentID: document.ID,
	})
	if err != nil || metadata.ProcessingState.ParseStatus != "succeeded" || metadata.ProcessingState.ChunkStatus != "succeeded" || metadata.ProcessingState.IndexStatus != "unknown" || metadata.ProcessingError != nil {
		t.Fatalf("stale processing revision affected native artifact: %+v err=%v", metadata, err)
	}
}
