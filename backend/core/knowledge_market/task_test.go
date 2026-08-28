package knowledge_market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/store"
)

// newTaskTestRouter seeds the handler test catalog and migrates the async job
// and install models needed by the task/install endpoints.
func newTaskTestRouter(t *testing.T) *mux.Router {
	t.Helper()
	// Schema-less readonly tables so the doc-service task table can be
	// migrated and queried on the same SQLite test database.
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}, &orm.KnowledgeMarketInstall{}, &orm.Task{}, &readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatalf("auto migrate task models: %v", err)
	}
	if err := SeedCatalog(context.Background(), db, writeCatalog(t, handlerTestCatalog)); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	store.Init(db, db, nil)
	return marketRouter()
}

func performGetWithUser(t *testing.T, router *mux.Router, path, userID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// insertInstallJob inserts an async install job row with fixed identity.
func insertInstallJob(t *testing.T, db *gorm.DB, id, userID, itemID, status string, createdAt time.Time, progressCurrent, progressTotal int64, resultJSON string) {
	t.Helper()
	job := &orm.AsyncJob{
		ID:              id,
		JobType:         "knowledge_market_install",
		Status:          status,
		ResourceType:    "knowledge_market_item",
		ResourceID:      itemID,
		IdempotencyKey:  "kb_install:" + itemID + ":" + userID,
		PayloadJSON:     json.RawMessage(fmt.Sprintf(`{"market_item_id":%q,"user_id":%q,"revision":"master"}`, itemID, userID)),
		ResultJSON:      json.RawMessage(resultJSON),
		ProgressCurrent: progressCurrent,
		ProgressTotal:   progressTotal,
		AttemptCount:    1,
		MaxAttempts:     2,
		NextRunAt:       createdAt,
		CreateUserID:    userID,
		CreateUserName:  "user-" + userID,
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt,
	}
	if status == "succeeded" {
		finished := createdAt.Add(time.Minute)
		job.FinishedAt = &finished
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("insert job %s: %v", id, err)
	}
}

func insertInstall(t *testing.T, db *gorm.DB, itemID, userID, state, datasetID string, updatedAt time.Time) {
	t.Helper()
	insertInstallWithConfig(t, db, itemID, userID, state, datasetID, `{}`, updatedAt)
}

func insertInstallWithConfig(t *testing.T, db *gorm.DB, itemID, userID, state, datasetID, config string, updatedAt time.Time) {
	t.Helper()
	row := &orm.KnowledgeMarketInstall{
		MarketItemID:     itemID,
		UserID:           userID,
		InstalledVersion: "v2.5.0",
		DatasetID:        datasetID,
		InstallState:     state,
		Config:           json.RawMessage(config),
		CreatedAt:        updatedAt,
		UpdatedAt:        updatedAt,
	}
	if state == "done" {
		row.InstalledAt = &updatedAt
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("insert install %s/%s: %v", itemID, userID, err)
	}
}

// insertTask inserts a parse task row whose ext carries the given task_state.
func insertTask(t *testing.T, db *gorm.DB, id, datasetID, state string) {
	t.Helper()
	insertTaskWithDocID(t, db, id, "", datasetID, state)
}

func insertTaskWithDocID(t *testing.T, db *gorm.DB, id, docID, datasetID, state string) {
	t.Helper()
	now := time.Now().UTC()
	ext := fmt.Sprintf(`{"task_type":"TASK_TYPE_PARSE_UPLOADED","task_state":%q,"data_source_type":"MARKET"}`, state)
	row := &orm.Task{
		ID:          id,
		DocID:       docID,
		DatasetID:   datasetID,
		TaskType:    "TASK_TYPE_PARSE_UPLOADED",
		DisplayName: "file_" + id,
		Ext:         json.RawMessage(ext),
		BaseModel: orm.BaseModel{
			CreateUserID: "user-a",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("insert task %s: %v", id, err)
	}
}

// insertLinkedTask inserts a core parse task as the market install pipeline
// leaves it: lazyllm_task_id/doc_id populated and ext.task_state empty (the
// parse submission path never writes ext.task_state).
func insertLinkedTask(t *testing.T, db *gorm.DB, id, docID, lazyllmTaskID, datasetID string) {
	t.Helper()
	now := time.Now().UTC()
	ext := `{"task_type":"TASK_TYPE_PARSE_UPLOADED","data_source_type":"MARKET"}`
	row := &orm.Task{
		ID:            id,
		LazyllmTaskID: lazyllmTaskID,
		DocID:         docID,
		DatasetID:     datasetID,
		TaskType:      "TASK_TYPE_PARSE_UPLOADED",
		DisplayName:   "file_" + id,
		Ext:           json.RawMessage(ext),
		BaseModel: orm.BaseModel{
			CreateUserID: "user-a",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("insert task %s: %v", id, err)
	}
}

// insertDocServiceTask inserts a row in the doc-service task table, the
// authoritative status source for the install parse progress.
func insertDocServiceTask(t *testing.T, db *gorm.DB, taskID, docID, kbID, status string) {
	t.Helper()
	now := time.Now().UTC()
	row := &readonlyorm.LazyLLMDocServiceTaskRow{
		TaskID:    taskID,
		TaskType:  "DOC_ADD",
		DocID:     docID,
		KbID:      kbID,
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.Table(row.TableName()).Create(row).Error; err != nil {
		t.Fatalf("insert doc-service task %s: %v", taskID, err)
	}
}

func mustTaskData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	return mustData(t, rec)
}

func TestMarketListInstallTasks(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	// job_a1: finished install; job_a2: still importing, created later so it
	// sorts first in the descending list.
	insertInstallJob(t, db, "job_a1", "user-a", "law-cn", "succeeded", base, 2, 2, `{"dataset_id":"ds_1","submitted":2}`)
	insertInstall(t, db, "law-cn", "user-a", "done", "ds_1", base.Add(time.Minute))
	insertInstallJob(t, db, "job_a2", "user-a", "finance", "running", base.Add(time.Hour), 1, 2, "")
	insertInstall(t, db, "finance", "user-a", "importing", "", base.Add(time.Hour+time.Minute))
	// Another user's job must never leak into the response.
	insertInstallJob(t, db, "job_b1", "user-b", "gov", "succeeded", base.Add(2*time.Hour), 2, 2, `{"dataset_id":"ds_9","submitted":1}`)

	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks", "user-a"))
	if got := data["total"].(float64); got != 2 {
		t.Fatalf("total=%v, want 2", got)
	}
	items := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%d, want 2", len(items))
	}

	first := items[0].(map[string]any)
	if first["job_id"] != "job_a2" {
		t.Fatalf("first job_id=%v, want job_a2 (created desc)", first["job_id"])
	}
	if first["job_status"] != "running" || first["install_state"] != "importing" {
		t.Fatalf("unexpected states: %v %v", first["job_status"], first["install_state"])
	}
	if first["name"] != "金融监管与业务知识库" || first["icon"] != "¥" {
		t.Fatalf("unexpected item enrichment: %v %v", first["name"], first["icon"])
	}
	if first["market_item_id"] != "finance" || first["dataset_id"] != "" {
		t.Fatalf("unexpected item/dataset: %v %v", first["market_item_id"], first["dataset_id"])
	}
	progress := first["progress"].(map[string]any)
	if progress["current"] != float64(1) || progress["total"] != float64(2) {
		t.Fatalf("progress=%v, want 1/2", progress)
	}

	second := items[1].(map[string]any)
	if second["job_id"] != "job_a1" {
		t.Fatalf("second job_id=%v, want job_a1", second["job_id"])
	}
	// Version numbers are not exposed anymore; the install row still enriches
	// state and dataset linkage.
	if _, exists := second["version"]; exists {
		t.Fatal("task item must not expose version")
	}
	if second["install_state"] != "done" || second["dataset_id"] != "ds_1" {
		t.Fatalf("unexpected install data: %v %v", second["install_state"], second["dataset_id"])
	}
	if second["error_message"] != "" {
		t.Fatalf("error_message=%v, want empty", second["error_message"])
	}
	if second["finished_at"] == nil {
		t.Fatal("finished_at must be set on a succeeded job")
	}
}

func TestMarketListInstallTasksFilterPaginationAndAuth(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	insertInstallJob(t, db, "job_a1", "user-a", "law-cn", "succeeded", base, 2, 2, `{"dataset_id":"ds_1","submitted":2}`)
	insertInstallJob(t, db, "job_a2", "user-a", "finance", "running", base.Add(time.Hour), 1, 2, "")

	// status filter
	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks?status=running", "user-a"))
	if got := data["total"].(float64); got != 1 {
		t.Fatalf("filtered total=%v, want 1", got)
	}
	if items := data["items"].([]any); len(items) != 1 || items[0].(map[string]any)["job_id"] != "job_a2" {
		t.Fatalf("filtered items=%v, want job_a2 only", items)
	}

	// pagination
	data = mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks?page=1&page_size=1", "user-a"))
	if got := data["total"].(float64); got != 2 {
		t.Fatalf("paginated total=%v, want 2", got)
	}
	if items := data["items"].([]any); len(items) != 1 || items[0].(map[string]any)["job_id"] != "job_a2" {
		t.Fatalf("page1 items=%v, want job_a2", items)
	}
	data = mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks?page=2&page_size=1", "user-a"))
	if items := data["items"].([]any); len(items) != 1 || items[0].(map[string]any)["job_id"] != "job_a1" {
		t.Fatalf("page2 items=%v, want job_a1", items)
	}

	// errors
	if rec := performGetWithUser(t, router, "/knowledge-market/tasks?status=unknown", "user-a"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status -> %d, want 400", rec.Code)
	}
	if rec := performGetWithUser(t, router, "/knowledge-market/tasks", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing user -> %d, want 400", rec.Code)
	}
}

func TestMarketGetInstallTask(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	insertInstallJob(t, db, "job_a1", "user-a", "law-cn", "succeeded", base, 2, 2, `{"dataset_id":"ds_1","submitted":2}`)
	insertInstall(t, db, "law-cn", "user-a", "done", "ds_1", base.Add(time.Minute))

	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/job_a1", "user-a"))
	if data["job_id"] != "job_a1" || data["job_status"] != "succeeded" {
		t.Fatalf("unexpected detail: %v", data["job_id"])
	}
	if data["attempt_count"] != float64(1) || data["max_attempts"] != float64(2) {
		t.Fatalf("attempts=%v/%v, want 1/2", data["attempt_count"], data["max_attempts"])
	}
	payload := data["payload"].(map[string]any)
	if payload["market_item_id"] != "law-cn" || payload["revision"] != "master" {
		t.Fatalf("payload=%v, want law-cn/master", payload)
	}
	result := data["result"].(map[string]any)
	if result["dataset_id"] != "ds_1" || result["submitted"] != float64(2) {
		t.Fatalf("result=%v, want ds_1/2", result)
	}
	if data["install_state"] != "done" || data["dataset_id"] != "ds_1" {
		t.Fatalf("install enrichment=%v/%v", data["install_state"], data["dataset_id"])
	}

	// ownership: another user must not see the task
	if rec := performGetWithUser(t, router, "/knowledge-market/tasks/job_a1", "user-b"); rec.Code != http.StatusNotFound {
		t.Fatalf("other user -> %d, want 404", rec.Code)
	}
	// unknown task
	if rec := performGetWithUser(t, router, "/knowledge-market/tasks/job_missing", "user-a"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task -> %d, want 404", rec.Code)
	}
	// missing user
	if rec := performGetWithUser(t, router, "/knowledge-market/tasks/job_a1", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing user -> %d, want 400", rec.Code)
	}
}

func TestMarketGetInstallTaskParseProgress(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	insertInstallJob(t, db, "job_p1", "user-a", "law-cn", "succeeded", base, 2, 2, `{"dataset_id":"ds_1","submitted":4}`)
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds_1",
		`{"revision":"master","task_ids":["t1","t2","t3","t4"],"files":[]}`, base.Add(time.Minute))
	// ext.task_state is empty; the authoritative state comes from the
	// doc-service task table linked through lazyllm_task_id.
	insertLinkedTask(t, db, "t1", "d1", "lazy-1", "ds_1")
	insertDocServiceTask(t, db, "lazy-1", "d1", "ds_1", "WORKING")
	insertLinkedTask(t, db, "t2", "d2", "lazy-2", "ds_1")
	insertDocServiceTask(t, db, "lazy-2", "d2", "ds_1", "SUCCESS")
	insertLinkedTask(t, db, "t3", "d3", "lazy-3", "ds_1")
	insertDocServiceTask(t, db, "lazy-3", "d3", "ds_1", "FAILED")
	insertLinkedTask(t, db, "t4", "d4", "lazy-4", "ds_1")
	insertDocServiceTask(t, db, "lazy-4", "d4", "ds_1", "WAITING")

	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/job_p1", "user-a"))
	parse := data["parse"].(map[string]any)
	if parse["state"] != "failed" || parse["total"] != float64(4) {
		t.Fatalf("parse=%v, want failed/4", parse)
	}
	if parse["pending"] != float64(1) || parse["parsing"] != float64(1) ||
		parse["done"] != float64(1) || parse["failed"] != float64(1) {
		t.Fatalf("parse counts=%v", parse)
	}
	if data["stage"] != "failed" {
		t.Fatalf("stage=%v, want failed", data["stage"])
	}
	if data["overall_percent"] != float64(70) {
		t.Fatalf("overall_percent=%v, want 70", data["overall_percent"])
	}
}

func TestMarketGetInstallTaskParseProgressFallsBackToExtState(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	insertInstallJob(t, db, "job_p2", "user-a", "law-cn", "succeeded", base, 2, 2, `{"dataset_id":"ds_1","submitted":2}`)
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds_1",
		`{"revision":"master","task_ids":["t1","t2"],"files":[]}`, base.Add(time.Minute))
	// t1 has no lazyllm_task_id: the doc-service row is matched by doc_id.
	insertTaskWithDocID(t, db, "t1", "d1", "ds_1", "")
	insertDocServiceTask(t, db, "lazy-1", "d1", "ds_1", "SUCCESS")
	// t2 has no doc-service row -> falls back to ext.task_state.
	insertTask(t, db, "t2", "ds_1", "RUNNING")

	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/job_p2", "user-a"))
	parse := data["parse"].(map[string]any)
	if parse["state"] != "parsing" || parse["total"] != float64(2) {
		t.Fatalf("parse=%v, want parsing/2", parse)
	}
	if parse["done"] != float64(1) || parse["parsing"] != float64(1) {
		t.Fatalf("parse counts=%v, want done=1 parsing=1", parse)
	}
	if data["stage"] != "parsing" {
		t.Fatalf("stage=%v, want parsing", data["stage"])
	}
}

