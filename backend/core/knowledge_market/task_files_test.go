package knowledge_market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func TestMarketTaskFilesPaginationAndHistory(t *testing.T) {
	router := newTaskTestRouter(t)
	router.HandleFunc("/knowledge-market/tasks/{job_id}/files", MarketTaskFiles).Methods(http.MethodGet)
	db := store.DB()
	ids := make([]string, 0, 14)
	for i := 0; i < 14; i++ {
		id := fmt.Sprintf("file-%02d", i)
		ids = append(ids, id)
		state := "SUCCESS"
		if i == 11 {
			state = "FAILED"
		}
		if i == 12 {
			state = "CANCELED"
		}
		if i == 13 {
			state = "WAITING"
		}
		insertTask(t, db, id, "dataset-files", state)
	}
	result, _ := json.Marshal(map[string]any{"dataset_id": "dataset-files", "task_ids": ids})
	insertInstallJob(t, db, "files-job", "user-a", "kb-legal", "succeeded", time.Now(), 2, 2, string(result))
	insertInstallWithConfig(t, db, "kb-legal", "user-a", "done", "dataset-files", string(result), time.Now())
	read := func(query, user string) map[string]any {
		t.Helper()
		rec := performGetWithUser(t, router, "/knowledge-market/tasks/files-job/files"+query, user)
		if rec.Code != 200 {
			t.Fatalf("files status %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Data
	}
	data := read("?status=success&page=2&page_size=10", "user-a")
	if data["total"] != float64(11) || len(data["items"].([]any)) != 1 {
		t.Fatalf("wrong page: %#v", data)
	}
	counts := data["counts"].(map[string]any)
	if counts["success"] != float64(11) || counts["failed"] != float64(1) || counts["canceled"] != float64(1) || counts["unfinished"] != float64(1) {
		t.Fatalf("wrong counts: %#v", counts)
	}
	if data["items"].([]any)[0].(map[string]any)["file_id"] != "file-10" {
		t.Fatalf("unstable order: %#v", data)
	}
	data = read("?status=success&page=999", "user-a")
	if data["page"] != float64(2) {
		t.Fatalf("page not clamped: %#v", data)
	}
	for _, query := range []string{"?status=other", "?status=failed&page=0", "?page_size=101", "?page=abc"} {
		if rec := performGetWithUser(t, router, "/knowledge-market/tasks/files-job/files"+query, "user-a"); rec.Code != 400 {
			t.Fatalf("invalid query %s: %d", query, rec.Code)
		}
	}
	if rec := performGetWithUser(t, router, "/knowledge-market/tasks/files-job/files", "user-b"); rec.Code != 404 {
		t.Fatalf("ownership: %d", rec.Code)
	}
	// Once processing finishes, freeze the file-level result before a retry can change it.
	db.Model(&orm.Task{}).Where("id = ?", "file-13").Update("ext", json.RawMessage(`{"task_state":"CANCELED"}`))
	var install orm.KnowledgeMarketInstall
	db.Where("market_item_id = ? AND user_id = ?", "kb-legal", "user-a").Take(&install)
	if err := doc.SnapshotMarketTaskHistory(context.Background(), db, &install); err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.Task{}).Where("id = ?", "file-11").Update("ext", json.RawMessage(`{"task_state":"SUCCESS"}`))
	data = read("?status=failed", "user-a")
	if data["total"] != float64(1) || len(data["items"].([]any)) != 1 {
		t.Fatalf("history changed after retry: %#v", data)
	}
	// Old aggregate-only snapshots cannot safely recover historical file rows.
	db.Model(&orm.AsyncJob{}).Where("id = ?", "files-job").Update("result_json", json.RawMessage(`{"parse":{"state":"done","total":14,"done":14}}`))
	data = read("?status=success", "user-a")
	if data["file_details_available"] != false || data["total"] != float64(14) {
		t.Fatalf("invented legacy details: %#v", data)
	}
}

func TestMarketTaskSummaryMixedTypes(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now()
	for i, kind := range marketJobTypes {
		id := fmt.Sprintf("summary-%d", i)
		insertInstallJob(t, db, id, "user-a", "kb-legal", "succeeded", now, 2, 2, `{"reason":"no_change"}`)
		db.Model(&orm.AsyncJob{}).Where("id = ?", id).Update("job_type", kind)
	}
	insertInstallJob(t, db, "other-user", "user-b", "kb-legal", "succeeded", now, 2, 2, `{}`)
	rec := performGetWithUser(t, router, "/knowledge-market/tasks?job_type=all&view=summary&page_size=2", "user-a")
	if rec.Code != 200 {
		t.Fatalf("summary: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Total int              `json:"total"`
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Data.Total != 3 || len(body.Data.Items) != 2 || body.Data.Items[0]["job_id"] != "summary-2" {
		t.Fatalf("wrong summary: %#v", body)
	}
	if _, ok := body.Data.Items[0]["parse"].(map[string]any)["failures"]; ok {
		t.Fatal("summary included failure rows")
	}
}

func TestMarketTaskFilesUnknownImportAndIsolation(t *testing.T) {
	router := newTaskTestRouter(t)
	router.HandleFunc("/knowledge-market/tasks/{job_id}/files", MarketTaskFiles).Methods(http.MethodGet)
	db := store.DB()
	insertTask(t, db, "unknown-file", "own-dataset", "UNKNOWN")
	insertTask(t, db, "foreign-file", "other-dataset", "SUCCESS")
	result := `{"dataset_id":"own-dataset","task_ids":["unknown-file","foreign-file"],"failures":[{"name":"import.pdf","reason":"private-provider-error"}]}`
	insertInstallJob(t, db, "isolated-job", "user-a", "kb-legal", "succeeded", time.Now(), 2, 2, result)
	rec := performGetWithUser(t, router, "/knowledge-market/tasks/isolated-job/files?status=failed", "user-a")
	if rec.Code != 200 {
		t.Fatalf("files: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Items  []doc.MarketTaskFile `json:"items"`
			Counts map[string]int       `json:"counts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Counts["unfinished"] != 1 || body.Data.Counts["failed"] != 2 || len(body.Data.Items) != 2 {
		t.Fatalf("wrong buckets: %#v", body)
	}
	if strings.Contains(rec.Body.String(), "private-provider-error") || strings.Contains(rec.Body.String(), "file_foreign-file") {
		t.Fatal("leaked provider detail or another dataset's file")
	}
	if rec := performGetWithUser(t, router, "/knowledge-market/tasks/isolated-job/files", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous access: %d", rec.Code)
	}
	if _, exists := marketResultDTO(json.RawMessage(`{"file_snapshot":[{"name":"private snapshot"}],"dataset_id":"own"}`))["file_snapshot"]; exists {
		t.Fatal("unpaginated snapshot leaked")
	}
}

func TestMarketTaskFilesPrefersSnapshotCommittedDuringRead(t *testing.T) {
	router := newTaskTestRouter(t)
	router.HandleFunc("/knowledge-market/tasks/{job_id}/files", MarketTaskFiles).Methods(http.MethodGet)
	db := store.DB()
	insertTask(t, db, "race-file", "race-dataset", "FAILED")
	insertInstallJob(t, db, "race-job", "user-a", "kb-legal", "succeeded", time.Now(), 2, 2, `{"dataset_id":"race-dataset","task_ids":["race-file"]}`)
	frozen := false
	const callback = "test:freeze-market-history"
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if frozen || tx.Statement.Table != "tasks" {
			return
		}
		frozen = true
		// A competing retry first freezes the old result, then changes the file.
		result := json.RawMessage(`{"dataset_id":"race-dataset","parse":{"state":"failed","total":1,"failed":1},"file_snapshot":[{"file_id":"race-file","name":"old.pdf","status":"failed","reason_code":"parse_failed"}]}`)
		if err := db.Model(&orm.AsyncJob{}).Where("id = ?", "race-job").Update("result_json", result).Error; err != nil {
			t.Error(err)
		}
		if err := db.Model(&orm.Task{}).Where("id = ?", "race-file").Update("ext", json.RawMessage(`{"task_state":"SUCCESS"}`)).Error; err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove(callback)
	rec := performGetWithUser(t, router, "/knowledge-market/tasks/race-job/files?status=failed", "user-a")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "old.pdf") {
		t.Fatalf("lost historical snapshot: %d %s", rec.Code, rec.Body.String())
	}
}
