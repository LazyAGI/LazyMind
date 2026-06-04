package evalset

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func TestEvalSetIntegrationManualMaintenance(t *testing.T) {
	db := newEvalSetTestDB(t)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets", `{"name":"manual cases","dataset_ids":["dataset_1"]}`, "u1")
	CreateEvalSet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected create eval set status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	evalSet := decodeOKData[EvalSetResponse](t, rec)

	rec, req = requestWithUser(http.MethodPost, "/api/core/eval-sets/"+evalSet.ID+"/items", `{"case_id":"case_1","question":"q1","ground_truth":"a1","question_type":"type_1"}`, "u1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": evalSet.ID})
	CreateEvalSetItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected create item status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	item := decodeOKData[EvalSetItemResponse](t, rec)
	if item.Source != SourceManual {
		t.Fatalf("expected manual source, got %#v", item)
	}

	rec, req = requestWithUser(http.MethodGet, "/api/core/eval-sets/"+evalSet.ID+"/items", "", "u1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": evalSet.ID})
	ListEvalSetItems(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	list := decodeOKData[ListEvalSetItemsResponse](t, rec)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != item.ID {
		t.Fatalf("expected created item in list, got %#v", list)
	}

	rec, req = requestWithUser(http.MethodPatch, "/api/core/eval-sets/"+evalSet.ID+"/items/"+item.ID, `{"question":"q1 updated","ground_truth":"a1 updated"}`, "u1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": evalSet.ID, "item_id": item.ID})
	UpdateEvalSetItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	updated := decodeOKData[EvalSetItemResponse](t, rec)
	if updated.Question != "q1 updated" || updated.GroundTruth != "a1 updated" {
		t.Fatalf("unexpected updated item: %#v", updated)
	}

	rec, req = requestWithUser(http.MethodPost, "/api/core/eval-sets/"+evalSet.ID+"/items:batchDelete", `{"item_ids":["`+item.ID+`"]}`, "u1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": evalSet.ID})
	BatchDeleteEvalSetItems(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected batch delete status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var row orm.EvalSet
	if err := db.First(&row, "id = ?", evalSet.ID).Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if row.ItemCount != 0 {
		t.Fatalf("expected item_count back to 0, got %d", row.ItemCount)
	}
}

func TestEvalSetIntegrationCSVPreviewFailure(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	rec, req := multipartImportRequest(t, "missing-question.csv", "", "question,ground_truth,question_type\n,answer,type\n", "u1")
	PreviewEvalSetImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected preview status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if resp.ValidRows != 0 || resp.InvalidRows != 1 || resp.InvalidRowsDownloadURL == "" {
		t.Fatalf("unexpected invalid-only preview response: %#v", resp)
	}
	if len(resp.ErrorDetails) != 1 || resp.ErrorDetails[0].Row != 2 || resp.ErrorDetails[0].Column != "question" {
		t.Fatalf("expected row 2 question error, got %#v", resp.ErrorDetails)
	}

	var previewCount, itemCount int64
	if err := db.Model(&orm.EvalSetImportPreview{}).Count(&previewCount).Error; err != nil {
		t.Fatalf("count previews: %v", err)
	}
	if err := db.Model(&orm.EvalSetItem{}).Count(&itemCount).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if previewCount != 1 || itemCount != 0 {
		t.Fatalf("expected one preview and no item writes, previews=%d items=%d", previewCount, itemCount)
	}
}

func TestEvalSetIntegrationCSVCreateImportSuccess(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	csv := "question,ground_truth,question_type\nq1,a1,type\nq2,a2,type\n"
	rec, req := multipartImportRequest(t, "cases.csv", "", csv, "u1")
	PreviewEvalSetImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected preview status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	preview := decodeOKData[ImportPreviewResponse](t, rec)

	rec, req = requestWithUser(http.MethodPost, "/api/core/eval-sets:import", `{"name":"csv cases","dataset_ids":["dataset_1"],"import_token":"`+preview.ImportToken+`"}`, "u1")
	CreateEvalSetByImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected import status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	importResp := decodeOKData[CreateEvalSetByImportResponse](t, rec)

	job := runImportWorkerUntilDone(t, db, importResp.TaskID, string(asyncjob.StatusSucceeded))
	if job.Status != string(asyncjob.StatusSucceeded) {
		t.Fatalf("expected succeeded job, got %#v", job)
	}

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", importResp.EvalSetID).Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.ItemCount != 2 {
		t.Fatalf("expected item_count 2, got %d", evalSet.ItemCount)
	}
	var uploadCount int64
	if err := db.Model(&orm.EvalSetItem{}).
		Where("eval_set_id = ? AND source = ?", importResp.EvalSetID, SourceUpload).
		Count(&uploadCount).Error; err != nil {
		t.Fatalf("count upload items: %v", err)
	}
	if uploadCount != 2 {
		t.Fatalf("expected all imported items source upload, got %d", uploadCount)
	}
}