func TestMarketGetInstallTaskStageAndPercent(t *testing.T) {
	cases := []struct {
		name         string
		jobStatus    string
		progressCur  int64
		installState string
		taskStates   []string
		wantStage    string
		wantPercent  float64
	}{
		{name: "pending", jobStatus: "pending", progressCur: 0, installState: "", wantStage: "pending", wantPercent: 0},
		{name: "downloading", jobStatus: "running", progressCur: 0, installState: "downloading", wantStage: "downloading", wantPercent: 0},
		{name: "importing", jobStatus: "running", progressCur: 1, installState: "importing", wantStage: "importing", wantPercent: 40},
		{name: "parsing", jobStatus: "succeeded", progressCur: 2, installState: "done", taskStates: []string{"RUNNING", "SUCCEEDED"}, wantStage: "parsing", wantPercent: 80},
		{name: "done", jobStatus: "succeeded", progressCur: 2, installState: "done", taskStates: []string{"SUCCEEDED", "SUCCEEDED"}, wantStage: "done", wantPercent: 100},
		{name: "parse-failed", jobStatus: "succeeded", progressCur: 2, installState: "done", taskStates: []string{"FAILED"}, wantStage: "failed", wantPercent: 60},
		{name: "job-failed-download", jobStatus: "failed", progressCur: 0, installState: "downloading", wantStage: "failed", wantPercent: 0},
		{name: "job-failed-import", jobStatus: "failed", progressCur: 1, installState: "importing", wantStage: "failed", wantPercent: 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newTaskTestRouter(t)
			db := store.DB()
			base := time.Now().UTC().Add(-time.Hour)
			jobID := "job_" + tc.name
			insertInstallJob(t, db, jobID, "user-a", "law-cn", tc.jobStatus, base, tc.progressCur, 2, "")
			if tc.installState != "" {
				cfg := `{"revision":"master","task_ids":[],"files":[]}`
				if len(tc.taskStates) > 0 {
					ids := make([]string, 0, len(tc.taskStates))
					for i := range tc.taskStates {
						ids = append(ids, fmt.Sprintf("%s_t%d", jobID, i))
					}
					idsJSON, _ := json.Marshal(ids)
					cfg = fmt.Sprintf(`{"revision":"master","task_ids":%s,"files":[]}`, idsJSON)
					for i, state := range tc.taskStates {
						insertTask(t, db, ids[i], "ds_1", state)
					}
				}
				insertInstallWithConfig(t, db, "law-cn", "user-a", tc.installState, "ds_1", cfg, base.Add(time.Minute))
			}

			data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/"+jobID, "user-a"))
			if data["stage"] != tc.wantStage {
				t.Fatalf("stage=%v, want %s", data["stage"], tc.wantStage)
			}
			if got := data["overall_percent"].(float64); got != tc.wantPercent {
				t.Fatalf("overall_percent=%v, want %v", got, tc.wantPercent)
			}
		})
	}
}

