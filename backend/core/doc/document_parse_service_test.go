package doc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

func TestEnsureDocumentParsedUsesCurrentRootsBeforeHistoricalTaskStatus(t *testing.T) {
	for _, tt := range []struct {
		name       string
		upload     string
		task       string
		roots      string
		wantStatus string
	}{
		{"successful retry after failed task", "SUCCESS", "FAILED", `{"items":[{"chunk_id":"root-1","content":"parsed text"}],"total":1}`, "parsed"},
		{"succeeded alias after failed task", "SUCCEEDED", "FAILED", `{"items":[{"chunk_id":"root-1","content":"parsed text"}],"total":1}`, "parsed"},
		{"successful retry before roots visible", "SUCCESS", "FAILED", `{"items":[],"total":0}`, "parsing"},
		{"successful retry while roots unavailable", "SUCCESS", "FAILED", "", "parsing"},
		{"successful document with unfinished task", "SUCCESS", "WORKING", `{"items":[{"chunk_id":"root-1","content":"parsed text"}],"total":1}`, "parsed"},
		{"current parsing failure", "FAILED", "FAILED", "", "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := newDocumentTestDB(t)
			now := time.Now().UTC()
			seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
			seedDocumentServiceLazyDocument(t, db, "dataset-1", "doc-1", "lazy-doc-1", "user-1")
			if err := db.Create(&readonlyorm.LazyLLMDocRow{
				DocID: "lazy-doc-1", UploadStatus: tt.upload, CreatedAt: now, UpdatedAt: now,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.Task{
				ID: "old-task", DocID: "doc-1", DatasetID: "dataset-1",
				Ext:       mustJSON(taskExt{TaskState: tt.task}),
				BaseModel: orm.BaseModel{CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
			}).Error; err != nil {
				t.Fatal(err)
			}
			state := newDocumentProcessingState("dataset-1", "doc-1", now)
			state.ParseStatus = "failed"
			state.ParseErrorCode, state.ParseErrorMessage = "OLD_FAILURE", "previous attempt failed"
			if err := db.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			if tt.upload != "FAILED" {
				// A successful service-side retry must be proven by its current
				// task, not inferred from the document's old UploadStatus.
				seedCurrentParseSnapshot(t, db, "retry-external", tt.upload, now)
			}
			rootRequests := 0
			installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
				if r.URL.Query().Get("group") != RootNodeGroup || r.URL.Query().Get("doc_id") != "lazy-doc-1" {
					t.Errorf("unexpected root lookup: %s", r.URL)
				}
				rootRequests++
				if tt.roots == "" {
					return http.StatusServiceUnavailable, `{"message":"unavailable"}`
				}
				return http.StatusOK, tt.roots
			})
			t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

			listed, _, err := loadMergedDocumentsBySearch(context.Background(), []string{"dataset-1"}, "", nil, 10, 0)
			if err != nil || len(listed) != 1 || listed[0].DocumentStage != tt.upload {
				t.Fatalf("document list did not expose current service status: %+v, %v", listed, err)
			}
			result, err := mustDocumentService(t, db).EnsureDocumentParsed(
				httptest.NewRequest(http.MethodPost, "/", nil),
				EnsureDocumentParsedRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1"},
			)
			if err != nil || result.Status != tt.wantStatus {
				t.Fatalf("EnsureDocumentParsed = %+v, %v; want %s", result, err, tt.wantStatus)
			}
			if result.Status != "failed" && result.Error != nil {
				t.Fatalf("historical error leaked into current parse result: %+v", result)
			}
			if tt.upload == "FAILED" && rootRequests != 0 {
				t.Fatal("failed current document should not query roots")
			}
			if err := db.Where("dataset_id = ? AND document_id = ?", "dataset-1", "doc-1").Take(&state).Error; err != nil {
				t.Fatal(err)
			}
			if result.Status == "parsed" && (state.ParseStatus != "succeeded" || state.ParseErrorCode != "" || state.ParseErrorMessage != "") {
				t.Fatalf("successful roots did not clear the persisted parse failure: %+v", state)
			}
			var taskCount int64
			if err := db.Model(&orm.Task{}).Count(&taskCount).Error; err != nil || taskCount != 1 {
				t.Fatalf("status check created a retry task: count=%d, err=%v", taskCount, err)
			}
		})
	}
}

