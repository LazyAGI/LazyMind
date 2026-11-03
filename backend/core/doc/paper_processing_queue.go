package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
	"lazymind/core/store"
	"time"
)

const paperProcessingJob = "document.paper_processing"

type paperProcessingWorker struct{}
type paperProcessingPayload struct {
	DatasetID, TaskID, UserID, Level string
	Automatic                        bool
}

func init() { asyncjob.Register(paperProcessingJob, runPaperProcessing) }

// Persist submission separately from ingestion so parser availability cannot
// hold an already-saved paper in the import dialog.
func queuePaperProcessing(ctx context.Context, db *gorm.DB, user, dataset string, tasks []string, level string) ([]StartTaskResult, error) {
	results := make([]StartTaskResult, 0, len(tasks))
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seen := map[string]bool{}
		for _, id := range tasks {
			if seen[id] {
				continue
			}
			seen[id] = true
			var task orm.Task
			if err := tx.Where("id=? AND dataset_id=? AND deleted_at IS NULL", id, dataset).Take(&task).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&orm.Document{}).Where("id=? AND dataset_id=? AND deleted_at IS NULL", task.DocID, dataset).Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("document must be saved before processing is queued")
			}
			_, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{
				JobType: paperProcessingJob, ResourceType: "document", ResourceID: task.DocID,
				IdempotencyKey: "paper-processing:" + id, MaxAttempts: 1, SkipSucceeded: true,
				Payload: paperProcessingPayload{DatasetID: dataset, TaskID: id, UserID: user, Level: level, Automatic: readingpolicy.IsAutomatic(ctx)}, CreateUserID: user,
			})
			if err != nil {
				return err
			}
			results = append(results, StartTaskResult{TaskID: id, DocumentID: task.DocID, DisplayName: task.DisplayName, Status: "STARTED", SubmitStatus: "ACCEPTED", Message: "document saved; processing queued"})
		}
		return nil
	})
	return results, err
}
func runPaperProcessing(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (result asyncjob.Result, resultErr error) {
	var p paperProcessingPayload
	if err := json.Unmarshal(job.PayloadJSON, &p); err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	// Every execution failure, including revoked access and missing identity,
	// must release the document's parsing latch as well as fail the queue job.
	defer func() {
		if resultErr == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var task orm.Task
		db := store.DB().WithContext(cleanup)
		if db.Where("id=? AND dataset_id=? AND deleted_at IS NULL", p.TaskID, p.DatasetID).Take(&task).Error != nil {
			return
		}
		markTaskStartFailed(cleanup, p.DatasetID, task, resultErr.Error())
		// A late failure must not overwrite a newer attempt's progress.
		newer := db.Model(&orm.Task{}).Select("1").Where("dataset_id=? AND doc_id=? AND deleted_at IS NULL AND created_at>? AND task_type IN ?", p.DatasetID, task.DocID, task.CreatedAt, []string{string(TaskTypeParse), string(TaskTypeParseUploaded), string(TaskTypeReparse)})
		db.Model(&orm.DocumentProcessingState{}).Where("dataset_id=? AND document_id=? AND parse_status=?", p.DatasetID, task.DocID, "running").Where("NOT EXISTS (?)", newer).
			Updates(map[string]any{"parse_status": "failed", "parse_error_code": "READER_SUBMIT_FAILED", "parse_error_message": resultErr.Error(), "updated_at": time.Now().UTC()})
	}()
	if p.Automatic {
		ctx = readingpolicy.AutomaticContext(ctx)
	}
	if _, err := preparationRequestWithPermission(ctx, p.UserID, readingPreparationPayload{DatasetID: p.DatasetID}, acl.PermissionDatasetRead); err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	// Before bounded preparation existed, import-origin jobs queued the empty
	// requested level and inherited the dataset default. Explicit ensure and the
	// bounded chat-preparation worker always request ProcessingLevelParsed.
	// Retire those legacy paper import jobs without starting paid work on upgrade.
	if p.Level == "" {
		var dataset orm.Dataset
		if err := store.DB().WithContext(ctx).Where("id=? AND deleted_at IS NULL", p.DatasetID).Take(&dataset).Error; err != nil {
			return asyncjob.Result{Permanent: true}, err
		}
		if dataset.Scene == "academic_papers" {
			if _, err := SubmitOriginalTask(ctx, DatasetCatalogCaller{UserID: p.UserID}, p.DatasetID, p.TaskID); err != nil {
				return asyncjob.Result{Permanent: true}, err
			}
			return asyncjob.Result{ResultJSON: mustJSON(map[string]string{"status": "not_requested", "reason": "paper import preparation is owned by the bounded reading scheduler"})}, nil
		}
	}
	results, err := startParseTasksAtLevel(context.WithValue(ctx, paperProcessingWorker{}, true), p.UserID, p.DatasetID, []string{p.TaskID}, p.Level)
	if err == nil && (len(results) != 1 || results[0].Status != "STARTED") {
		err = fmt.Errorf("processing submission failed")
		if len(results) > 0 && results[0].Message != "" {
			err = fmt.Errorf("%s", results[0].Message)
		}
	}
	if err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	return asyncjob.Result{}, nil
}