func TestMarketListInstalls(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	insertInstall(t, db, "law-cn", "user-a", "done", "ds_1", base)
	insertInstall(t, db, "gov", "user-a", "importing", "", base.Add(time.Hour))
	insertInstall(t, db, "finance", "user-b", "done", "ds_9", base.Add(2*time.Hour))

	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/installs", "user-a"))
	if got := data["total"].(float64); got != 2 {
		t.Fatalf("total=%v, want 2", got)
	}
	items := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%d, want 2", len(items))
	}
	first := items[0].(map[string]any)
	if first["market_item_id"] != "gov" || first["install_state"] != "importing" {
		t.Fatalf("first install=%v/%v, want gov/importing (updated desc)", first["market_item_id"], first["install_state"])
	}
	if first["name"] != "政务办事知识库" || first["icon"] != "🏛" || first["domain"] != "政务" {
		t.Fatalf("unexpected item info: %v", first)
	}
	second := items[1].(map[string]any)
	if second["market_item_id"] != "law-cn" || second["install_state"] != "done" || second["dataset_id"] != "ds_1" {
		t.Fatalf("second install=%v", second)
	}
	if second["installed_at"] == nil {
		t.Fatal("installed_at must be set on done")
	}
	if second["installed_version"] != "v2.5.0" {
		t.Fatalf("installed_version=%v, want v2.5.0", second["installed_version"])
	}
	if _, hasPage := data["page"]; hasPage {
		t.Fatal("installs response must not include pagination")
	}

	if rec := performGetWithUser(t, router, "/knowledge-market/installs", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing user -> %d, want 400", rec.Code)
	}
}

