package knowledge_market

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/store"
)

func TestMarketUnfinishedCountAcrossPagesAndFilters(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	for i, status := range []string{"pending", "running", "failed", "canceled", "succeeded"} {
		id := "summary-" + status
		insertInstallJob(t, db, id, "user-a", id, status, now.Add(time.Duration(i)*time.Second), 0, 1, `{"reason":"no_change"}`)
	}
	insertTask(t, db, "parsing", "dataset", "PARSING")
	insertTask(t, db, "waiting", "dataset", "WAITING")
	insertTask(t, db, "unknown", "dataset", "UNKNOWN")
	for _, id := range []string{"parsing", "waiting", "unknown"} {
		insertInstallJob(t, db, id, "user-a", id, "succeeded", now, 1, 1, `{"dataset_id":"dataset","task_ids":["`+id+`"]}`)
	}
	insertInstallJob(t, db, "foreign", "user-b", "foreign", "running", now, 0, 1, `{}`)
	for _, query := range []string{"?job_type=all&view=summary&page_size=1", "?job_type=all&view=summary&page_size=1&page=2", "?job_type=knowledge_market_update&status=failed"} {
		rec := performGetWithUser(t, router, "/knowledge-market/tasks"+query, "user-a")
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 {
			t.Fatalf("list %s: %d %s", query, rec.Code, rec.Body.String())
		}
		if body.Data["unfinished_count"] != float64(5) {
			t.Fatalf("count must be global and include parsing: %#v", body.Data)
		}
	}
}

func TestMarketUnfinishedCountHistoryRetryAndDraining(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	insertTask(t, db, "retry-file", "dataset", "WAITING")
	insertInstallJob(t, db, "history", "user-a", "kb-legal", "succeeded", now.Add(-time.Hour), 1, 1, `{"parse":{"state":"partial_failed","total":2,"done":1,"failed":1}}`)
	insertInstallJob(t, db, "retry", "user-a", "kb-legal", "succeeded", now, 1, 1, `{"dataset_id":"dataset","task_ids":["retry-file"]}`)
	insertInstallJob(t, db, "stopping", "user-a", "stopping", "canceled", now, 0, 1, `{"reason":"stopped"}`)
	until := now.Add(time.Minute)
	db.Model(&orm.AsyncJob{}).Where("id = ?", "stopping").Updates(map[string]any{"locked_by": "worker", "lock_until": until})
	readCount := func(want int) {
		t.Helper()
		rec := performGetWithUser(t, router, "/knowledge-market/tasks?job_type=all&view=summary", "user-a")
		var body struct {
			Data struct {
				Count int `json:"unfinished_count"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || body.Data.Count != want {
			t.Fatalf("want %d: %d %s", want, rec.Code, rec.Body.String())
		}
	}
	readCount(2)
	db.Model(&orm.Task{}).Where("id = ?", "retry-file").Update("ext", json.RawMessage(`{"task_state":"FAILED"}`))
	db.Model(&orm.AsyncJob{}).Where("id = ?", "stopping").Updates(map[string]any{"locked_by": "", "lock_until": nil})
	readCount(0)
}

func TestMarketUnfinishedCountMixedTypesAndFileScopes(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	for _, state := range []string{"WAITING", "SUCCESS", "FAILED"} {
		insertTask(t, db, state, "same-dataset", state)
		insertInstallJob(t, db, state, "user-a", state, "succeeded", now, 1, 1, `{"dataset_id":"same-dataset","task_ids":["`+state+`"]}`)
	}
	for _, kind := range []string{updateJobType, updateAllJobType} {
		insertInstallJob(t, db, kind, "user-a", kind, "pending", now, 0, 1, `{}`)
		db.Model(&orm.AsyncJob{}).Where("id = ?", kind).Update("job_type", kind)
	}
	for user, want := range map[string]int{"user-a": 3, "user-b": 0} {
		rec := performGetWithUser(t, router, "/knowledge-market/tasks?job_type=all&view=summary", user)
		var body struct {
			Data struct {
				Count *int `json:"unfinished_count"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || body.Data.Count == nil || *body.Data.Count != want {
			t.Fatalf("count for %s want %d: %d %s", user, want, rec.Code, rec.Body.String())
		}
	}
}

func TestMarketUnfinishedCountReadFailureDoesNotReturnZero(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	insertLinkedTask(t, db, "linked", "doc", "parse-id", "dataset")
	insertInstallJob(t, db, "active", "user-a", "active", "succeeded", now, 1, 1, `{"dataset_id":"dataset","task_ids":["linked"]}`)
	insertInstallJob(t, db, "done", "user-a", "done", "succeeded", now.Add(time.Minute), 1, 1, `{"reason":"no_change"}`)
	if err := db.Migrator().DropTable(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatal(err)
	}
	rec := performGetWithUser(t, router, "/knowledge-market/tasks?job_type=all&view=summary&page_size=1", "user-a")
	if rec.Code < 500 || rec.Code > 599 {
		t.Fatalf("must fail rather than claim zero: %d %s", rec.Code, rec.Body.String())
	}
}
