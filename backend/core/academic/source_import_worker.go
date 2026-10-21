package academic

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"time"
)

// Fence every publication against the current durable job attempt.
func sourceJobFence(tx *gorm.DB, job asyncjob.Job, batchID string) error {
	var current orm.AsyncJob
	if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ? AND attempt_count = ?", job.ID, "running", job.AttemptCount).Take(&current).Error; e != nil {
		return e
	}
	var count int64
	if e := tx.Model(&orm.PaperImportBatch{}).Where("id = ? AND async_job_id = ? AND status <> ?", batchID, job.ID, "canceled").Count(&count).Error; e != nil {
		return e
	}
	if count != 1 {
		return errors.New("import attempt superseded")
	}
	return nil
}
func sourceItemUpdate(ctx context.Context, job asyncjob.Job, b orm.PaperImportBatch, itemID string, updates map[string]any) error {
	return store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := sourceJobFence(tx, job, b.ID); e != nil {
			return e
		}
		updates["updated_at"] = time.Now().UTC()
		return tx.Model(&orm.PaperImportItem{}).Where("id = ? AND batch_id = ?", itemID, b.ID).Updates(updates).Error
	})
}
func sourcePresentDocument(tx *gorm.DB, dataset, workID, hash string) (orm.Document, error) {
	var d orm.Document
	q := tx.Table("documents d").Select("d.*").Where("d.dataset_id = ? AND d.deleted_at IS NULL", dataset)
	if hash != "" {
		q = q.Joins("JOIN uploaded_files uf ON uf.document_id = d.id AND uf.dataset_id = d.dataset_id").Where("uf.content_hash = ? AND uf.deleted_at IS NULL", hash)
	} else {
		q = q.Joins("JOIN academic_work_documents wd ON wd.document_id = d.id AND wd.dataset_id = d.dataset_id").Where("wd.academic_work_id = ?", workID)
	}
	err := q.Take(&d).Error
	return d, err
}
func handleSourceImportJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter, b orm.PaperImportBatch) (asyncjob.Result, error) {
	var items []orm.PaperImportItem
	if e := store.DB().WithContext(ctx).Where("batch_id = ? AND status IN ?", b.ID, []string{"pending", "running"}).Order("created_at ASC").Find(&items).Error; e != nil {
		return asyncjob.Result{}, e
	}
	caller := sourceImportCaller(b)
	originals, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: store.DB()})
	if err != nil {
		return asyncjob.Result{}, err
	}
	for index, item := range items {
		if err = reporter.Heartbeat(ctx); err != nil {
			return asyncjob.Result{}, err
		}
		if err = sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "running", "attempt_count": gorm.Expr("attempt_count + 1")}); err != nil {
			return asyncjob.Result{}, err
		}
		err = processSourceItem(ctx, job, b, item, caller, originals)
		if ctx.Err() != nil {
			return asyncjob.Result{}, ctx.Err()
		}
		if err != nil {
			code := "import_failed"
			if item.ErrorCode == "already_present" {
				code = item.ErrorCode
			}
			if e := sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "failed", "error_code": code, "error_details_json": mustSourceJSON(map[string]string{"message": doc.SafeProcessingMessage(err.Error())})}); e != nil {
				return asyncjob.Result{}, e
			}
		}
		if err = reporter.SetProgress(ctx, int64(index+1), int64(len(items))); err != nil {
			return asyncjob.Result{}, err
		}
	}
	err = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := sourceJobFence(tx, job, b.ID); e != nil {
			return e
		}
		var failed, completed int64
		if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = ?", b.ID, "failed").Count(&failed).Error; e != nil {
			return e
		}
		if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = ?", b.ID, "succeeded").Count(&completed).Error; e != nil {
			return e
		}
		status := "succeeded"
		if failed > 0 {
			status = "completed_with_errors"
		}
		return tx.Model(&b).Updates(map[string]any{"status": status, "completed_items": completed, "failed_items": failed, "updated_at": time.Now().UTC()}).Error
	})
	return asyncjob.Result{ResultJSON: mustSourceJSON(map[string]string{"batch_id": b.ID})}, err
}
func processSourceItem(ctx context.Context, job asyncjob.Job, b orm.PaperImportBatch, item orm.PaperImportItem, caller doc.DatasetCatalogCaller, originals *doc.OriginalService) error {
	ctx, err := prepareSourceTarget(ctx, caller, b.TargetDatasetID)
	if err != nil {
		return err
	}
	if item.Stage == "reparse" {
		svc, e := doc.NewResourceService(doc.ResourceServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
		if e != nil {
			return e
		}
		result, e := svc.Reparse(ctx, doc.ResourceOperationContext{Caller: caller, OperationKey: "source-reparse:" + sourceHash([]string{job.ID, item.ID})}, doc.ReparseDocumentsRequest{KnowledgeBaseID: b.TargetDatasetID, DocumentIDs: []string{item.DocumentID}})
		if e != nil {
			return e
		}
		for _, receipt := range result.Items {
			if receipt.Error != nil {
				return errors.New(receipt.Error.Message)
			}
			code := ""
			if item.ErrorCode == "already_present" {
				code = item.ErrorCode
			}
			return sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "succeeded", "stage": "processing", "document_task_id": receipt.TaskID, "error_code": code, "error_details_json": json.RawMessage("{}")})
		}
		return errors.New("missing reparse receipt")
	}
	var bound doc.OriginalImportResult
	// Resume a committed document after interruption, never acquire a second copy.
	if item.DocumentID != "" {
		if err = store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", item.DocumentID, b.TargetDatasetID).Take(&bound.Document).Error; err != nil {
			return err
		}
		if err = store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", item.DocumentTaskID, b.TargetDatasetID).Take(&bound.Task).Error; err != nil {
			return err
		}
	} else {
		existing, e := sourcePresentDocument(store.DB().WithContext(ctx), b.TargetDatasetID, item.AcademicWorkID, "")
		if e == nil {
			return sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "succeeded", "stage": "done", "document_id": existing.ID, "error_code": "already_present"})
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		var candidate FulltextCandidate
		if e = json.Unmarshal(item.SelectedCandidateJSON, &candidate); e != nil {
			return e
		}
		if err = sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"stage": "download"}); err != nil {
			return err
		}
		original, e := acquireCandidateOriginal(ctx, originals, caller, candidate)
		if e != nil {
			return e
		}
		var work orm.AcademicWork
		if e = store.DB().WithContext(ctx).Where("id = ?", item.AcademicWorkID).Take(&work).Error; e != nil {
			return e
		}
		prepared, e := originals.PrepareOriginalBinding(ctx, caller, doc.ImportOriginalRequest{FileRef: original.FileRef, DatasetID: b.TargetDatasetID, DisplayName: safePaperFilename(work.CanonicalTitle) + ".pdf", DataSourceType: "ACADEMIC_PROVIDER", Tags: []string{"paper"}, ConflictPolicy: "skip_same_content"})
		if e != nil {
			return e
		}
		committed := false
		defer func() {
			if !committed || bound.Skipped {
				_ = originals.CleanupPreparedBinding(prepared)
			}
		}()
		ctx, e = doc.PrepareResourceAuthorization(ctx, caller, append(prepared.AuthorizationRequests(), doc.ResourceAuthorizationRequest{DatasetID: b.TargetDatasetID, Action: acl.PermissionDatasetWrite}))
		if e != nil {
			return e
		}
		e = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := lockSourceTarget(ctx, tx, caller, b.TargetDatasetID); e != nil {
				return e
			}
			if e := sourceJobFence(tx, job, b.ID); e != nil {
				return e
			}
			var current orm.PaperImportItem
			if e := tx.Where("id = ?", item.ID).Take(&current).Error; e != nil {
				return e
			}
			if current.DocumentID != "" {
				return errors.New("document already bound by a newer attempt")
			}
			existing, e := sourcePresentDocument(tx, b.TargetDatasetID, item.AcademicWorkID, "")
			if errors.Is(e, gorm.ErrRecordNotFound) {
				existing, e = sourcePresentDocument(tx, b.TargetDatasetID, item.AcademicWorkID, original.ContentSHA256)
			}
			if e == nil {
				bound = doc.OriginalImportResult{Document: existing, ContentSHA256: original.ContentSHA256, Skipped: true}
			} else if errors.Is(e, gorm.ErrRecordNotFound) {
				bound, e = originals.BindOriginalTx(ctx, tx, caller, prepared)
				if e != nil {
					return e
				}
			} else {
				return e
			}
			now := time.Now().UTC()
			link := orm.AcademicWorkDocument{AcademicWorkID: item.AcademicWorkID, DatasetID: b.TargetDatasetID, DocumentID: bound.Document.ID, SourceProvider: candidate.SourceProvider, SourceLocator: candidate.URL, ContentSHA256: original.ContentSHA256, MatchMethod: "import", MatchConfidence: 1, IsPreferredVersion: true, CreatedAt: now, UpdatedAt: now}
			if e = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; e != nil {
				return e
			}
			updates := map[string]any{"document_id": bound.Document.ID, "document_task_id": bound.Task.ID, "content_sha256": original.ContentSHA256, "stage": "processing", "updated_at": now}
			if bound.Skipped {
				updates["error_code"] = "already_present"
				updates["status"] = "succeeded"
				updates["stage"] = "done"
			}
			return tx.Model(&orm.PaperImportItem{}).Where("id = ?", item.ID).Updates(updates).Error
		})
		if e != nil {
			return e
		}
		committed = true
		if bound.Skipped {
			return nil
		}
	}
	bound = originals.SubmitOriginalProcessing(ctx, caller, bound)
	if bound.SubmissionStatus == "failed" {
		return errors.New(bound.SubmissionError)
	}
	return sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "succeeded", "stage": "processing", "error_code": "", "error_details_json": json.RawMessage("{}")})
}
