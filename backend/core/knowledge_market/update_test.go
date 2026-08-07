package knowledge_market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/knowledge_market/download"
	"lazymind/core/store"
)

func newUpdateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	for _, model := range []any{
		&orm.KnowledgeMarketInstall{},
		&orm.AsyncJob{},
		&orm.Dataset{},
		&orm.Document{},
	} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatalf("auto migrate %T: %v", model, err)
		}
	}
	return db
}

func updateRouter() *mux.Router {
	r := mux.NewRouter()
	r.UseEncodedPath()
	r.HandleFunc("/knowledge-market/items/{market_item_id}:update", MarketUpdate).Methods(http.MethodPost)
	r.HandleFunc("/knowledge-market:update-all", MarketUpdateAll).Methods(http.MethodPost)
	return r
}

func performPost(t *testing.T, router *mux.Router, path, userID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("X-User-Name", "Alice")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func seedPublishedItem(t *testing.T, db *gorm.DB, id, packageURL string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.KnowledgeMarketItem{
		ID:              id,
		Category:        "industry",
		Name:            "知识库 " + id,
		Description:     "desc",
		Tags:            json.RawMessage(`[]`),
		SampleQuestions: json.RawMessage(`[]`),
		PackageURL:      packageURL,
		PackageRevision: "master",
		Status:          "published",
		SortOrder:       1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}).Error; err != nil {
		t.Fatalf("create item %s: %v", id, err)
	}
}

