package knowledge_market

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

var errMarketBusy = doc.ErrMarketProcessing
var errMarketNotInstalled = errors.New("knowledge base is not installed")

func parseIsActive(p parseProgressInfo) bool {
	return p.Total > 0 && (p.State == "pending" || p.State == "parsing" || p.State == "unknown")
}

func canRetryMarketParse(p parseProgressInfo) bool {
	for _, failure := range p.Failures {
		if failure.TaskID == "" || failure.Reason == "missing_task" {
			return false
		}
	}
	return true
}

// Re-download only when a failed file never reached task registration. Keep
// existing documents, resubmit their failed tasks and import only missing files.
func repairMarketImport(ctx context.Context, db *gorm.DB, install *orm.KnowledgeMarketInstall, userName string, files []doc.MarketImportFile) (*doc.MarketImportResult, error) {
	cfg := decodeInstallConfig(install)
	var tasks []orm.Task
	if err := db.WithContext(ctx).Where("id IN ? AND dataset_id = ? AND deleted_at IS NULL", cfg.TaskIDs, install.DatasetID).Find(&tasks).Error; err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(tasks))
	result := &doc.MarketImportResult{DatasetID: install.DatasetID}
	for _, task := range tasks {
		var ext struct {
			Files []struct {
				RelativePath string `json:"relative_path"`
			} `json:"files"`
		}
		_ = json.Unmarshal(task.Ext, &ext)
		relativePath := ""
		if len(ext.Files) > 0 {
			relativePath = ext.Files[0].RelativePath
		}
		present[relativePath+"/"+task.DisplayName] = true
		result.TaskIDs = append(result.TaskIDs, task.ID)
	}
	parse := parseProgress((&http.Request{}).WithContext(ctx), db, install)
	var retryIDs []string
	for _, failure := range parse.Failures {
		if failure.TaskID != "" && failure.Reason != "missing_task" {
			retryIDs = append(retryIDs, failure.TaskID)
		}
	}
	result.DispatchedTaskIDs = append([]string(nil), retryIDs...)
	if err := doc.MarketImportCheckpoint(ctx, result); err != nil {
		return result, err
	}
	if len(retryIDs) > 0 {
		var err error
		result.Submitted, err = doc.RetryMarketTasks(ctx, install.DatasetID, install.UserID, userName, retryIDs)
		if err != nil {
			return result, err
		}
	}
	var missing []doc.MarketImportFile
	names := make(map[string]bool, len(files))
	for _, file := range files {
		names[file.DisplayName] = true
		if !present[file.RelativePath+"/"+file.DisplayName] {
			missing = append(missing, file)
		}
	}
	for _, failure := range cfg.Failures {
		if !names[failure.Name] {
			result.Failures = append(result.Failures, failure)
		}
	}
	if len(missing) > 0 {
		var ds orm.Dataset
		if err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", install.DatasetID).Take(&ds).Error; err != nil {
			return result, err
		}
		added, err := doc.ImportMarketFiles(ctx, &ds, install.UserID, userName, missing)
		if added != nil {
			result.TaskIDs = append(result.TaskIDs, added.TaskIDs...)
			result.Failures = append(result.Failures, added.Failures...)
			result.Submitted += added.Submitted
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// Serialize item actions and freeze terminal history before any worker changes
// the shared dataset. ResultJSON carries the snapshot; no schema migration.
func enqueueMarketItem(ctx context.Context, db *gorm.DB, req asyncjob.EnqueueRequest, parentJobID string) (*orm.AsyncJob, error) {
	var job *orm.AsyncJob
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item orm.KnowledgeMarketItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", req.ResourceID).Take(&item).Error; err != nil {
			return err
		}
		install, err := doc.LockMarketInstall(ctx, tx, req.CreateUserID, req.ResourceID)
		if err != nil {
			return err
		}
		// The HTTP preflight may predate a concurrent uninstall. Revalidate
		// after acquiring the lock rather than enqueue against that stale view.
		if req.JobType == updateJobType && (install == nil || install.DatasetID == "") {
			return errMarketNotInstalled
		}
		var active int64
		if err := tx.Model(&orm.AsyncJob{}).Where("create_user_id = ? AND (status IN ? OR (status = 'canceled' AND lock_until > ?)) AND id <> ? AND ((job_type IN ? AND resource_id = ?) OR job_type = ?)", req.CreateUserID, []string{"pending", "running"}, time.Now().UTC(), parentJobID, []string{installJobType, updateJobType}, req.ResourceID, updateAllJobType).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return errMarketBusy
		}
		if err := doc.SnapshotMarketTaskHistory(ctx, tx, install); err != nil {
			return err
		}
		if err := tx.Model(&orm.AsyncJob{}).Where("job_type = ? AND idempotency_key = ? AND create_user_id = ? AND status IN ?", req.JobType, req.IdempotencyKey, req.CreateUserID, []string{"succeeded", "failed", "canceled"}).Update("idempotency_key", gorm.Expr("? || id", "kb_history:")).Error; err != nil {
			return err
		}
		job, err = asyncjob.EnqueueInTransaction(ctx, tx, req)
		return err
	})
	return job, err
}