func seedCurrentParseSnapshot(t *testing.T, db *orm.DB, taskID, status string, createdAt time.Time) {
	t.Helper()
	code, message := "READER_FAILED", "current reader failed"
	for _, row := range []any{
		&readonlyorm.LazyLLMDocParseStateRow{
			DocID: "lazy-doc-1", KbID: "kb-dataset-1", CurrentTaskID: taskID,
			Status: status, LastErrorCode: &code, LastErrorMsg: &message,
		},
		&readonlyorm.LazyLLMDocServiceTaskRow{
			TaskID: taskID, DocID: "lazy-doc-1", KbID: "kb-dataset-1", Status: status,
			ErrorCode: &code, ErrorMsg: &message, CreatedAt: createdAt, UpdatedAt: createdAt,
		},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnsureDocumentParsedChecksCurrentTaskWhenRootsUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name          string
		currentTask   string
		currentStatus string
		roots         string
		wantStatus    string
		wantTask      string
		localSubmit   bool
		dropSnapshot  bool
	}{
		{name: "current reparse failed with empty roots", currentTask: "core-external", currentStatus: "FAILED", roots: `{"items":[],"total":0}`, wantStatus: "failed", wantTask: "core-task"},
		{name: "current reparse failed with root query error", currentTask: "core-external", currentStatus: "FAILED", wantStatus: "failed", wantTask: "core-task"},
		{name: "usable roots still win over failure", currentTask: "core-external", currentStatus: "FAILED", roots: `{"items":[{"chunk_id":"root-1","content":"text"}],"total":1}`, wantStatus: "parsed"},
		{name: "service retry succeeded before roots visible", currentTask: "retry-external", currentStatus: "SUCCESS", roots: `{"items":[],"total":0}`, wantStatus: "parsing", wantTask: "retry-external"},
		{name: "service retry succeeded but root query unavailable", currentTask: "retry-external", currentStatus: "SUCCEEDED", wantStatus: "parsing", wantTask: "retry-external"},
		{name: "service retry still running", currentTask: "retry-external", currentStatus: "WORKING", wantStatus: "parsing", wantTask: "retry-external"},
		{name: "service retry queued", currentTask: "retry-external", currentStatus: "WAITING", wantStatus: "parsing", wantTask: "retry-external"},
		{name: "service retry failed", currentTask: "retry-external", currentStatus: "FAILED", wantStatus: "failed", wantTask: "retry-external"},
		{name: "service retry canceled", currentTask: "retry-external", currentStatus: "CANCELED", wantStatus: "failed", wantTask: "retry-external"},
		{name: "unknown snapshot is not success", currentTask: "retry-external", currentStatus: "UNKNOWN", wantStatus: "unknown", wantTask: "retry-external"},
		{name: "missing snapshot preserves current failure", wantStatus: "failed", wantTask: "core-task"},
		{name: "old snapshot does not hide newer external failure", currentTask: "previous-external", currentStatus: "SUCCESS", wantStatus: "failed", wantTask: "core-task"},
		{name: "old snapshot does not hide newer submission failure", currentTask: "previous-external", currentStatus: "SUCCESS", localSubmit: true, wantStatus: "failed", wantTask: "core-task"},
		{name: "snapshot query error is surfaced", dropSnapshot: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := newDocumentTestDB(t)
			now := time.Now().UTC()
			seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
			seedDocumentServiceLazyDocument(t, db, "dataset-1", "doc-1", "lazy-doc-1", "user-1")
			if err := db.Create(&readonlyorm.LazyLLMDocRow{DocID: "lazy-doc-1", UploadStatus: "SUCCESS", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}).Error; err != nil {
				t.Fatal(err)
			}
			lazyTaskID := "core-external"
			if tt.localSubmit {
				lazyTaskID = ""
			}
			if err := db.Create(&orm.Task{
				ID: "core-task", DatasetID: "dataset-1", DocID: "doc-1", LazyllmTaskID: lazyTaskID,
				TaskType: string(TaskTypeReparse), Ext: mustJSON(taskExt{TaskState: "FAILED", ProcessingLevel: ProcessingLevelParsed, ErrorMessage: "submission failed"}),
				BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now},
			}).Error; err != nil {
				t.Fatal(err)
			}
			state := newDocumentProcessingState("dataset-1", "doc-1", now)
			state.ParseStatus = "running"
			if err := db.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			if tt.currentTask != "" {
				createdAt := now.Add(time.Minute)
				if tt.currentTask == "previous-external" {
					createdAt = now.Add(-time.Hour)
				}
				seedCurrentParseSnapshot(t, db, tt.currentTask, tt.currentStatus, createdAt)
			}
			if tt.currentTask != "core-external" {
				if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "core-external", DocID: "lazy-doc-1", KbID: "kb-dataset-1", Status: "FAILED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Other documents and libraries must not supply the current status.
			for _, row := range []readonlyorm.LazyLLMDocParseStateRow{
				{DocID: "other-doc", KbID: "kb-dataset-1", CurrentTaskID: "unrelated", Status: "SUCCESS"},
				{DocID: "lazy-doc-1", KbID: "other-kb", CurrentTaskID: "unrelated", Status: "SUCCESS"},
			} {
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tt.dropSnapshot {
				if err := db.Migrator().DropTable(&readonlyorm.LazyLLMDocParseStateRow{}); err != nil {
					t.Fatal(err)
				}
			}
			installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
				if tt.roots == "" {
					return http.StatusServiceUnavailable, `{"message":"unavailable"}`
				}
				return http.StatusOK, tt.roots
			})
			t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
			result, err := mustDocumentService(t, db).EnsureDocumentParsed(httptest.NewRequest(http.MethodPost, "/", nil), EnsureDocumentParsedRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1"})
			if tt.dropSnapshot {
				if documentServiceCode(err) != DocumentServiceUnavailable {
					t.Fatalf("snapshot query error = %v, want unavailable", err)
				}
				return
			}
			if err != nil || result.Status != tt.wantStatus || result.TaskID != tt.wantTask {
				t.Fatalf("EnsureDocumentParsed = %+v, %v; want %s / %s", result, err, tt.wantStatus, tt.wantTask)
			}
			if tt.wantStatus == "failed" {
				if result.Error == nil || result.Error.TaskID != tt.wantTask {
					t.Fatalf("missing current failure diagnostic: %+v", result)
				}
				if (tt.currentTask == "core-external" || tt.currentTask == "retry-external") && (result.Error.Code != "READER_FAILED" || result.Error.Message != "current reader failed") {
					t.Fatalf("wrong failure details: %+v", result.Error)
				}
			} else if result.Error != nil {
				t.Fatalf("stale failure leaked: %+v", result.Error)
			}
			if err := db.Where("dataset_id = ? AND document_id = ?", "dataset-1", "doc-1").Take(&state).Error; err != nil {
				t.Fatal(err)
			}
			if result.Status != "parsed" && state.ParseStatus != "running" {
				t.Fatalf("status observation changed persisted parse state: %+v", state)
			}
			var taskCount int64
			if err := db.Model(&orm.Task{}).Count(&taskCount).Error; err != nil || taskCount != 1 {
				t.Fatalf("status check created a task: count=%d, err=%v", taskCount, err)
			}
		})
	}
}

