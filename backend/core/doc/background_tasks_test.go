package doc

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProcessingTasksLatestAttemptAndOwnership(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "mine", "user-1")
	seedDocumentListDataset(t, db, "private", "other")
	now := time.Now().UTC()
	for _, d := range []orm.Document{{ID: "native", DatasetID: "mine", DisplayName: "native.pdf"}, {ID: "remote", DatasetID: "mine", DisplayName: "remote.pdf", LazyllmDocID: "external-doc"}, {ID: "gone", DatasetID: "mine", BaseModel: orm.BaseModel{DeletedAt: &now}}, {ID: "secret", DatasetID: "private"}} {
		if err := db.Create(&d).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []orm.Task{
		{ID: "old", DocID: "native", DatasetID: "mine", Ext: json.RawMessage(`{"task_state":"FAILED","error_message":"old failure","processing_level":"parsed"}`), BaseModel: orm.BaseModel{CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(time.Hour)}},
		{ID: "new", DocID: "native", DatasetID: "mine", Ext: json.RawMessage(`{"task_state":"SUCCEEDED","processing_level":"parsed"}`), BaseModel: orm.BaseModel{CreatedAt: now}},
		{ID: "remote", DocID: "remote", DatasetID: "mine", LazyllmTaskID: "external-task", Ext: json.RawMessage(`{"processing_level":"indexed"}`)},
		{ID: "gone", DocID: "gone", DatasetID: "mine", Ext: json.RawMessage(`{"task_state":"RUNNING"}`)},
		{ID: "secret", DocID: "secret", DatasetID: "private", Ext: json.RawMessage(`{"task_state":"RUNNING"}`)},
	} {
		task.TaskType = string(TaskTypeParseUploaded)
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "external-task", DocID: "external-doc", KbID: "kb-mine", Status: "RUNNING"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "external-task", DocID: "unrelated", KbID: "kb-mine", Status: "FAILED"}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/knowledge-processing/tasks", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListProcessingTasks(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var result ProcessingTasksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || result.Counts["succeeded"] != 1 || result.Counts["active"] != 1 || result.UnfinishedCount != 1 {
		t.Fatalf("unexpected tasks: %+v", result)
	}
	for _, item := range result.Items {
		if item.ID == "new" && (item.Error != "" || item.Stages[1].Status != "not_requested" || item.Stages[2].Status != "not_requested") {
			t.Fatalf("unrequested stages counted: %+v", item)
		}
	}
}
func TestProcessingTaskFiltersAndPagination(t *testing.T) {
	items := []ProcessingTaskItem{}
	for i := 0; i < 25; i++ {
		items = append(items, ProcessingTaskItem{ID: time.Unix(int64(i), 0).String(), DocumentName: "paper", Level: "indexed", Status: "failed", CreatedAt: time.Unix(int64(i), 0)})
	}
	items = append(items, ProcessingTaskItem{DocumentName: "other", Level: "parsed", Status: "running"})
	result := filterProcessingTasks(items, "failed", "indexed", "PAPER", "2")
	if result.Total != 25 || len(result.Items) != 5 || result.Page != 2 || result.Counts["failed"] != 25 {
		t.Fatalf("bad pagination %+v", result)
	}
	if processingTaskStatus("") != "unknown" || processingTaskStatus("CANCELLED") != "canceled" {
		t.Fatal("unconfirmed or canceled work must not be active")
	}
}

func TestProcessingTasksStoredLibraryAndMissingWorkerEvidence(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "mine", "user-1")
	db.Model(&orm.Dataset{}).Where("id = ?", "mine").Update("processing_level", "stored")
	doc := orm.Document{ID: "doc", DatasetID: "mine", LazyllmDocID: "remote-doc"}
	if err := db.Create(&doc).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.Task{ID: "task", DocID: "doc", DatasetID: "mine", TaskType: string(TaskTypeReparse), LazyllmTaskID: "missing-worker", Ext: json.RawMessage(`{"task_state":"RUNNING","processing_level":"indexed"}`)}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/knowledge-processing/tasks", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListProcessingTasks(rec, req)
	var result ProcessingTasksResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if rec.Code != 200 || result.Total != 1 || result.Items[0].Status != "unknown" || result.UnfinishedCount != 0 {
		t.Fatalf("missing worker evidence must not imply running: %d %+v", rec.Code, result)
	}
}
func TestOriginalOnlyReceiptIsNotAParseTask(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "paper", "user-1")
	db.Model(&orm.Dataset{}).Where("id = ?", "paper").Updates(map[string]any{"scene": "academic_papers", "processing_level": "stored"})
	if err := db.Create(&orm.Document{ID: "doc", DatasetID: "paper"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Task{ID: "receipt", DocID: "doc", DatasetID: "paper", TaskType: string(TaskTypeParseUploaded), Ext: json.RawMessage(`{"task_state":"SUCCEEDED"}`)}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/knowledge-processing/tasks", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListProcessingTasks(rec, req)
	var result ProcessingTasksResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if rec.Code != 200 || result.Total != 0 {
		t.Fatalf("upload receipt is not parsed: %d %+v", rec.Code, result)
	}
}

func TestAggregateFailureDoesNotFailEveryStage(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "mine", "user-1")
	if err := db.Create(&orm.Document{ID: "doc", DatasetID: "mine", LazyllmDocID: "remote-doc"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Task{ID: "task", DocID: "doc", DatasetID: "mine", TaskType: string(TaskTypeReparse), LazyllmTaskID: "worker", Ext: json.RawMessage(`{"processing_level":"indexed","processing_revision":1}`)}).Error; err != nil {
		t.Fatal(err)
	}
	msg := "parser failed"
	if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "worker", DocID: "remote-doc", KbID: "kb-mine", Status: "FAILED", ErrorMsg: &msg}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/knowledge-processing/tasks", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListProcessingTasks(rec, req)
	var result ProcessingTasksResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if rec.Code != 200 || len(result.Items) != 1 || result.Items[0].Status != "failed" {
		t.Fatalf("bad failure response %s", rec.Body.String())
	}
	for _, stage := range result.Items[0].Stages {
		if stage.Status == "failed" {
			t.Fatalf("aggregate failure incorrectly attributed to %+v", stage)
		}
	}
}