// Retry a terminal parse result without deleting successful documents. The
// current install keeps all files, while the new job tracks this retry's work.
func retryMarketParse(ctx context.Context, db *gorm.DB, install *orm.KnowledgeMarketInstall, userName string) (asyncjob.Result, error) {
	cfg := decodeInstallConfig(install)
	parse := parseProgress((&http.Request{}).WithContext(ctx), db, install)
	var ids []string
	for _, failure := range parse.Failures {
		if failure.TaskID != "" && failure.Reason != "missing_task" {
			ids = append(ids, failure.TaskID)
		}
	}
	if err := doc.MarketImportCheckpoint(ctx, &doc.MarketImportResult{DatasetID: install.DatasetID, TaskIDs: cfg.TaskIDs, Failures: cfg.Failures, DispatchedTaskIDs: ids}); err != nil {
		return asyncjob.Result{}, err
	}
	submitted := 0
	if len(ids) > 0 {
		var err error
		submitted, err = doc.RetryMarketTasks(ctx, install.DatasetID, install.UserID, userName, ids)
		if err != nil {
			return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
		}
	}
	if err := resetInstallStateOnly(ctx, db, install.MarketItemID, install.UserID, orm.InstallStateDone); err != nil {
		return asyncjob.Result{}, err
	}
	if parse.State == "done" || parse.Total == 0 {
		return noChangeUpdateResult()
	}
	result, _ := json.Marshal(doc.MarketImportResult{DatasetID: install.DatasetID, Submitted: submitted, TaskIDs: cfg.TaskIDs, Failures: cfg.Failures})
	return asyncjob.Result{ResultJSON: result}, nil
}

func loadActionTask(w http.ResponseWriter, r *http.Request, db *gorm.DB) (*orm.AsyncJob, *orm.KnowledgeMarketInstall, parseProgressInfo, bool) {
	userID := strings.TrimSpace(common.UserID(r))
	if userID == "" {
		common.ReplyAppErr(w, common.NewAppError(http.StatusUnauthorized, common.ErrCodeUnauthorized, "Authentication required"))
		return nil, nil, parseProgressInfo{}, false
	}
	var job orm.AsyncJob
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND job_type IN ?", common.PathVar(r, "job_id"), userID, marketJobTypes).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "knowledge market task not found", http.StatusNotFound)
		} else {
			replyServiceError(w, err)
		}
		return nil, nil, parseProgressInfo{}, false
	}
	install, err := loadInstall(r, db, userID, job.ResourceID)
	if err != nil {
		replyServiceError(w, err)
		return nil, nil, parseProgressInfo{}, false
	}
	_, parse := taskProgress(r, db, job, install)
	if job.Status == "pending" || job.Status == "running" || marketDraining(job) || parseIsActive(parse) {
		common.ReplyAppErr(w, common.NewAppError(http.StatusConflict, common.ErrCodeConflict, "Task is still processing"))
		return nil, nil, parseProgressInfo{}, false
	}
	return &job, install, parse, true
}

