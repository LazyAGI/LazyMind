package evalset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func TestCreateImportAPIEnqueuesWithoutCreatingEvalSet(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	preview := seedImportPreviewRows(t, db, "import_tmp_create_api", "user_1", importFileTypeCSV, []ImportNormalizedRow{
		{Question: "q", GroundTruth: "a", QuestionType: "1"},
	}, true)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets:import", `{"name":"cases","description":"desc","dataset_ids":["dataset_1","dataset_2"],"group_id":"group_1","import_token":"import_tmp_create_api"}`, "user_1")
	CreateEvalSetByImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[CreateEvalSetByImportResponse](t, rec)
	if !strings.HasPrefix(resp.EvalSetID, "eval_set_") || !strings.HasPrefix(resp.TaskID, "job_") {
		t.Fatalf("unexpected response: %#v", resp)
	}

	var evalSetCount int64
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", resp.EvalSetID).Count(&evalSetCount).Error; err != nil {
		t.Fatalf("count eval sets: %v", err)
	}
	if evalSetCount != 0 {
		t.Fatalf("create import API must not create active eval set immediately, count=%d", evalSetCount)
	}

	var consumed orm.EvalSetImportPreview
	if err := db.First(&consumed, "token = ?", preview.Token).Error; err != nil {
		t.Fatalf("query consumed preview: %v", err)
	}
	if consumed.Status != importPreviewStatusConsumed || consumed.ConsumedAt == nil {
		t.Fatalf("expected consumed preview, got %#v", consumed)
	}

	var job orm.AsyncJob
	if err := db.First(&job, "id = ?", resp.TaskID).Error; err != nil {
		t.Fatalf("query async job: %v", err)
	}
	if job.JobType != importJobType || job.ResourceID != resp.EvalSetID || job.IdempotencyKey != "eval_set_import:"+preview.Token {
		t.Fatalf("unexpected job: %#v", job)
	}
}

func TestCreateImportAllowsEmptyDatasetIDs(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	seedImportPreviewRows(t, db, "import_tmp_create_no_kb", "user_1", importFileTypeCSV, []ImportNormalizedRow{
		{Question: "q", GroundTruth: "a", QuestionType: "1"},
	}, true)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets:import", `{"name":"cases without kb","import_token":"import_tmp_create_no_kb"}`, "user_1")
	CreateEvalSetByImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[CreateEvalSetByImportResponse](t, rec)

	runImportWorkerUntilDone(t, db, resp.TaskID, string(asyncjob.StatusSucceeded))

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", resp.EvalSetID).Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.Status != StatusActive || evalSet.ItemCount != 1 || len(parseDatasetIDsJSON(evalSet.DatasetIDs)) != 0 {
		t.Fatalf("unexpected eval set: %#v", evalSet)
	}
}

func TestCreateImportWorkerCreatesEvalSetAndUploadItems(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	seedImportPreviewRows(t, db, "import_tmp_create_worker", "user_1", importFileTypeCSV, []ImportNormalizedRow{
		{CaseID: "case_1", Question: "q1", GroundTruth: "a1", QuestionType: "1"},
		{Question: "q2", GroundTruth: "a2", QuestionType: "2", IsDeleted: true},
	}, true)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets:import", `{"name":"cases","description":"desc","dataset_ids":["dataset_1","dataset_2"],"group_id":"group_1","import_token":"import_tmp_create_worker"}`, "user_1")
	CreateEvalSetByImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[CreateEvalSetByImportResponse](t, rec)

	runImportWorkerUntilDone(t, db, resp.TaskID, string(asyncjob.StatusSucceeded))

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", resp.EvalSetID).Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.Status != StatusActive || evalSet.ItemCount != 2 || evalSet.Name != "cases" || strings.Join(parseDatasetIDsJSON(evalSet.DatasetIDs), ",") != "dataset_1,dataset_2" {
		t.Fatalf("unexpected eval set: %#v", evalSet)
	}

	var items []orm.EvalSetItem
	if err := db.Where("eval_set_id = ?", resp.EvalSetID).Order("question ASC").Find(&items).Error; err != nil {
		t.Fatalf("query items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	for _, item := range items {
		if item.Source != SourceUpload {
			t.Fatalf("expected upload source, got %#v", item)
		}
		if item.CaseID == "" || item.EstimatedBytes <= 0 {
			t.Fatalf("expected case_id and estimated bytes, got %#v", item)
		}
	}

	var shard orm.EvalSetShard
	if err := db.First(&shard, "id = ?", evalSet.ShardID).Error; err != nil {
		t.Fatalf("query shard: %v", err)
	}
	if shard.ActualRows != 2 || shard.EstimatedBytes <= 0 {
		t.Fatalf("expected shard counters updated, got rows=%d bytes=%d", shard.ActualRows, shard.EstimatedBytes)
	}
	assertImportTempRemoved(t, "import_tmp_create_worker")
}