func TestEvalSetIntegrationAppendImportRollback(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	seedEvalSet(t, db, "eval_set_append_rollback", "u1", "", "", time.Now().UTC())
	seedImportPreviewRows(t, db, "import_tmp_append_rollback", "u1", importFileTypeCSV, []ImportNormalizedRow{
		{Question: "q1", GroundTruth: "a1", QuestionType: "type"},
		{Question: "q2", GroundTruth: "a2", QuestionType: "type"},
	}, true)
	registerFailEvalSetItemCreateCallback(t, db)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_append_rollback/imports", `{"import_token":"import_tmp_append_rollback"}`, "u1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_append_rollback"})
	AppendEvalSetImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected append status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[AppendEvalSetImportResponse](t, rec)

	job := runImportWorkerUntilDone(t, db, resp.TaskID, string(asyncjob.StatusFailed))
	if job.ErrorCode != importErrorInsertFailed {
		t.Fatalf("expected insert_failed, got %#v", job)
	}

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", "eval_set_append_rollback").Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.ItemCount != 0 {
		t.Fatalf("expected item_count unchanged, got %d", evalSet.ItemCount)
	}
	var itemCount int64
	if err := db.Model(&orm.EvalSetItem{}).Where("eval_set_id = ?", "eval_set_append_rollback").Count(&itemCount).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("expected no partial rows, got %d", itemCount)
	}
}

func TestEvalSetIntegrationPermissions(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_permissions", "u1", "", "", time.Now().UTC())
	seedImportPreviewRows(t, db, "import_tmp_forbidden_append", "u2", importFileTypeCSV, []ImportNormalizedRow{
		{Question: "q", GroundTruth: "a", QuestionType: "type"},
	}, true)

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_permissions", "", "u2")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_permissions"})
	GetEvalSet(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected detail forbidden, got %d: %s", rec.Code, rec.Body.String())
	}

	rec, req = requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_permissions/items", "", "u2")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_permissions"})
	ListEvalSetItems(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected item list forbidden, got %d: %s", rec.Code, rec.Body.String())
	}

	rec, req = requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_permissions/imports", `{"import_token":"import_tmp_forbidden_append"}`, "u2")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_permissions"})
	AppendEvalSetImport(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected append forbidden, got %d: %s", rec.Code, rec.Body.String())
	}

	if id := acl.GetStore().AddACL(acl.ResourceTypeEvalSet, "eval_set_permissions", acl.GranteeUser, "u2", acl.PermissionEvalSetRead, "u1", nil); id == 0 {
		t.Fatalf("expected read ACL row")
	}

	rec, req = requestWithUser(http.MethodGet, "/api/core/eval-sets/eval_set_permissions", "", "u2")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_permissions"})
	GetEvalSet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected detail allowed with read ACL, got %d: %s", rec.Code, rec.Body.String())
	}

	rec, req = requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_permissions/items", `{"question":"q","ground_truth":"a","question_type":"type"}`, "u2")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_permissions"})
	CreateEvalSetItem(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected write forbidden with read ACL, got %d: %s", rec.Code, rec.Body.String())
	}

	if id := acl.GetStore().AddACL(acl.ResourceTypeEvalSet, "eval_set_permissions", acl.GranteeUser, "u2", acl.PermissionEvalSetWrite, "u1", nil); id == 0 {
		t.Fatalf("expected write ACL row")
	}

	rec, req = requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_permissions/items", `{"question":"q","ground_truth":"a","question_type":"type"}`, "u2")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_permissions"})
	CreateEvalSetItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected create allowed with write ACL, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestImportRuntimeConfigDefaultsAndEnvFallbacks(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("EVAL_SET_IMPORT_TEMP_DIR", "")
	t.Setenv("EVAL_SET_IMPORT_PREVIEW_TTL", "bad")
	t.Setenv("EVAL_SET_IMPORT_CLEANUP_INTERVAL", "bad")
	t.Setenv("EVAL_SET_IMPORT_TASK_RETENTION", "bad")
	t.Setenv("EVAL_SET_IMPORT_MAX_FILE_SIZE", "bad")
	t.Setenv("EVAL_SET_IMPORT_MAX_ROWS", "bad")
	t.Setenv("ASYNC_JOB_CONCURRENCY", "bad")
	t.Setenv("ASYNC_JOB_POLL_INTERVAL", "bad")
	t.Setenv("ASYNC_JOB_LOCK_TTL", "bad")

	importConfig := LoadImportRuntimeConfigFromEnv()
	if importConfig.TempDir != filepath.Join(os.TempDir(), "lazymind", "eval-set-import") {
		t.Fatalf("unexpected temp dir: %q", importConfig.TempDir)
	}
	if importConfig.PreviewTTL != 2*time.Hour ||
		importConfig.CleanupInterval != 30*time.Minute ||
		importConfig.TaskRetention != 30*24*time.Hour ||
		importConfig.MaxFileSize != 20*1024*1024 ||
		importConfig.MaxRows != 50000 {
		t.Fatalf("unexpected import config defaults: %#v", importConfig)
	}

	asyncConfig := LoadAsyncJobRuntimeConfigFromEnv()
	if asyncConfig.Concurrency != 2 || asyncConfig.PollInterval != 2*time.Second || asyncConfig.LockTTL != 10*time.Minute {
		t.Fatalf("unexpected async config defaults: %#v", asyncConfig)
	}
}