func MarketDeleteTask(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	job, _, _, ok := loadActionTask(w, r, db)
	if !ok {
		return
	}
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND status NOT IN ?", job.ID, common.UserID(r), []string{"pending", "running"}).Delete(&orm.AsyncJob{}).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"job_id": job.ID})
}

func MarketRetryTask(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	job, install, parse, ok := loadActionTask(w, r, db)
	if !ok {
		return
	}
	if install != nil && install.DatasetID != "" && !doc.RequireMarketDatasetPermission(w, r, install.DatasetID) {
		return
	}
	if marketWorkerHealth(r.Context()) != "available" {
		marketUnavailable(w)
		return
	}
	if job.Status != "failed" && parse.Failed == 0 {
		common.ReplyAppErr(w, common.NewAppError(http.StatusConflict, common.ErrCodeConflict, "Only failed tasks can be retried"))
		return
	}
	var newer int64
	if err := db.WithContext(r.Context()).Model(&orm.AsyncJob{}).Where("create_user_id = ? AND resource_id = ? AND job_type IN ? AND (created_at > ? OR (created_at = ? AND id > ?))", job.CreateUserID, job.ResourceID, marketJobTypes, job.CreatedAt, job.CreatedAt, job.ID).Count(&newer).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	if newer > 0 {
		common.ReplyAppErr(w, common.NewAppError(http.StatusConflict, common.ErrCodeConflict, "A newer task exists; retry the latest task"))
		return
	}
	if job.JobType == updateAllJobType {
		MarketUpdateAll(w, r)
		return
	}
	jobType := job.JobType
	var original installJobPayload
	_ = json.Unmarshal(job.PayloadJSON, &original)
	var payload any = installJobPayload{MarketItemID: job.ResourceID, UserID: common.UserID(r), UserName: common.UserName(r), Revision: original.Revision}
	if job.JobType == updateJobType || ((job.Status == "succeeded" || job.Status == "canceled") && install != nil && install.DatasetID != "") {
		jobType = updateJobType
		var update updateJobPayload
		_ = json.Unmarshal(job.PayloadJSON, &update)
		update.MarketItemID, update.UserID, update.UserName = job.ResourceID, common.UserID(r), common.UserName(r)
		// Only a successful submission with failed files is a parse-only retry.
		// Download/materialization failures must replay their original pipeline.
		update.RetryOnly = (job.Status == "succeeded" || job.Status == "canceled") && parse.Failed > 0
		payload = update
	}
	newJob, err := enqueueMarketItem(r.Context(), db, asyncjob.EnqueueRequest{JobType: jobType, ResourceType: "knowledge_market_item", ResourceID: job.ResourceID, IdempotencyKey: "kb_retry:" + job.ResourceID + ":" + common.UserID(r), Payload: payload, MaxAttempts: 2, CreateUserID: common.UserID(r), CreateUserName: common.UserName(r)}, "")
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"job_id": newJob.ID, "state": newJob.Status})
}

// Batch retries also retain the failed attempt instead of resetting its row.
func enqueueMarketBatch(ctx context.Context, db *gorm.DB, req asyncjob.EnqueueRequest) (*orm.AsyncJob, error) {
	var job *orm.AsyncJob
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item orm.KnowledgeMarketItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").First(&item).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		active, err := doc.HasActiveMarketBatch(ctx, tx, req.CreateUserID)
		if err != nil {
			return err
		}
		if active {
			return errMarketBusy
		}
		if err := tx.Model(&orm.AsyncJob{}).Where("job_type = ? AND idempotency_key = ? AND create_user_id = ? AND status IN ?", updateAllJobType, req.IdempotencyKey, req.CreateUserID, []string{"succeeded", "failed", "canceled"}).Update("idempotency_key", gorm.Expr("? || id", "kb_history:")).Error; err != nil {
			return err
		}
		job, err = asyncjob.EnqueueInTransaction(ctx, tx, req)
		return err
	})
	return job, err
}
