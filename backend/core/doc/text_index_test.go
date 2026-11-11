package doc

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestTextIndexInventoryUsesDocumentEvidenceAndPermissions(t *testing.T) {
	db := libraryKindTestDB(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []any{
		&orm.Dataset{ID: "ds-kind", Scene: "academic_papers", ProcessingLevel: "parsed", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.Document{ID: "paper", DatasetID: "ds-kind", DisplayName: "Paper.pdf"},
		&orm.Document{ID: "folder", DatasetID: "ds-kind", DocumentType: "FOLDER"},
		&orm.DocumentProcessingState{DatasetID: "ds-kind", DocumentID: "paper", ParseStatus: "succeeded", IndexStatus: "succeeded"},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := libraryKindRequest(t, GetTextIndex, "GET", "/api/core/datasets/ds-kind/text-index", "", "owner")
	if w.Code != 200 {
		t.Fatalf("inventory: %d %s", w.Code, w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	documents := raw["documents"].([]any)
	if len(documents) != 1 || documents[0].(map[string]any)["index_status"] != "succeeded" {
		t.Fatal(raw)
	}
	w = libraryKindRequest(t, GetTextIndex, "GET", "/api/core/datasets/ds-kind/text-index", "", "stranger")
	if w.Code != 403 {
		t.Fatalf("cross-owner inventory exposed: %d %s", w.Code, w.Body.String())
	}
}

func TestPaperKnowledgeDefaultsToParsedWithoutModel(t *testing.T) {
	libraryKindTestDB(t)
	w := libraryKindRequest(t, CreateDataset, "POST", "/datasets?dataset_id=ds-kind", `{"display_name":"Papers","scene":"academic_papers","tags":["test"]}`, "owner")
	if w.Code != 200 || libraryKindResponse(t, w)["processing_level"] != "parsed" {
		t.Fatalf("paper default: %d %s", w.Code, w.Body.String())
	}
}

func TestIndexedKnowledgeUploadDoesNotProbeUnavailableEmbedding(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "1")
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	db := libraryKindTestDB(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Dataset{ID: "ds-kind", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{UploadFile, BatchUploadTasks} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		file, err := form.CreateFormFile("files", "paper.pdf")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("%PDF-1.4\nretained original\n%%EOF"))
		_ = form.Close()
		r := httptest.NewRequest("POST", "/datasets/ds-kind/upload", &body)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("X-User-Id", "owner")
		r = mux.SetURLVars(r, map[string]string{"dataset": "ds-kind"})
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 200 {
			t.Fatalf("upload blocked by missing model: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestVectorCleanupPreservesObservedParsing(t *testing.T) {
	document := orm.Document{ID: "paper", DatasetID: "library"}
	snapshot := processingSnapshot{
		states: map[string]orm.DocumentProcessingState{"paper": {ParseStatus: "succeeded", ChunkStatus: "succeeded", IndexStatus: "pending", EmbeddingVersion: embeddingVersionReembedManual}},
		tasks:  map[string]orm.Task{"paper": {ID: "old-index-task"}},
	}
	state, _ := snapshot.observe(document)
	if state.ParseStatus != "succeeded" || state.ChunkStatus != "succeeded" || state.IndexStatus != "pending" {
		t.Fatalf("vector cleanup invalidated parsed content: %+v", state)
	}
}