func TestMarketGetInstallTaskHealsStuckJob(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	// Split-brain state: the install row already failed but the async job is
	// stuck in running (the two writes are not atomic; a crash between them
	// leaves the job running forever).
	insertInstallJob(t, db, "job_stuck", "user-a", "law-cn", "running", base, 80, 100, "")
	insertInstall(t, db, "law-cn", "user-a", "failed", "", base.Add(time.Minute))

	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/job_stuck", "user-a"))
	if data["job_status"] != "failed" {
		t.Fatalf("job_status=%v, want failed", data["job_status"])
	}
	if data["stage"] != "failed" {
		t.Fatalf("stage=%v, want failed", data["stage"])
	}
	if data["error_message"] == "" {
		t.Fatal("error_message must be filled by the heal")
	}

	var job orm.AsyncJob
	if err := db.Where("id = ?", "job_stuck").Take(&job).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.Status != "failed" || job.FinishedAt == nil || job.LockedBy != "" || job.LockUntil != nil {
		t.Fatalf("job not healed: status=%s locked_by=%q lock_until=%v finished_at=%v", job.Status, job.LockedBy, job.LockUntil, job.FinishedAt)
	}

	// A second poll must not rewrite the healed row (idempotent heal).
	before := job.UpdatedAt
	time.Sleep(5 * time.Millisecond)
	data = mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/job_stuck", "user-a"))
	if data["job_status"] != "failed" {
		t.Fatalf("second poll job_status=%v, want failed", data["job_status"])
	}
	if err := db.Where("id = ?", "job_stuck").Take(&job).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if !job.UpdatedAt.Equal(before) {
		t.Fatalf("healed job rewritten: updated_at %v -> %v", before, job.UpdatedAt)
	}
}

