package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const paperProcessingJob = "document.paper_processing"

type paperProcessingWorker struct{}
type paperProcessingPayload struct{ DatasetID, TaskID, UserID, Level string }

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
				Payload: paperProcessingPayload{dataset, id, user, level}, CreateUserID: user,
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
func runPaperProcessing(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var p paperProcessingPayload
	if err := json.Unmarshal(job.PayloadJSON, &p); err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	results, err := startParseTasksAtLevel(context.WithValue(ctx, paperProcessingWorker{}, true), p.UserID, p.DatasetID, []string{p.TaskID}, p.Level)
	if err == nil && (len(results) != 1 || results[0].Status != "STARTED") {
		err = fmt.Errorf("processing submission failed")
		if len(results) > 0 && results[0].Message != "" {
			err = fmt.Errorf("%s", results[0].Message)
		}
	}
	if err != nil {
		var task orm.Task
		if store.DB().WithContext(ctx).Where("id=? AND dataset_id=?", p.TaskID, p.DatasetID).Take(&task).Error == nil {
			markTaskStartFailed(ctx, p.DatasetID, task, err.Error())
		}
		return asyncjob.Result{Permanent: true}, err
	}
	return asyncjob.Result{}, nil
}
