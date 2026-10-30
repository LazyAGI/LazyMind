package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
	"os"
	"path/filepath"
	"strings"
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
func completePresentImport(ctx context.Context, job asyncjob.Job, b orm.PaperImportBatch, item orm.PaperImportItem, workID, documentID string) error {
	return store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := sourceJobFence(tx, job, b.ID); e != nil {
			return e
		}
		if e := checkEnrichmentMembership(tx, b, item); e != nil {
			return e
		}
		var d orm.Document
		var work orm.AcademicWork
		if e := tx.Where("id=? AND dataset_id=? AND deleted_at IS NULL", documentID, b.TargetDatasetID).Take(&d).Error; e != nil {
			return e
		}
		if e := tx.Where("id=?", workID).Take(&work).Error; e != nil {
			return e
		}
		var policy struct {
			FolderID string `json:"folder_id"`
		}
		if e := json.Unmarshal(b.PolicySnapshotJSON, &policy); e != nil {
			return e
		}
		id, e := registerRecognizedLibraryDocument(ctx, tx, d, work, b.EntryType != "bibliography", policy.FolderID, item.ID, "")
		if e != nil {
			return e
		}
		return tx.Model(&orm.PaperImportItem{}).Where("id=? AND batch_id=?", item.ID, b.ID).Updates(map[string]any{"academic_work_id": workID, "document_id": documentID, "library_paper_id": id, "status": presentImportStatus(b), "stage": "done", "error_code": "already_present", "updated_at": time.Now().UTC()}).Error
	})
}
func activeSourceDocuments(tx *gorm.DB, dataset string) *gorm.DB {
	return tx.Table("documents d").Where("d.dataset_id = ? AND d.deleted_at IS NULL", dataset)
}

func sourcePresentDocument(tx *gorm.DB, dataset, workID, hash string, version ...string) (orm.Document, error) {
	var documents []orm.Document
	q := activeSourceDocuments(tx, dataset).Select("d.*")
	if hash != "" {
		q = q.Joins("JOIN uploaded_files uf ON uf.document_id = d.id AND uf.dataset_id = d.dataset_id").Where("uf.content_hash = ? AND uf.deleted_at IS NULL", hash)
	} else {
		q = q.Joins("JOIN academic_work_documents wd ON wd.document_id = d.id AND wd.dataset_id = d.dataset_id").Where("wd.academic_work_id = ?", workID)
	}
	if hash == "" && len(version) > 0 && version[0] != "" {
		q = q.Where("wd.source_version=?", version[0])
	}
	if err := q.Find(&documents).Error; err != nil {
		return orm.Document{}, err
	}
	for _, document := range documents {
		if paperlibrary.OriginalAvailable(document) {
			return document, nil
		}
	}
	return orm.Document{}, gorm.ErrRecordNotFound
}

// Recover the item receipt without submitting an already accepted parser task.
// Unknown remote outcomes remain retryable errors, never permission to resubmit.
func sourceImportTaskAccepted(ctx context.Context, document orm.Document, task orm.Task) (bool, error) {
	var control *struct {
		State          string `json:"task_state"`
		Submission     string `json:"market_submission"`
		PreviousTaskID string `json:"market_previous_task_id"`
	}
	if err := json.Unmarshal(task.Ext, &control); err != nil || control == nil {
		return false, errors.New("imported task metadata is unavailable; submission outcome requires verification")
	}
	if control.Submission == "retrying" && (task.LazyllmTaskID == "" || task.LazyllmTaskID == control.PreviousTaskID) {
		return false, errors.New("previous processing submission outcome requires verification")
	}
	raw := strings.ToUpper(strings.TrimSpace(control.State))
	if task.LazyllmTaskID != "" {
		_, observed, _, err := doc.ObserveDocumentProcessingDetails(ctx, store.DB(), store.LazyLLMDB(), document)
		if err != nil {
			return false, err
		}
		raw = strings.ToUpper(strings.TrimSpace(observed))
		if raw == "" || raw == "UNKNOWN" {
			return false, errors.New("remote processing submission outcome requires verification")
		}
	} else if control.Submission == "submitting" {
		return false, errors.New("processing submission outcome requires verification")
	}
	if raw == "" || raw == "CREATING" || raw == "UPLOADING" || raw == "UPLOADED" {
		return false, nil
	}
	switch doc.NormalizeTaskStateForUI(raw) {
	case "SUCCESS", "WORKING", "WAITING":
		return true, nil
	case "FAILED", "CANCELED":
		return false, nil
	default:
		return false, errors.New("processing submission outcome requires verification")
	}
}