func TestCreateImportWorkerAllowsZeroValidRows(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	seedImportPreviewRows(t, db, "import_tmp_zero_valid", "user_1", importFileTypeCSV, []ImportNormalizedRow{}, true)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets:import", `{"name":"empty valid cases","dataset_ids":["dataset_1"],"import_token":"import_tmp_zero_valid"}`, "user_1")
	CreateEvalSetByImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[CreateEvalSetByImportResponse](t, rec)
	job := runImportWorkerUntilDone(t, db, resp.TaskID, string(asyncjob.StatusSucceeded))
	if job.ProgressTotal != 0 || job.ProgressCurrent != 0 {
		t.Fatalf("expected zero progress for zero valid rows, got %#v", job)
	}

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", resp.EvalSetID).Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.ItemCount != 0 || evalSet.Name != "empty valid cases" {
		t.Fatalf("unexpected eval set: %#v", evalSet)
	}
	var itemCount int64
	if err := db.Model(&orm.EvalSetItem{}).Where("eval_set_id = ?", resp.EvalSetID).Count(&itemCount).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("expected zero imported items, got %d", itemCount)
	}
}

func TestAppendImportWorkerIncrementsItemCount(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	seedEvalSet(t, db, "eval_set_append", "user_1", "", "", time.Now().UTC())
	existing := seedEvalSetItem(t, db, orm.EvalSetItem{ID: "eval_item_existing", EvalSetID: "eval_set_append"})
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", "eval_set_append").Update("item_count", 1).Error; err != nil {
		t.Fatalf("update item_count: %v", err)
	}
	if err := db.Model(&orm.EvalSetShard{}).Where("id = ?", DefaultShardID).Updates(map[string]any{
		"actual_rows":     1,
		"estimated_bytes": existing.EstimatedBytes,
	}).Error; err != nil {
		t.Fatalf("update shard counters: %v", err)
	}
	seedImportPreviewRows(t, db, "import_tmp_append", "user_1", importFileTypeJSON, []ImportNormalizedRow{
		{Question: "q2", GroundTruth: "a2", QuestionType: "1"},
		{Question: "q3", GroundTruth: "a3", QuestionType: "1"},
	}, true)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets/eval_set_append/imports", `{"import_token":"import_tmp_append"}`, "user_1")
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": "eval_set_append"})
	AppendEvalSetImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[AppendEvalSetImportResponse](t, rec)

	runImportWorkerUntilDone(t, db, resp.TaskID, string(asyncjob.StatusSucceeded))

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", "eval_set_append").Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.ItemCount != 3 {
		t.Fatalf("expected item_count 3, got %d", evalSet.ItemCount)
	}
	var uploadCount int64
	if err := db.Model(&orm.EvalSetItem{}).Where("eval_set_id = ? AND source = ?", "eval_set_append", SourceUpload).Count(&uploadCount).Error; err != nil {
		t.Fatalf("count upload items: %v", err)
	}
	if uploadCount != 2 {
		t.Fatalf("expected 2 upload items, got %d", uploadCount)
	}
}

