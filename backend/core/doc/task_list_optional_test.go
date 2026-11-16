package doc

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"gorm.io/gorm/logger"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

func TestListTasksWithoutRAGIncludesLocalStates(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "dataset-1", "user-1")
	if err := db.Migrator().DropTable(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"UPLOADING", "RUNNING", "SUCCEEDED", "FAILED"} {
		row := orm.Task{ID: state, DatasetID: "dataset-1", Ext: mustJSON(taskExt{TaskState: state})}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	db.Logger = logger.New(log.New(&logs, "", 0), logger.Config{LogLevel: logger.Error, IgnoreRecordNotFoundError: true})
	for _, tc := range []struct {
		filter string
		total  int32
	}{{"running", 2}, {"success", 1}, {"failed", 1}} {
		t.Run(tc.filter, func(t *testing.T) {
			logs.Reset()
			req := httptest.NewRequest(http.MethodGet, "/api/core/datasets/dataset-1/tasks?task_state="+tc.filter+"&page_size=1", nil)
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{"dataset": "dataset-1"})
			rec := httptest.NewRecorder()
			ListTasks(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var got ListTasksResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.TotalSize != tc.total || len(got.Tasks) != 1 {
				t.Errorf("filter %s: total=%d tasks=%d want total=%d, one page item", tc.filter, got.TotalSize, len(got.Tasks), tc.total)
			}
			if len(got.Tasks) == 1 {
				id := got.Tasks[0].TaskID
				if (tc.filter == "running" && id != "UPLOADING" && id != "RUNNING") || (tc.filter == "success" && id != "SUCCEEDED") || (tc.filter == "failed" && id != "FAILED") {
					t.Errorf("filter %s returned task %s", tc.filter, id)
				}
			}
			if tc.total > 1 && got.NextPageToken == "" {
				t.Error("missing next page token")
			}
			if strings.Contains(logs.String(), "lazyllm_doc_service_tasks") {
				t.Errorf("optional table triggered SQL error: %s", logs.String())
			}
		})
	}
}

func TestListTasksReturnsErrorForBrokenRAGTaskTable(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "dataset-1", "user-1")
	if err := db.Migrator().DropColumn(&readonlyorm.LazyLLMDocServiceTaskRow{}, "status"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/core/datasets/dataset-1/tasks?task_state=running", nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"dataset": "dataset-1"})
	rec := httptest.NewRecorder()
	ListTasks(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken existing RAG table returned %d: %s", rec.Code, rec.Body.String())
	}
}