// A retained document with a lost original keeps its history and display name.
// Give its replacement a free name instead of deleting or renaming that record.
func sourceImportFilename(tx *gorm.DB, dataset, parent, name string) (string, error) {
	var conflicts []orm.Document
	if err := tx.Where("dataset_id=? AND p_id=? AND display_name=? AND deleted_at IS NULL", dataset, parent, name).Find(&conflicts).Error; err != nil {
		return "", err
	}
	if len(conflicts) == 0 {
		return name, nil
	}
	for _, conflict := range conflicts {
		if paperlibrary.OriginalAvailable(conflict) {
			return name, nil // The ordinary document bridge owns live-name conflicts.
		}
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for index := 1; ; index++ {
		replacement := fmt.Sprintf("%s (recovered %d)%s", base, index, ext)
		var count int64
		if err := tx.Model(&orm.Document{}).Where("dataset_id=? AND p_id=? AND display_name=? AND deleted_at IS NULL", dataset, parent, replacement).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return replacement, nil
		}
	}
}

var errExternalCodeReference = errors.New("external code reference")

func handleImportBatchJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter, b orm.PaperImportBatch) (result asyncjob.Result, jobErr error) {
	defer func() {
		if jobErr == nil {
			return
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		persistErr := store.DB().WithContext(recovery).Transaction(func(tx *gorm.DB) error {
			if e := sourceJobFence(tx, job, b.ID); e != nil {
				return e
			}
			if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id=? AND status IN ?", b.ID, []string{"pending", "running"}).Updates(map[string]any{"status": "failed", "error_code": "import_interrupted", "error_details_json": mustSourceJSON(map[string]string{"message": doc.SafeProcessingMessage(jobErr.Error())}), "updated_at": time.Now().UTC()}).Error; e != nil {
				return e
			}
			return tx.Model(&b).Updates(map[string]any{"status": "failed", "updated_at": time.Now().UTC()}).Error
		})
		jobErr = errors.Join(jobErr, persistErr)
	}()

	var items []orm.PaperImportItem
	if e := store.DB().WithContext(ctx).Where("batch_id = ? AND status IN ?", b.ID, []string{"pending", "running"}).Order("created_at ASC").Find(&items).Error; e != nil {
		return asyncjob.Result{}, e
	}
	caller := sourceImportCaller(b)
	if b.EntryType != "conversation_sources" {
		var target orm.Dataset
		if err := store.DB().WithContext(ctx).Where("id = ? AND deleted_at IS NULL", b.TargetDatasetID).Take(&target).Error; err != nil {
			return asyncjob.Result{}, err
		}
		caller.TenantID = target.TenantID
	}
	if err := store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := sourceJobFence(tx, job, b.ID); err != nil {
			return err
		}
		return tx.Model(&b).Updates(map[string]any{"status": "running", "updated_at": time.Now().UTC()}).Error
	}); err != nil {
		return asyncjob.Result{}, err
	}

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
		err = processImportItem(ctx, job, b, item, caller, originals)
		if ctx.Err() != nil {
			return asyncjob.Result{}, ctx.Err()
		}
		if errors.Is(err, errExternalCodeReference) {
			if e := sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "skipped", "stage": "done", "error_code": "external_link"}); e != nil {
				return asyncjob.Result{}, e
			}
			err = nil
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
	summary := map[string]any{"batch_id": b.ID}
	err = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := sourceJobFence(tx, job, b.ID); e != nil {
			return e
		}
		var failed, completed, skipped int64
		if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = ?", b.ID, "failed").Count(&failed).Error; e != nil {
			return e
		}
		if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = ?", b.ID, "succeeded").Count(&completed).Error; e != nil {
			return e
		}
		if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = ?", b.ID, "skipped").Count(&skipped).Error; e != nil {
			return e
		}
		status := "succeeded"
		if failed > 0 {
			status = "completed_with_errors"
		}
		if completed == 0 && failed > 0 && b.EntryType != "conversation_sources" {
			status = "failed"
		}
		summary["status"], summary["completed"], summary["failed"], summary["skipped"] = status, completed, failed, skipped
		return tx.Model(&b).Updates(map[string]any{"skipped_items": skipped, "status": status, "completed_items": completed, "failed_items": failed, "updated_at": time.Now().UTC()}).Error
	})
	if err == nil && summary["failed"].(int64) > 0 && b.EntryType != "conversation_sources" {
		return asyncjob.Result{ResultJSON: mustSourceJSON(summary), Permanent: true, ErrorCode: "import_failed"}, errors.New("one or more paper imports failed")
	}
	return asyncjob.Result{ResultJSON: mustSourceJSON(summary)}, err
}
func processImportItem(ctx context.Context, job asyncjob.Job, b orm.PaperImportBatch, item orm.PaperImportItem, caller doc.DatasetCatalogCaller, originals *doc.OriginalService) error {
	ctx, err := prepareSourceTarget(ctx, caller, b.TargetDatasetID)
	if err != nil {
		return err
	}
	if b.EntryType == "bibliography" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		if err := checkEnrichmentMembership(store.DB().WithContext(ctx), b, item); err != nil {
			return err
		}
		if item.DocumentID == "" {
			existing, err := sourcePresentDocument(store.DB().WithContext(ctx), b.TargetDatasetID, item.AcademicWorkID, "")
			if err == nil {
				return completePresentImport(ctx, job, b, item, item.AcademicWorkID, existing.ID)
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := enrichBibliographyItem(ctx, job, b, &item, caller); err != nil {
				return err
			}
		}
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
				return fmt.Errorf("document reparse failed: %s", receipt.Error.Message)
			}
			code := ""
			if item.ErrorCode == "already_present" {
				code = item.ErrorCode
			}
			return sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "succeeded", "stage": "processing", "document_task_id": receipt.TaskID, "error_code": code, "error_details_json": json.RawMessage("{}")})
		}
		return errors.New("missing reparse receipt")
	}
	if item.DocumentID == "" && item.Stage != "reparse" {
		if err := resolveImportCandidate(ctx, job, b, &item); err != nil {
			return err
		}
	}
	var importGuard *arxivImportGuard
	var selected savedImportCandidate
	if err = json.Unmarshal(item.SelectedCandidateJSON, &selected); err != nil {
		return err
	}
	if item.DocumentID == "" && selected.SourceProvider == "arxiv" {
		urls, e := canonicalPaperURLs(selected.FulltextCandidate)
		if e != nil {
			return e
		}
		identifier, e := arxivsource.Parse(urls[0])
		if e != nil {
			return e
		}
		var identity orm.AcademicWork
		if e = store.DB().WithContext(ctx).Where("id=?", item.AcademicWorkID).Take(&identity).Error; e != nil {
			return e
		}
		if identity.ArxivIDBase != "" && identity.ArxivIDBase != identifier.BaseID {
			return errors.New("work identity conflict")
		}
		guard, e := lockArxivImport(ctx, store.DB(), identifier.BaseID)
		if e != nil {
			return e
		}
		defer guard.release()
		importGuard = guard
		ctx = guard.ctx
		if selected.VerifiedPaper == nil {
			metadata, e := arxivsource.NewClient().Lookup(ctx, urls[0])
			if e != nil {
				return e
			}
			ref := PaperRef{PaperID: item.AcademicWorkID, Title: metadata.Title, Abstract: &metadata.Abstract, Authors: metadata.Authors, Year: metadata.Year, Venue: metadata.Venue, DOI: metadata.DOI, ArxivID: metadata.BaseID, SourceProvider: "arxiv", SourceVersion: metadata.Version, PDFURL: metadata.PDFURL}
			selected.VerifiedPaper = &ref
			selected.URL = metadata.PDFURL
			selected.Version = metadata.Version
			item.SelectedCandidateJSON = marshal(selected)
			if e = sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"selected_candidate_json": item.SelectedCandidateJSON}); e != nil {
				return e
			}
		}
	}
	if item.DocumentID == "" {
		presentWork, presentDocument, err := findEquivalentPresentWork(ctx, item.AcademicWorkID, b.TargetDatasetID, selected.Version)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			associateReferencesWithWork(ctx, item.ReferenceIDsJSON, presentWork.ID, "presence")
			return completePresentImport(ctx, job, b, item, presentWork.ID, presentDocument.DocumentID)
		}
	}
	var bound doc.OriginalImportResult
	// Resume a committed document after interruption, never acquire a second copy.
	if item.DocumentID != "" {
		if err = store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", item.DocumentID, b.TargetDatasetID).Take(&bound.Document).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return doc.ErrImportedDocumentUnavailable
			}
			return err
		}
		if !paperlibrary.OriginalAvailable(bound.Document) {
			return doc.ErrImportedDocumentUnavailable
		}
		if err = store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND doc_id = ? AND deleted_at IS NULL", item.DocumentTaskID, b.TargetDatasetID, bound.Document.ID).Take(&bound.Task).Error; err != nil {
			return err
		}
		accepted, e := sourceImportTaskAccepted(ctx, bound.Document, bound.Task)
		if e != nil {
			return e
		}
		if accepted {
			return sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "succeeded", "stage": "processing", "error_code": "", "error_details_json": json.RawMessage("{}")})
		}
	} else {
		existing, e := sourcePresentDocument(store.DB().WithContext(ctx), b.TargetDatasetID, item.AcademicWorkID, "", selected.Version)
		if e == nil {
			return completePresentImport(ctx, job, b, item, item.AcademicWorkID, existing.ID)
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
		original, e := acquireImportOriginal(ctx, originals, caller, selected)
		if e != nil {
			return e
		}
		defer func() { _ = originals.ReleaseOriginal(context.WithoutCancel(ctx), caller, original.FileRef) }()
		var work orm.AcademicWork
		if e = store.DB().WithContext(ctx).Where("id = ?", item.AcademicWorkID).Take(&work).Error; e != nil {
			return e
		}
		title := strings.TrimSpace(work.CanonicalTitle)
		if candidate.SourceProvider == "arxiv" {
			title += " (" + work.ArxivIDBase + candidate.Version + ")"
		}
		if title == "" {
			title = work.ArxivIDBase
		}
		tags := []string{"paper"}
		if candidate.SourceProvider == "web" {
			tags = []string{"reference", "web"}
		}
		policy := "skip_same_content"
		if candidate.SourceProvider == "arxiv" {
			policy = "ask"
		}
		name, e := sourceImportFilename(store.DB().WithContext(ctx), b.TargetDatasetID, b.TargetPID, safePaperFilename(title)+filepath.Ext(original.Filename))
		if e != nil {
			return e
		}
		prepared, e := originals.PrepareOriginalBinding(ctx, caller, doc.ImportOriginalRequest{FileRef: original.FileRef, DatasetID: b.TargetDatasetID, ParentID: b.TargetPID, DisplayName: name, DataSourceType: "ACADEMIC_PROVIDER", Tags: tags, ConflictPolicy: policy})
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
			if importGuard != nil {
				if e := importGuard.check(); e != nil {
					return e
				}
			}
			if _, e := lockSourceTarget(ctx, tx, caller, b.TargetDatasetID); e != nil {
				return e
			}
			if e := sourceJobFence(tx, job, b.ID); e != nil {
				return e
			}
			if e := checkEnrichmentMembership(tx, b, item); e != nil {
				return e
			}
			var current orm.PaperImportItem
			if e := tx.Where("id = ?", item.ID).Take(&current).Error; e != nil {
				return e
			}
			if current.DocumentID != "" {
				return errors.New("document already bound by a newer attempt")
			}
			existing, e := sourcePresentDocument(tx, b.TargetDatasetID, item.AcademicWorkID, "", selected.Version)
			if errors.Is(e, gorm.ErrRecordNotFound) && candidate.SourceProvider != "arxiv" {
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
			_, version := NormalizeArxivID(original.FinalURL)
			if version == "" {
				_, version = NormalizeArxivID(candidate.URL)
			}
			versionKind := "unknown"
			if candidate.SourceProvider == "arxiv" {
				versionKind = "preprint"
			}
			link := orm.AcademicWorkDocument{
				AcademicWorkID: item.AcademicWorkID, DatasetID: b.TargetDatasetID, DocumentID: bound.Document.ID,
				VersionKind: versionKind, SourceVersion: version, SourceProvider: candidate.SourceProvider,
				SourceLocator: original.FinalURL, ContentSHA256: original.ContentSHA256,
				MatchMethod: "import", MatchConfidence: 1, IsPreferredVersion: true, CreatedAt: now, UpdatedAt: now,
			}
			if e = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; e != nil {
				return e
			}

			if selected.VerifiedPaper != nil && !bound.Skipped {
				metadata, ok := arxivMetadataFromRef(*selected.VerifiedPaper)
				if !ok {
					return errors.New("invalid verified paper snapshot")
				}
				if e = bindImportedPaper(tx, bound.Document, work); e != nil {
					return e
				}
				if e = tx.Model(&orm.AcademicDocumentState{}).Where("document_id=?", bound.Document.ID).Update("metadata_json", marshal(arxivWorkInput(metadata))).Error; e != nil {
					return e
				}
			}
			var policy struct {
				FolderID string `json:"folder_id"`
			}
			if e = json.Unmarshal(b.PolicySnapshotJSON, &policy); e != nil {
				return e
			}
			libraryID, e := registerRecognizedLibraryDocument(ctx, tx, bound.Document, work, b.EntryType != "bibliography", policy.FolderID, item.ID, "")
			if e != nil {
				return e
			}
			updates := map[string]any{"document_id": bound.Document.ID, "document_task_id": bound.Task.ID, "content_sha256": original.ContentSHA256, "stage": "processing", "updated_at": now, "library_paper_id": libraryID}
			if bound.Skipped {
				updates["error_code"] = "already_present"
				updates["status"] = presentImportStatus(b)
				updates["stage"] = "done"
			}
			return tx.Model(&orm.PaperImportItem{}).Where("id = ?", item.ID).Updates(updates).Error
		})
		if e != nil {
			return e
		}
		committed = true
		associateReferencesWithWork(ctx, item.ReferenceIDsJSON, item.AcademicWorkID, "import")
		if bound.Skipped {
			return nil
		}
	}
	bound = originals.SubmitOriginalProcessing(ctx, caller, bound)
	if bound.SubmissionStatus == "failed" {
		return fmt.Errorf("document processing submission failed: %s", bound.SubmissionError)
	}
	return sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"status": "succeeded", "stage": "processing", "error_code": "", "error_details_json": bibliographyDiagnostics(b, item)})
}