func TestDecodeUpdatePayload(t *testing.T) {
	raw := json.RawMessage(`{"market_item_id":" law-cn ","user_id":" u1 ","revision":" master "}`)
	p, err := decodeUpdatePayload(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.MarketItemID != "law-cn" || p.UserID != "u1" || p.Revision != "master" {
		t.Fatalf("unexpected payload %+v", p)
	}
	if _, err := decodeUpdatePayload(json.RawMessage(`{"user_id":"u1"}`)); err == nil {
		t.Fatal("expected error for missing market_item_id")
	}
	if _, err := decodeUpdateAllPayload(json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for missing user_id")
	}
}

func TestSameFileSnapshot(t *testing.T) {
	files := []download.FetchedFile{{Path: "a.txt", Size: 3, SHA256: "abc"}, {Path: "b.txt", Size: 4, SHA256: "def"}}
	old := []fileSnapshot{{Path: "a.txt", Size: 3, SHA256: "abc"}, {Path: "b.txt", Size: 4, SHA256: "def"}}
	if !sameFileSnapshot(files, old) {
		t.Fatal("expected identical snapshots to match")
	}
	old[1] = fileSnapshot{Path: "b.txt", Size: 4, SHA256: "zzz"}
	if sameFileSnapshot(files, old) {
		t.Fatal("expected changed sha256 to differ")
	}
	if sameFileSnapshot(files, old[:1]) {
		t.Fatal("expected different lengths to differ")
	}
}

func TestHandleUpdateJobSkipsWhenNotInstalled(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)

	payload, _ := json.Marshal(updateJobPayload{MarketItemID: "law-cn", UserID: "u1", Revision: "master"})
	result, err := HandleUpdateJob(context.Background(), asyncjob.Job{ID: "job_1", PayloadJSON: payload}, nil)
	if err != nil {
		t.Fatalf("HandleUpdateJob: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal(result.ResultJSON, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res["skipped"] != true || res["reason"] != "not_installed" {
		t.Fatalf("unexpected result %v", res)
	}
}

func TestHandleUpdateJobSkipsWhenDatasetDeleted(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)
	now := time.Now().UTC()
	// Install row pointing at a dataset that no longer exists.
	if err := db.Create(&orm.KnowledgeMarketInstall{
		MarketItemID: "law-cn",
		UserID:       "u1",
		DatasetID:    "ds-gone",
		InstallState: string(orm.InstallStateDone),
		Config:       json.RawMessage(`{}`),
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create install: %v", err)
	}

	payload, _ := json.Marshal(updateJobPayload{MarketItemID: "law-cn", UserID: "u1", Revision: "master"})
	result, err := HandleUpdateJob(context.Background(), asyncjob.Job{ID: "job_1", PayloadJSON: payload}, nil)
	if err != nil {
		t.Fatalf("HandleUpdateJob: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal(result.ResultJSON, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res["skipped"] != true || res["reason"] != "not_installed" {
		t.Fatalf("unexpected result %v", res)
	}
}

func TestHandleUpdateAllJobSpawnsSubTasks(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)
	now := time.Now().UTC()

	// law-cn: installed, git package with an empty commit baseline -> full
	// refresh sub-task (no network needed).
	seedPublishedItem(t, db, "law-cn", "https://example.com/law-cn.git")
	if err := db.Create(&orm.KnowledgeMarketInstall{
		MarketItemID: "law-cn",
		UserID:       "u1",
		DatasetID:    "ds_1",
		InstallState: string(orm.InstallStateDone),
		Config:       json.RawMessage(`{}`),
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create install: %v", err)
	}
	// ghost: installed but the catalog item disappeared -> skipped.
	if err := db.Create(&orm.KnowledgeMarketInstall{
		MarketItemID: "ghost",
		UserID:       "u1",
		DatasetID:    "ds_2",
		InstallState: string(orm.InstallStateDone),
		Config:       json.RawMessage(`{}`),
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create install: %v", err)
	}

	payload, _ := json.Marshal(updateAllJobPayload{UserID: "u1", UserName: "Alice"})
	result, err := HandleUpdateAllJob(context.Background(), asyncjob.Job{ID: "job_all", PayloadJSON: payload}, nil)
	if err != nil {
		t.Fatalf("HandleUpdateAllJob: %v", err)
	}
	var res struct {
		Checked int      `json:"checked"`
		Updated []string `json:"updated"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(result.ResultJSON, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Checked != 2 || len(res.Updated) != 1 || res.Updated[0] != "law-cn" {
		t.Fatalf("unexpected batch result %+v", res)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "ghost" {
		t.Fatalf("unexpected skipped %v", res.Skipped)
	}
	var jobs []orm.AsyncJob
	if err := db.Where("job_type = ?", updateJobType).Find(&jobs).Error; err != nil {
		t.Fatalf("query update jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ResourceID != "law-cn" || jobs[0].IdempotencyKey != "kb_update:law-cn:u1" {
		t.Fatalf("unexpected spawned jobs %+v", jobs)
	}
}

func TestMarketItemHasUpdateEmptyDatasetAlwaysNeedsUpdate(t *testing.T) {
	// Even when the installed snapshot matches the remote content, an empty
	// dataset (a previous failed update cleared the docs) must always refresh.
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)
	seedPublishedItem(t, db, "law-cn", "https://example.com/law-cn.git")
	now := time.Now().UTC()
	cfg, _ := json.Marshal(installConfig{Revision: "master", Commit: "abc123"})
	install := &orm.KnowledgeMarketInstall{
		MarketItemID: "law-cn", UserID: "u1", DatasetID: "ds_1",
		InstallState: string(orm.InstallStateFailed), Config: cfg,
		CreatedAt: now, UpdatedAt: now,
	}

	hasUpdate, err := marketItemHasUpdate(context.Background(), getItem(t, db, "law-cn"), install)
	if err != nil {
		t.Fatalf("marketItemHasUpdate: %v", err)
	}
	if !hasUpdate {
		t.Fatal("empty dataset must always be treated as needing an update")
	}
}

func TestMarketUpdateConflict409(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)
	seedPublishedItem(t, db, "law-cn", "https://example.com/law-cn.git")
	now := time.Now().UTC()
	if err := db.Create(&orm.KnowledgeMarketInstall{
		MarketItemID: "law-cn", UserID: "u1", DatasetID: "ds_1",
		InstallState: string(orm.InstallStateDone), Config: json.RawMessage(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create install: %v", err)
	}
	if err := db.Create(&orm.AsyncJob{
		ID: "job_active", JobType: installJobType, Status: "running",
		ResourceType: "knowledge_market_item", ResourceID: "law-cn",
		IdempotencyKey: "kb_install:law-cn:u1", PayloadJSON: json.RawMessage(`{}`),
		NextRunAt: now, CreateUserID: "u1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create active job: %v", err)
	}

	router := updateRouter()
	rec := performPost(t, router, "/knowledge-market/items/law-cn:update", "u1")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestMarketUpdateNotInstalled404(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)
	seedPublishedItem(t, db, "law-cn", "https://example.com/law-cn.git")

	router := updateRouter()
	rec := performPost(t, router, "/knowledge-market/items/law-cn:update", "u1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestMarketUpdateEnqueues(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)
	seedPublishedItem(t, db, "law-cn", "https://example.com/law-cn.git")
	now := time.Now().UTC()
	if err := db.Create(&orm.KnowledgeMarketInstall{
		MarketItemID: "law-cn", UserID: "u1", DatasetID: "ds_1",
		InstallState: string(orm.InstallStateDone), Config: json.RawMessage(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create install: %v", err)
	}

	router := updateRouter()
	rec := performPost(t, router, "/knowledge-market/items/law-cn:update", "u1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", rec.Code, rec.Body.String())
	}
	var jobs []orm.AsyncJob
	if err := db.Where("job_type = ? AND resource_id = ?", updateJobType, "law-cn").Find(&jobs).Error; err != nil {
		t.Fatalf("query update jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].IdempotencyKey != "kb_update:law-cn:u1" {
		t.Fatalf("unexpected jobs %+v", jobs)
	}
}

// TestMarketUpdateTwiceCreatesFreshJob verifies the single-item update follows
// the same policy as update-all (P2-2): a second click after the first job
// succeeded must create a fresh job (new job_id) that actually re-runs,
// instead of replaying the old result.
func TestMarketUpdateTwiceCreatesFreshJob(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)
	seedPublishedItem(t, db, "law-cn", "https://example.com/law-cn.git")
	now := time.Now().UTC()
	if err := db.Create(&orm.KnowledgeMarketInstall{
		MarketItemID: "law-cn", UserID: "u1", DatasetID: "ds_1",
		InstallState: string(orm.InstallStateDone), Config: json.RawMessage(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create install: %v", err)
	}

	router := updateRouter()

	first := performPost(t, router, "/knowledge-market/items/law-cn:update", "u1")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d, want 200: %s", first.Code, first.Body.String())
	}
	firstJobID := jobIDFromUpdateAllResponse(t, first)
	if firstJobID == "" {
		t.Fatalf("first response missing job_id: %s", first.Body.String())
	}

	// Simulate the update finishing successfully before the user clicks again.
	if err := db.Model(&orm.AsyncJob{}).
		Where("id = ?", firstJobID).
		Updates(map[string]any{
			"status":      "succeeded",
			"finished_at": now,
			"updated_at":  now,
		}).Error; err != nil {
		t.Fatalf("mark first update succeeded: %v", err)
	}

	second := performPost(t, router, "/knowledge-market/items/law-cn:update", "u1")
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d, want 200: %s", second.Code, second.Body.String())
	}
	secondJobID := jobIDFromUpdateAllResponse(t, second)
	if secondJobID == "" {
		t.Fatalf("second response missing job_id: %s", second.Body.String())
	}
	if secondJobID == firstJobID {
		t.Fatalf("expected a fresh job on second click, reused %q", firstJobID)
	}

	var count int64
	if err := db.Model(&orm.AsyncJob{}).
		Where("job_type = ? AND resource_id = ? AND create_user_id = ?", updateJobType, "law-cn", "u1").
		Count(&count).Error; err != nil {
		t.Fatalf("count update jobs: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected history + fresh job = 2 rows, got %d", count)
	}

	// The retired history row keeps its record but no longer holds the active
	// idempotency key, so a third click keeps creating fresh jobs.
	var active int64
	if err := db.Model(&orm.AsyncJob{}).
		Where("job_type = ? AND idempotency_key = ?", updateJobType, "kb_update:law-cn:u1").
		Count(&active).Error; err != nil {
		t.Fatalf("count active-key jobs: %v", err)
	}
	if active != 1 {
		t.Fatalf("expected exactly one job holding the active key, got %d", active)
	}
}

func TestMarketUpdateAllConflict409(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)
	now := time.Now().UTC()
	if err := db.Create(&orm.AsyncJob{
		ID: "job_batch", JobType: updateAllJobType, Status: "running",
		ResourceType: "knowledge_market_user", ResourceID: "u1",
		IdempotencyKey: "kb_update_all:u1", PayloadJSON: json.RawMessage(`{}`),
		NextRunAt: now, CreateUserID: "u1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create batch job: %v", err)
	}

	router := updateRouter()
	rec := performPost(t, router, "/knowledge-market:update-all", "u1")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestMarketUpdateAllEnqueues(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)

	router := updateRouter()
	rec := performPost(t, router, "/knowledge-market:update-all", "u1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", rec.Code, rec.Body.String())
	}
	var jobs []orm.AsyncJob
	if err := db.Where("job_type = ?", updateAllJobType).Find(&jobs).Error; err != nil {
		t.Fatalf("query batch jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].IdempotencyKey != "kb_update_all:u1" {
		t.Fatalf("unexpected jobs %+v", jobs)
	}
}

// TestMarketUpdateAllTwiceCreatesFreshJob verifies the P2-2 fix: a second
// one-click update after the first batch succeeded must create a fresh job
// (new job_id) that actually re-runs, instead of replaying the old result.
func TestMarketUpdateAllTwiceCreatesFreshJob(t *testing.T) {
	db := newUpdateTestDB(t)
	store.Init(db, db, nil)

	router := updateRouter()

	first := performPost(t, router, "/knowledge-market:update-all", "u1")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d, want 200: %s", first.Code, first.Body.String())
	}
	firstJobID := jobIDFromUpdateAllResponse(t, first)
	if firstJobID == "" {
		t.Fatalf("first response missing job_id: %s", first.Body.String())
	}

	// Simulate the batch finishing successfully before the user clicks again.
	now := time.Now().UTC()
	if err := db.Model(&orm.AsyncJob{}).
		Where("id = ?", firstJobID).
		Updates(map[string]any{
			"status":      "succeeded",
			"finished_at": now,
			"updated_at":  now,
		}).Error; err != nil {
		t.Fatalf("mark first batch succeeded: %v", err)
	}

	second := performPost(t, router, "/knowledge-market:update-all", "u1")
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d, want 200: %s", second.Code, second.Body.String())
	}
	secondJobID := jobIDFromUpdateAllResponse(t, second)
	if secondJobID == "" {
		t.Fatalf("second response missing job_id: %s", second.Body.String())
	}
	if secondJobID == firstJobID {
		t.Fatalf("expected a fresh job on second click, reused %q", firstJobID)
	}

	var count int64
	if err := db.Model(&orm.AsyncJob{}).
		Where("job_type = ? AND create_user_id = ?", updateAllJobType, "u1").
		Count(&count).Error; err != nil {
		t.Fatalf("count update-all jobs: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected history + fresh batch = 2 rows, got %d", count)
	}

	// The retired history row keeps its record but no longer holds the active
	// idempotency key, so a third click keeps creating fresh jobs.
	var active int64
	if err := db.Model(&orm.AsyncJob{}).
		Where("job_type = ? AND idempotency_key = ?", updateAllJobType, "kb_update_all:u1").
		Count(&active).Error; err != nil {
		t.Fatalf("count active-key jobs: %v", err)
	}
	if active != 1 {
		t.Fatalf("expected exactly one job holding the active key, got %d", active)
	}
}

func jobIDFromUpdateAllResponse(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Data struct {
			JobID string `json:"job_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.Data.JobID
}