func TestCreateImportBatchInsertFailureRollsBackAllRowsAndEvalSet(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	seedImportPreviewRows(t, db, "import_tmp_insert_fail", "user_1", importFileTypeCSV, []ImportNormalizedRow{
		{Question: "q1", GroundTruth: "a1", QuestionType: "1"},
		{Question: "q2", GroundTruth: "a2", QuestionType: "1"},
	}, true)
	registerFailEvalSetItemCreateCallback(t, db)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets:import", `{"name":"cases","dataset_ids":["dataset_1"],"import_token":"import_tmp_insert_fail"}`, "user_1")
	CreateEvalSetByImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[CreateEvalSetByImportResponse](t, rec)

	job := runImportWorkerUntilDone(t, db, resp.TaskID, string(asyncjob.StatusFailed))
	if job.ErrorCode != importErrorInsertFailed {
		t.Fatalf("expected insert_failed, got %#v", job)
	}

	var evalSetCount, itemCount int64
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", resp.EvalSetID).Count(&evalSetCount).Error; err != nil {
		t.Fatalf("count eval sets: %v", err)
	}
	if err := db.Model(&orm.EvalSetItem{}).Where("eval_set_id = ?", resp.EvalSetID).Count(&itemCount).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if evalSetCount != 0 || itemCount != 0 {
		t.Fatalf("expected full rollback, eval sets=%d items=%d", evalSetCount, itemCount)
	}
}

func TestCreateImportMissingTempFileFailsWithoutFormalWrites(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	seedImportPreviewRows(t, db, "import_tmp_missing_temp", "user_1", importFileTypeCSV, []ImportNormalizedRow{
		{Question: "q", GroundTruth: "a", QuestionType: "1"},
	}, false)

	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets:import", `{"name":"cases","dataset_ids":["dataset_1"],"import_token":"import_tmp_missing_temp"}`, "user_1")
	CreateEvalSetByImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[CreateEvalSetByImportResponse](t, rec)

	job := runImportWorkerUntilDone(t, db, resp.TaskID, string(asyncjob.StatusFailed))
	if job.ErrorCode != importErrorTempFileMissing {
		t.Fatalf("expected temp_file_missing, got %#v", job)
	}
	var evalSetCount, itemCount int64
	if err := db.Model(&orm.EvalSet{}).Where("id = ?", resp.EvalSetID).Count(&evalSetCount).Error; err != nil {
		t.Fatalf("count eval sets: %v", err)
	}
	if err := db.Model(&orm.EvalSetItem{}).Where("eval_set_id = ?", resp.EvalSetID).Count(&itemCount).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if evalSetCount != 0 || itemCount != 0 {
		t.Fatalf("expected no formal writes, eval sets=%d items=%d", evalSetCount, itemCount)
	}
}

func TestTwoAppendImportJobsDoNotLoseItemCount(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)
	seedEvalSet(t, db, "eval_set_serial_append", "user_1", "", "", time.Now().UTC())
	seedImportPreviewRows(t, db, "import_tmp_serial_1", "user_1", importFileTypeCSV, []ImportNormalizedRow{
		{Question: "q1", GroundTruth: "a1", QuestionType: "1"},
	}, true)
	seedImportPreviewRows(t, db, "import_tmp_serial_2", "user_1", importFileTypeCSV, []ImportNormalizedRow{
		{Question: "q2", GroundTruth: "a2", QuestionType: "1"},
	}, true)

	firstID := enqueueAppendImportByHTTP(t, "eval_set_serial_append", "import_tmp_serial_1", "user_1")
	secondID := enqueueAppendImportByHTTP(t, "eval_set_serial_append", "import_tmp_serial_2", "user_1")

	runImportWorkerUntilDone(t, db, firstID, string(asyncjob.StatusSucceeded))
	waitJobStatus(t, db, secondID, string(asyncjob.StatusSucceeded))

	var evalSet orm.EvalSet
	if err := db.First(&evalSet, "id = ?", "eval_set_serial_append").Error; err != nil {
		t.Fatalf("query eval set: %v", err)
	}
	if evalSet.ItemCount != 2 {
		t.Fatalf("expected item_count 2, got %d", evalSet.ItemCount)
	}
}