func TestMarketListInstallsHealsStuckJob(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	insertInstallJob(t, db, "job_stuck2", "user-a", "law-cn", "running", base, 80, 100, "")
	insertInstall(t, db, "law-cn", "user-a", "failed", "", base.Add(time.Minute))

	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/installs", "user-a"))
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if item["active"] != false {
		t.Fatalf("active=%v, want false after heal", item["active"])
	}
	if item["install_state"] != "failed" {
		t.Fatalf("install_state=%v, want failed", item["install_state"])
	}

	var job orm.AsyncJob
	if err := db.Where("id = ?", "job_stuck2").Take(&job).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.Status != "failed" {
		t.Fatalf("job status=%s, want failed", job.Status)
	}
}

func TestMarketListInstallTasksHealsStuckJob(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	base := time.Now().UTC().Add(-time.Hour)
	insertInstallJob(t, db, "job_stuck3", "user-a", "law-cn", "running", base, 80, 100, "")
	insertInstall(t, db, "law-cn", "user-a", "failed", "", base.Add(time.Minute))

	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks", "user-a"))
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if item["job_status"] != "failed" {
		t.Fatalf("job_status=%v, want failed", item["job_status"])
	}
	if item["error_message"] == "" {
		t.Fatal("error_message must be filled by the heal")
	}

	var job orm.AsyncJob
	if err := db.Where("id = ?", "job_stuck3").Take(&job).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.Status != "failed" {
		t.Fatalf("job status=%s, want failed", job.Status)
	}
}