func TestImportRuntimeConfigCleanupIntervalAlias(t *testing.T) {
	t.Setenv("EVAL_SET_IMPORT_CLEANUP_INTERVAL", "")
	t.Setenv("EVAL_SET_IMPORT_CLEAN_INTERVAL", "7m")

	config := LoadImportRuntimeConfigFromEnv()
	if config.CleanupInterval != 7*time.Minute {
		t.Fatalf("expected legacy cleanup interval alias, got %v", config.CleanupInterval)
	}
}

func TestImportCleanupRemovesOnlySafeConsumedPreviewsAndOldTerminalJobs(t *testing.T) {
	db := newEvalSetTestDB(t)
	tempDir := withTempImportDir(t)
	now := time.Now().UTC()

	safePath := writeCleanupTempFile(t, tempDir, "import_tmp_safe")
	activeTokenPath := writeCleanupTempFile(t, tempDir, "import_tmp_active_token")
	activePathOnlyPath := writeCleanupTempFile(t, tempDir, "import_tmp_active_path")
	readyExpiredPath := writeCleanupTempFile(t, tempDir, "import_tmp_ready_expired")

	seedCleanupPreview(t, db, "import_tmp_safe", importPreviewStatusConsumed, safePath, now.Add(-25*time.Hour), now.Add(time.Hour))
	seedCleanupPreview(t, db, "import_tmp_active_token", importPreviewStatusConsumed, activeTokenPath, now.Add(-25*time.Hour), now.Add(time.Hour))
	seedCleanupPreview(t, db, "import_tmp_active_path", importPreviewStatusConsumed, activePathOnlyPath, now.Add(-25*time.Hour), now.Add(time.Hour))
	seedCleanupPreview(t, db, "import_tmp_ready_expired", importPreviewStatusReady, readyExpiredPath, now.Add(-3*time.Hour), now.Add(-time.Minute))

	seedImportJob(t, db, "job_active_token", string(asyncjob.StatusPending), now, EvalSetImportJobPayload{
		Mode:        importModeAppend,
		EvalSetID:   "eval_set_cleanup",
		ImportToken: "import_tmp_active_token",
		TempPath:    filepath.Join(tempDir, "unused.json"),
		ValidRows:   1,
	})
	seedImportJob(t, db, "job_active_path", string(asyncjob.StatusRunning), now, EvalSetImportJobPayload{
		Mode:        importModeAppend,
		EvalSetID:   "eval_set_cleanup",
		ImportToken: "other_token",
		TempPath:    activePathOnlyPath,
		ValidRows:   1,
	})
	oldFinished := now.Add(-31 * 24 * time.Hour)
	recentFinished := now.Add(-time.Hour)
	seedImportJobWithFinishedAt(t, db, "job_old_succeeded", string(asyncjob.StatusSucceeded), now.Add(-32*24*time.Hour), &oldFinished)
	seedImportJobWithFinishedAt(t, db, "job_old_failed", string(asyncjob.StatusFailed), now.Add(-32*24*time.Hour), &oldFinished)
	seedImportJobWithFinishedAt(t, db, "job_old_canceled", string(asyncjob.StatusCanceled), now.Add(-32*24*time.Hour), &oldFinished)
	seedImportJobWithFinishedAt(t, db, "job_recent_succeeded", string(asyncjob.StatusSucceeded), now.Add(-2*time.Hour), &recentFinished)
	seedImportJobWithFinishedAt(t, db, "job_old_pending", string(asyncjob.StatusPending), now.Add(-32*24*time.Hour), &oldFinished)

	if err := CleanupExpiredImportPreviews(t.Context(), db.DB, now); err != nil {
		t.Fatalf("cleanup expired previews: %v", err)
	}
	if err := CleanupConsumedImportPreviews(t.Context(), db.DB, now); err != nil {
		t.Fatalf("cleanup consumed previews: %v", err)
	}
	if err := CleanupTerminalImportJobs(t.Context(), db.DB, now, 30*24*time.Hour); err != nil {
		t.Fatalf("cleanup terminal import jobs: %v", err)
	}

	assertPreviewMissing(t, db, "import_tmp_safe")
	assertPreviewStatus(t, db, "import_tmp_active_token", importPreviewStatusConsumed)
	assertPreviewStatus(t, db, "import_tmp_active_path", importPreviewStatusConsumed)
	assertPreviewStatus(t, db, "import_tmp_ready_expired", importPreviewStatusExpired)
	assertFileMissing(t, safePath)
	assertFileExists(t, activeTokenPath)
	assertFileExists(t, activePathOnlyPath)
	assertFileMissing(t, readyExpiredPath)

	for _, id := range []string{"job_old_succeeded", "job_old_failed", "job_old_canceled"} {
		assertJobMissing(t, db, id)
	}
	for _, id := range []string{"job_recent_succeeded", "job_old_pending", "job_active_token", "job_active_path"} {
		assertJobExists(t, db, id)
	}
}