func TestWaitForDocumentParsedJoinsUntilParsed(t *testing.T) {
	statuses := []string{"parsing", "parsing", "parsed"}
	calls := 0
	err := waitForDocumentParsed(context.Background(), time.Second, time.Millisecond, func() (EnsureDocumentParsedResult, error) {
		status := statuses[calls]
		calls++
		return EnsureDocumentParsedResult{Status: status, TaskID: "shared-task"}, nil
	})
	if err != nil {
		t.Fatalf("waitForDocumentParsed: %v", err)
	}
	if calls != 3 {
		t.Fatalf("ensure calls = %d, want 3", calls)
	}
}

func TestWaitForDocumentParsedReturnsParseFailure(t *testing.T) {
	want := &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "reader crashed"}
	err := waitForDocumentParsed(context.Background(), time.Second, time.Millisecond, func() (EnsureDocumentParsedResult, error) {
		return EnsureDocumentParsedResult{Status: "failed"}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestWaitForDocumentParsedTimesOut(t *testing.T) {
	err := waitForDocumentParsed(context.Background(), 5*time.Millisecond, time.Millisecond, func() (EnsureDocumentParsedResult, error) {
		return EnsureDocumentParsedResult{Status: "parsing", TaskID: "shared-task"}, nil
	})
	var serviceErr *DocumentServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Code != DocumentServiceUnavailable {
		t.Fatalf("error = %v, want unavailable DocumentServiceError", err)
	}
}

func TestFailedParseStatusIsRetryable(t *testing.T) {
	for _, status := range []string{"FAILED", "ERROR"} {
		if !isRetryableParseObservation(status, "") {
			t.Fatalf("upload status %q should be retryable", status)
		}
	}
	for _, taskStatus := range []string{"FAILED", "ERROR"} {
		if !isRetryableParseObservation("WAITING", taskStatus) {
			t.Fatalf("task status %q should be retryable", taskStatus)
		}
	}
	if isRetryableParseObservation("SUCCESS", "FAILED") {
		t.Fatal("successful document artifact must win over a stale failed Core task")
	}
}
