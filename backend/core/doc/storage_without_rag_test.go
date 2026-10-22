package doc

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

func TestKnowledgeStorageWithoutRAG(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "0")
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.DefaultDataset{}, &orm.ACLModel{}, &orm.KBModel{}, &orm.VisibilityModel{}, &orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	acl.InitStore(db)
	installOriginalSourceTransport(t)
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/scan/internal/source-access/by-dataset:batch":
			return previous.RoundTrip(r)
		case "/api/scan/internal/sources/by-datasets":
			return testJSONResponse(200, `{"source_map":{}}`), nil
		case "/api/authservice/user/storage-user/groups/internal":
			return testJSONResponse(200, `{"groups":[]}`), nil
		}
		t.Errorf("storage must not call RAG services: %s", r.URL.Path)
		return testJSONResponse(503, "{}"), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	request := func(method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-User-Id", "storage-user")
		r.Header.Set("X-User-Name", "Storage Test")
		return mux.SetURLVars(r, map[string]string{"dataset": "storage-test"})
	}
	rec := httptest.NewRecorder()
	GetKnowledgeOptions(rec, request("GET", "/knowledge/options", ""))
	var options struct {
		Algorithms   []Algo `json:"algorithms"`
		DefaultLevel string `json:"default_processing_level"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &options); err != nil || rec.Code != 200 || len(options.Algorithms) != 0 || options.DefaultLevel != ProcessingLevelIndexed {
		t.Fatalf("native creation options: %d %s err=%v", rec.Code, rec.Body.String(), err)
	}
	rec = httptest.NewRecorder()
	CreateDataset(rec, request("POST", "/datasets?dataset_id=deferred-test", `{"display_name":"DeferredTest","tags":["test"],"processing_level":"indexed"}`))
	var deferred Dataset
	decodeRecorderJSON(t, rec, &deferred)
	if rec.Code != 200 || deferred.ProcessingLevel != ProcessingLevelIndexed {
		t.Fatalf("deferred creation: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	CreateDataset(rec, request("POST", "/datasets?dataset_id=storage-test", `{"display_name":"StorageTest","tags":["test"],"processing_level":"stored","algo":{"algo_id":"general_algo"}}`))
	if rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var ds orm.Dataset
	if err := db.First(&ds, "id = ?", "storage-test").Error; err != nil {
		t.Fatal(err)
	}
	if ds.ProcessingLevel != ProcessingLevelStored {
		t.Fatalf("level=%s", ds.ProcessingLevel)
	}
	// Existing indexed databases must also accept storage without an embedding model.
	indexed := ds
	indexed.ProcessingLevel = ProcessingLevelIndexed
	if datasetRequiresEmbedding(&indexed) {
		t.Fatal("storage unexpectedly requires embedding")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "paper.pdf")
	if err != nil {
		t.Fatal(err)
	}
	original := "%PDF-1.4\nsynthetic storage fixture, do not parse\n%%EOF"
	if _, err = part.Write([]byte(original)); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	r := httptest.NewRequest("POST", "/datasets/storage-test/tasks:batchUpload", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-User-Id", "storage-user")
	r = mux.SetURLVars(r, map[string]string{"dataset": "storage-test"})
	rec = httptest.NewRecorder()
	BatchUploadTasks(rec, r)
	if rec.Code != 200 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var tasks []orm.Task
	if err := db.Where("dataset_id = ?", ds.ID).Find(&tasks).Error; err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
	results, err := startTasksInternal(request("POST", "/", ""), ds.ID, []string{tasks[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "STARTED" {
		t.Fatalf("start=%v %v", results, err)
	}
	var document orm.Document
	if err := db.First(&document, "id = ?", tasks[0].DocID).Error; err != nil {
		t.Fatal(err)
	}
	if document.LazyllmDocID != "" {
		t.Fatal("document submitted to RAG")
	}
	rec = httptest.NewRecorder()
	SearchDocuments(rec, request("POST", "/datasets/storage-test/documents:search", `{ "page_size": 20 }`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), document.ID) {
		t.Fatalf("stored document missing from frontend listing: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"retained_only":true`) {
		t.Fatalf("stored document must expose first-ingestion action: %s", rec.Body.String())
	}
	var state orm.DocumentProcessingState
	if err := db.First(&state, "document_id = ?", document.ID).Error; err != nil {
		t.Fatal(err)
	}
	if state.ParseStatus != "pending" || state.ChunkStatus != "pending" || state.IndexStatus != "pending" {
		t.Fatalf("unexpected processing: %+v", state)
	}
	r = request("GET", "/datasets/storage-test/documents/"+document.ID+":download", "")
	r = mux.SetURLVars(r, map[string]string{"dataset": ds.ID, "document": document.ID})
	rec = httptest.NewRecorder()
	DownloadDocument(rec, r)
	if rec.Code != 200 || rec.Body.String() != original {
		t.Fatalf("download: %d %s", rec.Code, rec.Body.String())
	}
	var ext taskExt
	db.First(&tasks[0], "id = ?", tasks[0].ID)
	json.Unmarshal(tasks[0].Ext, &ext)
	if ext.TaskState != string(TaskStateSucceeded) {
		t.Fatalf("storage task state=%s", ext.TaskState)
	}
	if err := db.Model(&tasks[0]).Update("task_type", string(TaskTypeReparse)).Error; err != nil {
		t.Fatal(err)
	}
	results, err = startTasksInternal(request("POST", "/", ""), ds.ID, []string{tasks[0].ID})
	if err == nil || len(results) != 1 || results[0].SubmitStatus != "REJECTED" {
		t.Fatalf("reparse must be rejected without RAG: %v %v", results, err)
	}
	db.First(&tasks[0], "id = ?", tasks[0].ID)
	json.Unmarshal(tasks[0].Ext, &ext)
	if ext.TaskState != string(TaskStateFailed) || ext.ErrorMessage == "" {
		t.Fatalf("rejected task must persist terminal failure: %+v", ext)
	}
}