func presentImportStatus(b orm.PaperImportBatch) string {
	if b.EntryType == "conversation_sources" {
		return "succeeded"
	}
	return "skipped"
}

// Both entrypoints normalize missing reference candidates before the shared worker.
func resolveImportCandidate(ctx context.Context, job asyncjob.Job, b orm.PaperImportBatch, item *orm.PaperImportItem) error {
	var candidate FulltextCandidate
	if err := json.Unmarshal(item.SelectedCandidateJSON, &candidate); err != nil {
		return err
	}
	if candidate.URL != "" {
		return nil
	}
	var ids []string
	if json.Unmarshal(item.ReferenceIDsJSON, &ids) != nil || len(ids) == 0 {
		return errors.New("reference metadata is missing")
	}
	var ref orm.AcademicReference
	if err := store.DB().WithContext(ctx).Where("id = ?", ids[0]).Take(&ref).Error; err != nil {
		return err
	}
	if ref.DOINormalized != "" && !strings.Contains(strings.ToLower(ref.RawText), "arxiv") {
		ref.ArxivIDBase = ""
		if ref.ResolvedWorkID != "" {
			if err := store.DB().WithContext(ctx).Model(&orm.AcademicWork{}).Where("id = ?", ref.ResolvedWorkID).Updates(map[string]any{"arxiv_id_base": "", "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
	}
	work, _, _, err := resolveReferenceWithExternal(ctx, store.DB().WithContext(ctx), &ref, true)
	if err != nil {
		return err
	}
	candidates := candidatesForWork(work)
	if len(candidates) == 0 {
		pageURL := extractReferenceURL(ref.RawText)
		if pageURL == "" {
			return errors.New("no downloadable reference resource was found")
		}
		if isGitHubReferenceURL(pageURL) {
			return fmt.Errorf("%w: %s", errExternalCodeReference, pageURL)
		}
		candidates = []FulltextCandidate{{URL: pageURL, SourceType: "reference_page", SourceProvider: "web", ExpectedMIME: "text/html"}}
	}
	item.AcademicWorkID = work.ID
	item.SelectedCandidateJSON, err = json.Marshal(candidates[0])
	if err != nil {
		return err
	}
	return sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"academic_work_id": item.AcademicWorkID, "selected_candidate_json": item.SelectedCandidateJSON, "stage": "acquire"})
}

// The paper cache owns identity/version reuse; OriginalService owns bytes and binding.
func acquireImportOriginal(ctx context.Context, originals *doc.OriginalService, caller doc.DatasetCatalogCaller, selected savedImportCandidate) (doc.OriginalRef, error) {
	if selected.SourceProvider != "arxiv" {
		return acquireCandidateOriginal(ctx, originals, caller, selected.FulltextCandidate)
	}
	if selected.VerifiedPaper == nil {
		return doc.OriginalRef{}, errors.New("verified arXiv metadata required")
	}
	path, _, err := acquireCachedPDFLocked(ctx, caller.UserID, *selected.VerifiedPaper, func(ctx context.Context, c FulltextCandidate) (string, int64, error) {
		return acquirePDF(ctx, caller.UserID, c)
	})
	if err != nil {
		return doc.OriginalRef{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return doc.OriginalRef{}, err
	}
	defer file.Close()
	return originals.AcquireOriginal(ctx, caller, doc.AcquireOriginalRequest{Filename: "paper.pdf", ContentType: "application/pdf", Reader: file, FinalURL: selected.VerifiedPaper.PDFURL})
}
