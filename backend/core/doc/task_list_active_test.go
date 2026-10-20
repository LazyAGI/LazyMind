package doc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

func TestListTasksHTTPRunningExcludesSoftDeletedSources(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "dataset-1", "user-1")
	now := time.Now().UTC()
	for _, row := range []orm.Document{
		{ID: "deleted", DatasetID: "dataset-1", BaseModel: orm.BaseModel{DeletedAt: &now}},
		{ID: "active", DatasetID: "dataset-1"},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range []orm.Task{
		{ID: "deleted-local", DocID: "deleted", Ext: json.RawMessage(`{"task_state":"UPLOADED"}`)},
		{ID: "deleted-worker", DocID: "deleted", LazyllmTaskID: "worker-deleted"},
		{ID: "active-worker", DocID: "active", LazyllmTaskID: "worker-active"},
		{ID: "unbound-upload", Ext: json.RawMessage(`{"task_state":"UPLOADING"}`)},
		{ID: "missing-source", DocID: "not-yet-published", Ext: json.RawMessage(`{"task_state":"CREATING"}`)},
	} {
		row.DatasetID = "dataset-1"
		row.CreatedAt = now.Add(time.Duration(i) * time.Second)
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"worker-deleted", "worker-active"} {
		if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{
			TaskID: id, KbID: "dataset-1", Status: "RUNNING",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := mux.NewRouter()
	router.HandleFunc("/api/core/datasets/{dataset}/tasks", ListTasks).Methods(http.MethodGet)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	// Keep HTTP requests to the handler real; only the external scan policy is stubbed.
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	list := func(query string) ListTasksResponse {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/core/datasets/dataset-1/tasks?"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-User-Id", "user-1")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("list tasks HTTP status = %d", response.StatusCode)
		}
		var result ListTasksResponse
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	t.Run("running count and pagination retain active and unbound uploads", func(t *testing.T) {
		first := list("task_state=running&page_size=2")
		if first.TotalSize != 3 || len(first.Tasks) != 2 || first.NextPageToken == "" {
			t.Fatalf("first running page: total=%d count=%d next=%q; want 3, 2, nonempty", first.TotalSize, len(first.Tasks), first.NextPageToken)
		}
		second := list("task_state=running&page_size=2&page_token=" + url.QueryEscape(first.NextPageToken))
		if second.TotalSize != 3 || len(second.Tasks) != 1 || second.NextPageToken != "" {
			t.Fatalf("second running page: %+v", second)
		}
		var ids []string
		for _, row := range append(first.Tasks, second.Tasks...) {
			ids = append(ids, row.TaskID)
		}
		sort.Strings(ids)
		if want := []string{"active-worker", "missing-source", "unbound-upload"}; !reflect.DeepEqual(ids, want) {
			t.Fatalf("running tasks = %v, want %v", ids, want)
		}
	})
	t.Run("history retains deleted source tasks and original states", func(t *testing.T) {
		result := list("document_id=deleted")
		if result.TotalSize != 2 || len(result.Tasks) != 2 {
			t.Fatalf("deleted source history = %+v", result)
		}
		states := map[string]string{}
		for _, row := range result.Tasks {
			states[row.TaskID] = row.TaskState
		}
		want := map[string]string{"deleted-local": "WAITING", "deleted-worker": "WORKING"}
		if !reflect.DeepEqual(states, want) {
			t.Fatalf("historical states = %v, want %v", states, want)
		}
	})
	t.Run("terminal filters retain deleted source history", func(t *testing.T) {
		if err := db.Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("task_id = ?", "worker-deleted").Update("status", "SUCCEEDED").Error; err != nil {
			t.Fatal(err)
		}
		result := list("task_state=success")
		if result.TotalSize != 1 || len(result.Tasks) != 1 || result.Tasks[0].TaskID != "deleted-worker" {
			t.Fatalf("successful deleted source history = %+v", result)
		}
	})
}
