package doc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

func TestEnsureDatasetRAG(t *testing.T) {
	for _, status := range []int{200, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			posts := 0
			previous := http.DefaultTransport
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v1/algo/list" {
					return testJSONResponse(200, `{"code":200,"data":{"items":[{"algo_id":"general_algo"}]}}`), nil
				}
				if r.Method == http.MethodGet {
					return testJSONResponse(status, `{}`), nil
				}
				posts++
				var body kbCreateRequest
				json.NewDecoder(r.Body).Decode(&body)
				if body.KbID != "retained-kb" || body.OwnerID != "u1" || body.AlgoID != "general_algo" {
					t.Fatalf("wrong provisioning identity: %+v", body)
				}
				return testJSONResponse(200, `{}`), nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			err := ensureDatasetRAG(httptest.NewRequest("POST", "/", nil), &orm.Dataset{ID: "retained-kb", BaseModel: orm.BaseModel{CreateUserID: "u1"}})
			if (err != nil) != (status == 503) || (posts == 1) != (status == 404) {
				t.Fatalf("status=%d posts=%d err=%v", status, posts, err)
			}
		})
	}
}

func TestRetainedFileIngestionTask(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db := newDocumentTestDB(t)
	ds := orm.Dataset{ID: "kb-ingest", ProcessingLevel: ProcessingLevelIndexed}
	document := orm.Document{ID: "stored-file", DatasetID: ds.ID, DisplayName: "paper.md", Ext: mustJSON(documentExt{StoredPath: "/tmp/paper.md"})}
	for _, row := range []any{&ds, &document} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	item := CreateTaskItem{Task: TaskPayload{DocumentID: document.ID}}
	if err := validateCreateTaskItem(item, ds.ID, string(TaskTypeParseUploaded)); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/", nil)
	task, err := createTaskFromExistingDocument(req, ds.ID, "u1", "User", item, string(TaskTypeParseUploaded))
	if err != nil {
		t.Fatal(err)
	}
	var ext taskExt
	json.Unmarshal(task.Ext, &ext)
	if task.DocID != document.ID || ext.TaskState != string(TaskStateCreating) || len(ext.Files) != 1 || ext.Files[0].StoredPath != "/tmp/paper.md" {
		t.Fatalf("wrong ingestion task: %+v %+v", task, ext)
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicated retained document: %d", count)
	}
	db.Model(&ds).Update("processing_level", ProcessingLevelStored)
	if _, err := createTaskFromExistingDocument(req, ds.ID, "u1", "User", item, string(TaskTypeParseUploaded)); err == nil {
		t.Fatal("storage-only mode must not claim ingestion")
	}
	db.Model(&ds).Update("processing_level", ProcessingLevelIndexed)
	t.Setenv("LAZYMIND_RAG_ENABLED", "false")
	if _, err := createTaskFromExistingDocument(req, ds.ID, "u1", "User", item, string(TaskTypeParseUploaded)); err != nil {
		t.Fatalf("native parsing should be available without RAG: %v", err)
	}
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db.Model(&document).Update("lazyllm_doc_id", document.ID)
	if _, err := createTaskFromExistingDocument(req, ds.ID, "u1", "User", item, string(TaskTypeParseUploaded)); err == nil {
		t.Fatal("registered file must use reparse")
	}
	reparse, err := createTaskFromExistingDocument(req, ds.ID, "u1", "User", item, string(TaskTypeReparse))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(reparse.Ext, &ext)
	if ext.TaskState != string(TaskStateCreating) {
		t.Fatal("new reparse must not inherit old success")
	}
}

func TestProcessingCoverageUsesSubmittedLevel(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{ProcessingLevelParsed, ProcessingLevelIndexed} {
		ds := orm.Dataset{ID: level, KbID: level, ProcessingLevel: ProcessingLevelIndexed}
		doc := orm.Document{ID: level, DatasetID: level, LazyllmDocID: level}
		task := orm.Task{ID: level, DatasetID: level, DocID: level, LazyllmTaskID: level, Ext: mustJSON(taskExt{ProcessingLevel: level})}
		remote := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: level, DocID: level, KbID: level, Status: "SUCCESS"}
		state := newDocumentProcessingState(level, level, time.Now())
		for _, row := range []any{&ds, &doc, &task, &remote, &state} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		syncProcessingTaskStates(httptest.NewRequest("GET", "/", nil), level)
		db.Where("document_id = ?", level).Take(&state)
		if state.ParseStatus != "succeeded" || (state.IndexStatus == "succeeded") != (level == ProcessingLevelIndexed) {
			t.Fatalf("level %s state %+v", level, state)
		}
	}
}

func TestRetainedFileReparseUsesAdd(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db := newDocumentTestDB(t)
	ds := orm.Dataset{ID: "retained-kb", KbID: "retained-kb", ProcessingLevel: ProcessingLevelParsed}
	doc := orm.Document{ID: "retained-doc", DatasetID: ds.ID, DisplayName: "paper.md", Ext: mustJSON(documentExt{StoredPath: "/tmp/paper.md"})}
	task := orm.Task{ID: "retained-task", DatasetID: ds.ID, DocID: doc.ID, TaskType: string(TaskTypeReparse), Ext: mustJSON(taskExt{TaskState: string(TaskStateFailed), ErrorMessage: "old rejection"})}
	for _, row := range []any{&ds, &doc, &task} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	added := false
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/docs/add" {
			t.Fatalf("unexpected remote call: %s", r.URL.Path)
		}
		added = true
		return testJSONResponse(200, `{"items":[{"doc_id":"remote-doc","task_id":"remote-task","core_task_id":"retained-task","core_document_id":"retained-doc"}]}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-User-Id", "u1")
	results, _ := startReparseTasksInternal(r, ds.ID, []string{task.ID})
	if !added || len(results) != 1 || results[0].Status != "STARTED" {
		t.Fatalf("results=%+v added=%v", results, added)
	}
	db.First(&task, "id = ?", task.ID)
	var ext taskExt
	json.Unmarshal(task.Ext, &ext)
	if ext.TaskState != string(TaskStateRunning) || ext.ErrorMessage != "" || ext.ProcessingLevel != ProcessingLevelParsed {
		t.Fatalf("stale state: %+v", ext)
	}
}

func TestStoredReparseReportsRequiredUpgrade(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db := newDocumentTestDB(t)
	ds := orm.Dataset{ID: "stored", ProcessingLevel: ProcessingLevelStored}
	doc := orm.Document{ID: "stored-doc", DatasetID: ds.ID}
	task := orm.Task{ID: "stored-task", DatasetID: ds.ID, DocID: doc.ID, TaskType: string(TaskTypeReparse)}
	for _, row := range []any{&ds, &doc, &task} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-User-Id", "u1")
	results, _ := startReparseTasksInternal(r, ds.ID, []string{task.ID})
	if len(results) != 1 || results[0].Status != "FAILED" || results[0].SubmitStatus != "REJECTED" {
		t.Fatalf("results=%+v", results)
	}
	db.First(&task, "id = ?", task.ID)
	var ext taskExt
	json.Unmarshal(task.Ext, &ext)
	if ext.TaskState != string(TaskStateFailed) || ext.ErrorMessage == "" {
		t.Fatalf("state=%+v", ext)
	}
}