func writeCleanupTempFile(t *testing.T, tempDir, token string) string {
	t.Helper()
	path := filepath.Join(tempDir, "lazymind", "eval-set-import", token+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir temp dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("[]"), 0600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func seedCleanupPreview(t *testing.T, db *orm.DB, token, status, tempPath string, createdAt, expiresAt time.Time) {
	t.Helper()
	row := orm.EvalSetImportPreview{
		Token:          token,
		Status:         status,
		FileName:       token + ".csv",
		FileType:       importFileTypeCSV,
		TempPath:       tempPath,
		TotalRows:      1,
		ValidRows:      1,
		CreateUserID:   "u1",
		CreateUserName: "u1 name",
		CreatedAt:      createdAt,
		ExpiresAt:      expiresAt,
	}
	if status == importPreviewStatusConsumed {
		consumedAt := createdAt.Add(time.Minute)
		row.ConsumedAt = &consumedAt
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed preview %s: %v", token, err)
	}
}

func seedImportJob(t *testing.T, db *orm.DB, id, status string, now time.Time, payload EvalSetImportJobPayload) {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	row := orm.AsyncJob{
		ID:          id,
		JobType:     importJobType,
		Status:      status,
		PayloadJSON: payloadJSON,
		MaxAttempts: 1,
		NextRunAt:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed job %s: %v", id, err)
	}
}

func seedImportJobWithFinishedAt(t *testing.T, db *orm.DB, id, status string, createdAt time.Time, finishedAt *time.Time) {
	t.Helper()
	row := orm.AsyncJob{
		ID:          id,
		JobType:     importJobType,
		Status:      status,
		MaxAttempts: 1,
		NextRunAt:   createdAt,
		FinishedAt:  finishedAt,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed job %s: %v", id, err)
	}
}

func assertPreviewMissing(t *testing.T, db *orm.DB, token string) {
	t.Helper()
	var count int64
	if err := db.Model(&orm.EvalSetImportPreview{}).Where("token = ?", token).Count(&count).Error; err != nil {
		t.Fatalf("count preview %s: %v", token, err)
	}
	if count != 0 {
		t.Fatalf("expected preview %s removed, count=%d", token, count)
	}
}

func assertPreviewStatus(t *testing.T, db *orm.DB, token, status string) {
	t.Helper()
	var row orm.EvalSetImportPreview
	if err := db.First(&row, "token = ?", token).Error; err != nil {
		t.Fatalf("query preview %s: %v", token, err)
	}
	if row.Status != status {
		t.Fatalf("expected preview %s status %s, got %s", token, status, row.Status)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file %s removed, stat err=%v", path, err)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %s to exist: %v", path, err)
	}
}

func assertJobMissing(t *testing.T, db *orm.DB, id string) {
	t.Helper()
	var count int64
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("count job %s: %v", id, err)
	}
	if count != 0 {
		t.Fatalf("expected job %s removed, count=%d", id, count)
	}
}

func assertJobExists(t *testing.T, db *orm.DB, id string) {
	t.Helper()
	var count int64
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("count job %s: %v", id, err)
	}
	if count != 1 {
		t.Fatalf("expected job %s to remain, count=%d", id, count)
	}
}