func TestImportTaskForbiddenWithoutPermission(t *testing.T) {
	db := newEvalSetTestDB(t)
	seedEvalSet(t, db, "eval_set_private_task", "owner_1", "", "", time.Now().UTC())
	job, err := asyncjob.Enqueue(t.Context(), db.DB, asyncjob.EnqueueRequest{
		JobType:      importJobType,
		ResourceType: "eval_set",
		ResourceID:   "eval_set_private_task",
		Payload: EvalSetImportJobPayload{
			Mode:        importModeAppend,
			EvalSetID:   "eval_set_private_task",
			ImportToken: "import_tmp_private",
			TempPath:    filepath.Join(os.TempDir(), "missing.json"),
			FileName:    "private.csv",
			FileType:    importFileTypeCSV,
			TotalRows:   1,
			ValidRows:   1,
		},
		CreateUserID: "owner_1",
	})
	if err != nil {
		t.Fatalf("enqueue job: %v", err)
	}

	rec, req := requestWithUser(http.MethodGet, "/api/core/eval-set-import-tasks/"+job.ID, "", "user_2")
	req = mux.SetURLVars(req, map[string]string{"task_id": job.ID})
	GetEvalSetImportTask(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func enqueueAppendImportByHTTP(t *testing.T, evalSetID, token, userID string) string {
	t.Helper()
	body := fmt.Sprintf(`{"import_token":%q}`, token)
	rec, req := requestWithUser(http.MethodPost, "/api/core/eval-sets/"+evalSetID+"/imports", body, userID)
	req = mux.SetURLVars(req, map[string]string{"eval_set_id": evalSetID})
	AppendEvalSetImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected append status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return decodeOKData[AppendEvalSetImportResponse](t, rec).TaskID
}

func seedImportPreviewRows(t *testing.T, db *orm.DB, token, userID, fileType string, rows []ImportNormalizedRow, writeTemp bool) orm.EvalSetImportPreview {
	t.Helper()
	tempPath := tempPathForImportToken(token)
	if writeTemp {
		if err := os.MkdirAll(filepath.Dir(tempPath), 0700); err != nil {
			t.Fatalf("mkdir temp dir: %v", err)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatalf("marshal rows: %v", err)
		}
		if err := os.WriteFile(tempPath, raw, 0600); err != nil {
			t.Fatalf("write temp rows: %v", err)
		}
	}
	now := time.Now().UTC()
	preview := orm.EvalSetImportPreview{
		Token:          token,
		Status:         importPreviewStatusReady,
		FileName:       token + "." + fileType,
		FileType:       fileType,
		TempPath:       tempPath,
		TotalRows:      int64(len(rows)),
		ValidRows:      int64(len(rows)),
		CreateUserID:   userID,
		CreateUserName: userID + " name",
		CreatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	}
	if err := db.Create(&preview).Error; err != nil {
		t.Fatalf("seed import preview: %v", err)
	}
	return preview
}

func runImportWorkerUntilDone(t *testing.T, db *orm.DB, jobID, wantStatus string) orm.AsyncJob {
	t.Helper()
	RegisterAsyncJobs()
	ctx, cancel := context.WithCancel(context.Background())
	runner := asyncjob.Start(ctx, db.DB, asyncjob.Options{
		WorkerID:     "evalset-test-worker",
		Concurrency:  1,
		PollInterval: 10 * time.Millisecond,
		LockTTL:      time.Minute,
	})
	t.Cleanup(func() {
		cancel()
		<-runner.Done()
	})
	return waitJobStatus(t, db, jobID, wantStatus)
}

func waitJobStatus(t *testing.T, db *orm.DB, jobID, wantStatus string) orm.AsyncJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var job orm.AsyncJob
		if err := db.First(&job, "id = ?", jobID).Error; err != nil {
			t.Fatalf("query job: %v", err)
		}
		if job.Status == wantStatus {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	var job orm.AsyncJob
	_ = db.First(&job, "id = ?", jobID).Error
	t.Fatalf("job %s did not reach %s, last=%#v", jobID, wantStatus, job)
	return job
}

func registerFailEvalSetItemCreateCallback(t *testing.T, db *orm.DB) {
	t.Helper()
	name := "evalset_test_fail_eval_set_items"
	err := db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "eval_set_items" {
			tx.AddError(errors.New("forced eval_set_items insert failure"))
		}
	})
	if err != nil {
		t.Fatalf("register create callback: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(name)
	})
}

func assertImportTempRemoved(t *testing.T, token string) {
	t.Helper()
	if _, err := os.Stat(tempPathForImportToken(token)); !os.IsNotExist(err) {
		t.Fatalf("expected temp file removed for %s, stat err=%v", token, err)
	}
}
