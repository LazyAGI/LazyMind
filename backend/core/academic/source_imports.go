package academic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/learning"
	"lazymind/core/store"
)

type sourceImportPolicy struct {
	OriginKey      string        `json:"origin_key"`
	Context        SourceContext `json:"source_context"`
	RequestHash    string        `json:"request_hash"`
	TenantID       string        `json:"tenant_id"`
	CreatedLibrary bool          `json:"created_library"`
}

const sourceAlreadyPresent = "already_present"

var errSourceAlreadyPresent = errors.New("所选论文已在此论文集，请更新选择")

const sourceTargetNotFoundCode = "TARGET_NOT_FOUND"
const sourceTargetNotFoundMessage = "目标知识库不存在，请重新选择或新建知识库"

func isSourceTargetNotFound(err error) bool {
	var resource *doc.ResourceError
	return errors.As(err, &resource) && resource.Code == "NOT_FOUND"
}

func sourcePolicy(b orm.PaperImportBatch) sourceImportPolicy {
	var p sourceImportPolicy
	_ = json.Unmarshal(b.PolicySnapshotJSON, &p)
	return p
}
func sourceCaller(r *http.Request) doc.DatasetCatalogCaller {
	c := caller(r)
	c.UserName = store.UserName(r)
	c.Permissions = []string{"document.write", "document.read"}
	return c
}
func sourceImportCaller(b orm.PaperImportBatch) doc.DatasetCatalogCaller {
	return doc.DatasetCatalogCaller{UserID: b.CreatedBy, UserName: b.CreatedBy, TenantID: sourcePolicy(b).TenantID, Permissions: []string{"document.write", "document.read"}}
}

// A write locks the dataset row on both PostgreSQL and SQLite. The prepared
// source policy is rechecked by ResolveResourceDataset inside this transaction.
func lockSourceTarget(ctx context.Context, tx *gorm.DB, c doc.DatasetCatalogCaller, id string) (orm.Dataset, error) {
	// Acquire the write lock before any read. SQLite cannot upgrade an older
	// read snapshot if another importer committed between the read and write.
	result := tx.Model(&orm.Dataset{}).Where("id = ? AND deleted_at IS NULL", id).UpdateColumn("resource_version", gorm.Expr("resource_version + 1"))
	if result.Error != nil {
		return orm.Dataset{}, result.Error
	}
	if result.RowsAffected != 1 {
		return orm.Dataset{}, &doc.ResourceError{Code: "NOT_FOUND", Message: sourceTargetNotFoundMessage}
	}
	return doc.ResolveResourceDataset(ctx, tx, c, id, acl.PermissionDatasetWrite)
}
func prepareSourceTarget(ctx context.Context, c doc.DatasetCatalogCaller, id string) (context.Context, error) {
	return doc.PrepareResourceAuthorization(ctx, c, []doc.ResourceAuthorizationRequest{{DatasetID: id, Action: acl.PermissionDatasetWrite}, {DatasetID: id, Action: acl.PermissionDatasetUpload}})
}
func createSourceImport(w http.ResponseWriter, r *http.Request, user string, req createImportRequest) {
	if req.SourceContext == nil || len(req.SourceIDs) == 0 || len(req.SourceIDs) > 500 || len(req.Items) > 0 || req.TargetPID != "" || len(req.SourceDocumentIDs) > 0 || (req.TargetDatasetID == "") == (req.NewKnowledgeBase == nil) || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 {
		replyError(w, 400, "INVALID_REQUEST", "source_context, 1–500 source_ids, one target and idempotency_key required")
		return
	}
	sort.Strings(req.SourceIDs)
	hash := sourceHash(req)
	batchID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("source-import:"+user+":"+req.IdempotencyKey)).String()
	var prior orm.PaperImportBatch
	if e := store.DB().Where("id = ? AND created_by = ?", batchID, user).Take(&prior).Error; e == nil {
		if sourcePolicy(prior).RequestHash != hash {
			replyError(w, 409, "IDEMPOTENCY_CONFLICT", "request key already used for different parameters")
			return
		}
		common.ReplyJSON(w, map[string]any{"batch_id": prior.ID, "target_dataset_id": prior.TargetDatasetID, "total_items": prior.TotalItems})
		return
	} else if !errors.Is(e, gorm.ErrRecordNotFound) {
		replyError(w, 500, "IMPORT_QUERY_FAILED", e.Error())
		return
	}
	inspected, e := inspectSources(r.Context(), store.DB(), user, *req.SourceContext, req.SourceIDs)
	if e != nil {
		replyError(w, 403, "SOURCE_UNAVAILABLE", e.Error())
		return
	}
	for _, source := range inspected {
		if !source.Eligible {
			replyError(w, 422, "SOURCE_NOT_IMPORTABLE", source.Reason)
			return
		}
	}
	unique := importableSources(inspected)
	c := sourceCaller(r)
	target := req.TargetDatasetID
	if req.NewKnowledgeBase != nil {
		if req.NewKnowledgeBase.Scene != "" {
			scene, err := learning.NormalizeScene(req.NewKnowledgeBase.Scene)
			if err != nil || scene != "academic_papers" {
				replyError(w, 422, "INVALID_TARGET_SCENE", "来源论文只能加入论文集")
				return
			}
		}
		req.NewKnowledgeBase.Scene = "academic_papers"
		svc, e := doc.NewResourceService(doc.ResourceServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
		if e != nil {
			replyError(w, 503, "CREATE_UNAVAILABLE", e.Error())
			return
		}
		// Stable operation key and preferred ID also cover a lost response after creation.
		req.NewKnowledgeBase.PreferredID = "ds_" + sourceHash(batchID)[:31]
		created, e := svc.CreateKnowledge(r.Context(), doc.ResourceOperationContext{Caller: c, OperationKey: "source-library:" + batchID}, *req.NewKnowledgeBase)
		if e != nil {
			replyError(w, 422, "CREATE_FAILED", e.Error())
			return
		}
		if len(created.Items) == 0 {
			replyError(w, 500, "CREATE_FAILED", "missing creation receipt")
			return
		}
		target = created.Items[0].ResourceRef.KnowledgeBaseID
	}
	ctx, e := prepareSourceTarget(r.Context(), c, target)
	if e != nil {
		sourceSubmitError(w, target, e)
		return
	}
	batch := newSourceImportBatch(c, *req.SourceContext, target, batchID, req.IdempotencyKey, hash, len(unique), req.NewKnowledgeBase != nil)
	if e = enqueueSourceImport(ctx, c, &batch, unique, req.TargetDatasetID != "", nil); e != nil {
		sourceSubmitError(w, target, e)
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "target_dataset_id": target, "total_items": batch.TotalItems})
}

func sourceSelectionKey(source SourceInspection) string {
	if source.Candidate.Version != "" {
		return source.PaperKey + ":" + source.Candidate.SourceProvider + ":" + source.Candidate.Version
	}
	return source.PaperKey
}

func importableSources(inspected []SourceInspection) map[string]SourceInspection {
	unique := make(map[string]SourceInspection)
	pinned := make(map[string]bool)
	for _, source := range inspected {
		if source.Eligible && source.Candidate.Version != "" {
			pinned[source.PaperKey] = true
		}
	}
	for _, source := range inspected {
		if !source.Eligible || source.Candidate.Version == "" && pinned[source.PaperKey] {
			continue
		}
		key := sourceSelectionKey(source)
		if previous, exists := unique[key]; !exists || previous.NeedsPDFLookup || !source.NeedsPDFLookup {
			unique[key] = source
		}
	}
	return unique
}

func newSourceImportBatch(c doc.DatasetCatalogCaller, source SourceContext, target, batchID, operationKey, hash string, count int, created bool) orm.PaperImportBatch {
	now := time.Now().UTC()
	policy := mustSourceJSON(sourceImportPolicy{OriginKey: source.key(), Context: source, RequestHash: hash, TenantID: c.TenantID, CreatedLibrary: created})
	return orm.PaperImportBatch{ID: batchID, EntryType: "conversation_sources", TargetDatasetID: target, SourceDocumentIDsJSON: json.RawMessage("[]"), PolicySnapshotJSON: policy, Status: "pending", TotalItems: count, IdempotencyKey: operationKey, CreatedBy: c.UserID, CreatedAt: now, UpdatedAt: now}
}

func enqueueSourceImport(ctx context.Context, c doc.DatasetCatalogCaller, batch *orm.PaperImportBatch, unique map[string]SourceInspection, checkPresence bool, guard func(context.Context, *gorm.DB) error) error {
	now := batch.CreatedAt
	return store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if guard != nil {
			if err := guard(ctx, tx); err != nil {
				return err
			}
		}
		dataset, e := lockSourceTarget(ctx, tx, c, batch.TargetDatasetID)
		if e != nil {
			return e
		}
		if scene, err := learning.NormalizeScene(dataset.Scene); err != nil || scene != "academic_papers" {
			return errors.New("来源论文只能加入论文集")
		}
		var replay orm.PaperImportBatch
		if e := tx.Where("id = ?", batch.ID).Take(&replay).Error; e == nil {
			if sourcePolicy(replay).RequestHash != sourcePolicy(*batch).RequestHash {
				return errors.New("idempotency conflict")
			}
			*batch = replay
			return nil
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if checkPresence {
			for _, source := range unique {
				presence, err := sourceTargetPresence(ctx, tx, batch.TargetDatasetID, source)
				if err != nil {
					return err
				}
				if presence == sourceAlreadyPresent {
					return errSourceAlreadyPresent
				}
			}
		}
		if e := tx.Create(batch).Error; e != nil {
			return e
		}
		keys := make([]string, 0, len(unique))
		for key := range unique {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			s := unique[key]
			var work orm.AcademicWork
			if s.Requested == nil {
				var err error
				work, err = sourceWork(ctx, tx, s)
				if err != nil {
					return err
				}
			}
			candidate := savedImportCandidate{Requested: s.Requested}
			if s.Candidate.URL != "" {
				candidate = savedImportCandidate{FulltextCandidate: s.Candidate}
				// arXiv inspection is offline, even when the URL names a version.
				// Other accepted PDF sources already belong to a trusted saved work.
				if s.Candidate.SourceProvider != "arxiv" {
					ref := paperRef(work)
					ref.SourceProvider, ref.SourceVersion, ref.PDFURL = s.Candidate.SourceProvider, s.Candidate.Version, s.Candidate.URL
					candidate.VerifiedPaper = &ref
				}
			}
			refs, _ := json.Marshal([]string{s.SourceID})
			presence, _ := json.Marshal(map[string]string{"paper_key": s.PaperKey, "selection_key": key})
			item := orm.PaperImportItem{ID: uuid.NewString(), BatchID: batch.ID, TitleSnapshot: s.Title, AcademicWorkID: work.ID, ReferenceIDsJSON: refs, PresenceSnapshotJSON: presence, SelectedCandidateJSON: marshal(candidate), Status: "pending", Stage: "acquire", ErrorDetailsJSON: json.RawMessage("{}"), CreatedAt: now, UpdatedAt: now}
			// An alias follows the original durable item, including its processing state.
			var active orm.PaperImportItem
			q := tx.Table("paper_import_items pi").Select("pi.*").Joins("JOIN paper_import_batches pb ON pb.id = pi.batch_id").Where("pb.target_dataset_id = ? AND pb.entry_type = ? AND pi.academic_work_id = ? AND pi.status IN ?", batch.TargetDatasetID, batch.EntryType, work.ID, []string{"pending", "running"}).Order("pi.created_at ASC").Take(&active)
			if work.ID != "" && q.Error == nil {
				var previous map[string]string
				_ = json.Unmarshal(active.PresenceSnapshotJSON, &previous)
				var priorCandidate savedImportCandidate
				_ = json.Unmarshal(active.SelectedCandidateJSON, &priorCandidate)
				if previous["selection_key"] == key || (previous["selection_key"] == "" &&
					priorCandidate.SourceProvider == candidate.SourceProvider && priorCandidate.Version == candidate.Version) {
					item.Stage, item.Status = "follow", "succeeded"
					item.PresenceSnapshotJSON = mustSourceJSON(map[string]string{"paper_key": s.PaperKey, "follows_item_id": active.ID})
				}
			} else if q.Error != nil && !errors.Is(q.Error, gorm.ErrRecordNotFound) {
				return q.Error
			}
			if e := tx.Create(&item).Error; e != nil {
				return e
			}
		}
		job, e := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: paperImportJobType, ResourceType: "paper_import_batch", ResourceID: batch.ID, IdempotencyKey: "paper-import:" + batch.ID, Payload: paperImportPayload{BatchID: batch.ID, UserID: c.UserID, UserName: c.UserName}, CreateUserID: c.UserID, CreateUserName: c.UserName})
		if e != nil {
			return e
		}
		batch.AsyncJobID = job.ID
		return tx.Model(batch).Update("async_job_id", job.ID).Error
	})
}

func sourceSubmitError(w http.ResponseWriter, target string, e error) {
	status, code, message := http.StatusConflict, "IMPORT_NOT_CREATED", e.Error()
	switch {
	case isSourceTargetNotFound(e):
		status, code, message = http.StatusNotFound, sourceTargetNotFoundCode, sourceTargetNotFoundMessage
	case errors.Is(e, errSourceAlreadyPresent):
		code = "SOURCE_ALREADY_PRESENT"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message, "target_dataset_id": target})
}
func mustSourceJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func sourceWork(ctx context.Context, tx *gorm.DB, s SourceInspection) (orm.AcademicWork, error) {
	if s.Work.WorkID != "" {
		var w orm.AcademicWork
		e := tx.Where("id = ?", s.Work.WorkID).Take(&w).Error
		return w, e
	}
	identity := tx.Where("arxiv_id_base = ?", s.Work.ArxivID)
	identifier := s.Work.ArxivID
	externalIDs := map[string]string{"arxiv": identifier}
	if s.Work.DOI != "" {
		identity = tx.Where("doi_normalized = ?", s.Work.DOI)
		identifier = s.Work.DOI
		externalIDs = map[string]string{"doi": identifier}
	}
	var existing orm.AcademicWork
	if e := identity.Take(&existing).Error; e == nil {
		return existing, nil
	} else if !errors.Is(e, gorm.ErrRecordNotFound) {
		return existing, e
	}
	// Deterministic primary key closes the cross-dataset creation race as well.
	now := time.Now().UTC()
	title := firstNonEmpty(s.Title, identifier)
	work := orm.AcademicWork{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(s.PaperKey)).String(), CanonicalTitle: title, NormalizedTitle: NormalizeTitle(title), AuthorsJSON: json.RawMessage("[]"), ArxivIDBase: s.Work.ArxivID, DOINormalized: s.Work.DOI, ExternalIDsJSON: mustSourceJSON(externalIDs), ProvenanceJSON: json.RawMessage("{}"), ResolutionConfidence: 1, CreatedAt: now, UpdatedAt: now}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&work)
	if result.Error != nil || result.RowsAffected > 0 {
		return work, result.Error
	}
	err := identity.Take(&existing).Error
	return existing, err
}

type SourceImportView struct {
	RetryAt          *time.Time `json:"retry_at,omitempty"`
	ItemID           string     `json:"item_id"`
	BatchID          string     `json:"batch_id"`
	Title            string     `json:"title"`
	PaperKey         string     `json:"paper_key"`
	TargetDatasetID  string     `json:"target_dataset_id"`
	TargetName       string     `json:"target_name"`
	CreatedLibrary   bool       `json:"created_library"`
	Status           string     `json:"status"`
	Stage            string     `json:"stage"`
	ProcessingStatus string     `json:"processing_status,omitempty"`
	ProcessingLevel  string     `json:"processing_level,omitempty"`
	StateUnavailable bool       `json:"state_unavailable,omitempty"`
	DocumentID       string     `json:"document_id,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	ErrorCode        string     `json:"error_code,omitempty"`
	ErrorDetail      string     `json:"error_detail,omitempty"`
	ErrorRecovery    string     `json:"error_recovery,omitempty"`
	Retryable        bool       `json:"retryable"`
	RetryBatchID     string     `json:"retry_batch_id,omitempty"`
	RetryItemID      string     `json:"retry_item_id,omitempty"`
}

func sourceItemView(ctx context.Context, b orm.PaperImportBatch, item orm.PaperImportItem, c doc.DatasetCatalogCaller, cached ...map[string]map[string]doc.DocumentMetadata) (SourceImportView, error) {
	v := SourceImportView{ItemID: item.ID, BatchID: b.ID, TargetDatasetID: b.TargetDatasetID, CreatedLibrary: sourcePolicy(b).CreatedLibrary, Status: item.Status, Stage: item.Stage, DocumentID: item.DocumentID, ErrorCode: item.ErrorCode}
	var ds orm.Dataset
	if e := store.DB().Where("id = ?", b.TargetDatasetID).Take(&ds).Error; e != nil {
		return v, e
	}
	v.TargetName = ds.DisplayName
	var work orm.AcademicWork
	if item.AcademicWorkID != "" {
		if e := store.DB().Where("id = ?", item.AcademicWorkID).Take(&work).Error; e != nil {
			return v, e
		}
	}
	v.Title = firstNonEmpty(item.TitleSnapshot, work.CanonicalTitle, "未命名文献")
	var presence map[string]string
	_ = json.Unmarshal(item.PresenceSnapshotJSON, &presence)
	v.PaperKey = presence["paper_key"]
	if follows := presence["follows_item_id"]; follows != "" {
		var original orm.PaperImportItem
		if e := store.DB().Where("id = ? AND stage <> ?", follows, "follow").Take(&original).Error; e != nil {
			return v, e
		}
		var originalBatch orm.PaperImportBatch
		if e := store.DB().Where("id = ? AND target_dataset_id = ?", original.BatchID, b.TargetDatasetID).Take(&originalBatch).Error; e != nil {
			return v, e
		}
		originalView, e := sourceItemView(ctx, originalBatch, original, c, cached...)
		if e != nil {
			return v, e
		}
		originalView.ItemID = item.ID
		originalView.BatchID = b.ID
		originalView.CreatedLibrary = v.CreatedLibrary
		if originalBatch.CreatedBy != b.CreatedBy {
			originalView.Retryable = false
			originalView.RetryBatchID = ""
			originalView.RetryItemID = ""
		}

		return originalView, nil
	}
	var detail importFailureDetails
	_ = json.Unmarshal(item.ErrorDetailsJSON, &detail)
	v.ErrorMessage, v.ErrorDetail, v.ErrorRecovery, v.RetryAt = detail.Message, detail.Detail, detail.Recovery, detail.RetryAt
	if item.Status == "pending" && item.Stage == "retry_wait" {
		v.Status = "waiting_retry"
	}
	if item.Status == "failed" && item.ErrorCode == "needs_confirmation" {
		v.Status = "needs_confirmation"
	}
	if item.DocumentID != "" {
		var metadata map[string]doc.DocumentMetadata
		if len(cached) > 0 {
			metadata = cached[0][b.TargetDatasetID]
			if metadata == nil {
				return v, errors.New("document status unavailable")
			}
		} else {
			svc, e := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
			if e != nil {
				return v, e
			}
			metadata, e = svc.GetProcessingMetadata(ctx, c, b.TargetDatasetID, []string{item.DocumentID})
			if e != nil {
				return v, e
			}
		}
		m, ok := metadata[item.DocumentID]
		if !ok || b.EntryType == "conversation_sources" && !m.OriginalAvailable {
			v.Status, v.DocumentID = "original_unavailable", ""
			return v, nil
		}
		v.Status = "added"
		if item.ErrorCode == sourceAlreadyPresent {
			v.Status = "exists"
		}
		v.ProcessingLevel = m.ProcessingLevel
		if b.EntryType == "conversation_sources" {
			v.ProcessingLevel = m.ObservedProcessingLevel
		}
		v.ProcessingStatus = doc.DocumentProcessingStatus(v.ProcessingLevel, m.ProcessingState)
		if m.ProcessingError != nil {
			v.ProcessingStatus = "failed"
			v.ErrorMessage = "PDF 已保存，正文处理失败"
			v.ErrorCode, v.ErrorDetail, v.ErrorRecovery = m.ProcessingError.Code, m.ProcessingError.Message, m.ProcessingError.Recovery
			v.Stage = m.ProcessingError.Stage
		}
		v.Retryable = v.ProcessingStatus == "failed"
	} else if item.Status == "failed" && item.ErrorCode != "needs_confirmation" {
		v.Retryable = detail.RetryAt == nil || !detail.RetryAt.After(time.Now())
	}
	if item.Status == "running" || item.Status == "pending" {
		saved := v.Status == "added" || v.Status == "exists"
		var job orm.AsyncJob
		if e := store.DB().Where("id = ?", b.AsyncJobID).Take(&job).Error; e == nil && (job.Status == "failed" || job.Status == "canceled") {
			if saved {
				v.ProcessingStatus = "failed"
			} else {
				v.Status = "failed"
			}
			v.Retryable = true
			v.ErrorMessage = "后台任务已中断，请重试"
		} else if item.Stage == "reparse" && saved {
			v.ProcessingStatus, v.Stage, v.ErrorMessage, v.Retryable = item.Status, "processing", "", false
		}
	}
	v.StateUnavailable = v.ProcessingStatus == "unknown"
	if v.Retryable {
		v.RetryBatchID = b.ID
		v.RetryItemID = item.ID
	}
	return v, nil
}
func ListSourceImports(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	c := SourceContext{ConversationID: r.URL.Query().Get("conversation_id"), HistoryID: r.URL.Query().Get("history_id"), TaskID: r.URL.Query().Get("task_id"), WorkflowSessionID: r.URL.Query().Get("workflow_session_id"), WorkDisplayKey: r.URL.Query().Get("work_display_key")}
	if _, e := loadSourceRecords(r.Context(), store.DB(), user, c); e != nil {
		replyError(w, 403, "SOURCE_UNAVAILABLE", e.Error())
		return
	}
	var batches []orm.PaperImportBatch
	// JSON is an existing policy snapshot; CAST is supported by both database dialects.
	if e := store.DB().Where("created_by = ? AND entry_type = ? AND CAST(policy_snapshot_json AS TEXT) LIKE ?", user, "conversation_sources", "%"+c.key()+"%").Order("created_at ASC").Find(&batches).Error; e != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", e.Error())
		return
	}
	views := []SourceImportView{}
	batchIDs := make([]string, 0, len(batches))
	byBatch := map[string]orm.PaperImportBatch{}
	for _, b := range batches {
		batchIDs = append(batchIDs, b.ID)
		byBatch[b.ID] = b
	}
	var items []orm.PaperImportItem
	if len(batchIDs) > 0 {
		if e := store.DB().WithContext(r.Context()).Where("batch_id IN ?", batchIDs).Order("created_at ASC, id ASC").Find(&items).Error; e != nil {
			replyError(w, 500, "IMPORT_QUERY_FAILED", e.Error())
			return
		}
	}
	byTarget := map[string][]string{}
	seen := map[string]bool{}
	for _, item := range items {
		b := byBatch[item.BatchID]
		key := b.TargetDatasetID + "/" + item.DocumentID
		if item.DocumentID != "" && !seen[key] {
			byTarget[b.TargetDatasetID] = append(byTarget[b.TargetDatasetID], item.DocumentID)
			seen[key] = true
		}
	}
	// Follow-items refer to a previously committed item, not another processing job.
	followIDs := []string{}
	for _, item := range items {
		var presence map[string]string
		_ = json.Unmarshal(item.PresenceSnapshotJSON, &presence)
		if id := presence["follows_item_id"]; id != "" {
			followIDs = append(followIDs, id)
		}
	}
	if len(followIDs) > 0 {
		var followed []orm.PaperImportItem
		if e := store.DB().Where("id IN ? AND stage <> ?", followIDs, "follow").Find(&followed).Error; e != nil {
			replyError(w, 500, "IMPORT_QUERY_FAILED", e.Error())
			return
		}
		var prior []orm.PaperImportBatch
		ids := []string{}
		for _, item := range followed {
			ids = append(ids, item.BatchID)
		}
		if len(ids) > 0 {
			if e := store.DB().Where("id IN ?", ids).Find(&prior).Error; e != nil {
				replyError(w, 500, "IMPORT_QUERY_FAILED", e.Error())
				return
			}
		}
		for _, b := range prior {
			byBatch[b.ID] = b
		}
		for _, item := range followed {
			if item.DocumentID != "" {
				b := byBatch[item.BatchID]
				byTarget[b.TargetDatasetID] = append(byTarget[b.TargetDatasetID], item.DocumentID)
			}
		}
	}
	metadata := map[string]map[string]doc.DocumentMetadata{}
	svc, e := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
	if e != nil {
		replyError(w, 503, "IMPORT_QUERY_FAILED", e.Error())
		return
	}
	for target, ids := range byTarget {
		rows, err := svc.GetProcessingMetadata(r.Context(), sourceCaller(r), target, ids)
		if err == nil {
			metadata[target] = rows
		}
	}
	for _, item := range items {
		v, err := sourceItemView(r.Context(), byBatch[item.BatchID], item, sourceCaller(r), metadata)
		if err != nil {
			v.Status = "unknown"
			v.StateUnavailable = true
		}
		views = append(views, v)
	}
	common.ReplyJSON(w, map[string]any{"items": views})
}
func ListSourceTargets(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	svc, e := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: store.DB()})
	if e != nil {
		replyError(w, 503, "TARGETS_UNAVAILABLE", e.Error())
		return
	}
	rows := []doc.Dataset{}
	offset := 0
	for {
		page, e := svc.ListDatasets(r.Context(), doc.DatasetListRequest{UserID: user, Scene: "academic_papers", RequiredAction: "write", Offset: offset, Limit: 100, Caller: sourceCaller(r)})
		if e != nil {
			replyError(w, 503, "TARGETS_UNAVAILABLE", e.Error())
			return
		}
		for _, target := range page.Datasets {
			if _, err := doc.ResolveResourceDataset(r.Context(), store.DB(), sourceCaller(r), target.DatasetID, acl.PermissionDatasetUpload); err == nil {
				rows = append(rows, target)
			}
		}
		if !page.HasMore {
			break
		}
		offset = page.NextOffset
	}
	common.ReplyJSON(w, map[string]any{"items": rows})
}

func retrySourceImport(w http.ResponseWriter, r *http.Request, b orm.PaperImportBatch) {
	var req struct {
		ItemIDs        []string `json:"item_ids"`
		IdempotencyKey string   `json:"idempotency_key"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&req)
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) || req.IdempotencyKey == "" && b.EntryType == "conversation_sources" || len(req.IdempotencyKey) > 128 || len(req.ItemIDs) > 500 {
		replyError(w, 400, "INVALID_REQUEST", "idempotency_key and optional item_ids required")
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = uuid.NewString()
	}
	c := sourceCaller(r)
	ctx, err := prepareSourceTarget(r.Context(), c, b.TargetDatasetID)
	if err != nil {
		replyError(w, 403, "TARGET_UNAVAILABLE", doc.SafeProcessingMessage(err.Error()))
		return
	}
	key := "source-retry:" + sourceHash([]string{b.ID, req.IdempotencyKey})
	requested := append([]string{}, req.ItemIDs...)
	sort.Strings(requested)
	requestHash := sourceHash(requested)
	reply := func(job orm.AsyncJob) {
		var payload paperImportPayload
		_ = json.Unmarshal(job.PayloadJSON, &payload)
		if payload.RequestHash != requestHash {
			replyError(w, 409, "IDEMPOTENCY_CONFLICT", "重试请求与首次提交不同")
			return
		}
		common.ReplyJSON(w, map[string]any{"batch_id": b.ID, "job_id": job.ID, "status": "accepted", "retry_items": len(payload.ItemIDs)})
	}
	var replay orm.AsyncJob
	if err = store.DB().WithContext(ctx).Where("job_type=? AND idempotency_key=? AND resource_id=?", paperImportJobType, key, b.ID).Take(&replay).Error; err == nil {
		reply(replay)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		replyError(w, 500, "IMPORT_QUERY_FAILED", "无法查询重试记录")
		return
	}
	var items []orm.PaperImportItem
	query := store.DB().WithContext(ctx).Where("batch_id=?", b.ID)
	if len(req.ItemIDs) > 0 {
		query = query.Where("id IN ?", req.ItemIDs)
	}
	if err = query.Find(&items).Error; err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", "无法读取导入记录")
		return
	}
	if len(req.ItemIDs) > 0 {
		wanted := map[string]bool{}
		for _, id := range req.ItemIDs {
			wanted[id] = true
		}
		if len(wanted) != len(items) {
			replyError(w, 400, "INVALID_ITEMS", "所选论文不属于本次导入")
			return
		}
	}
	retryIDs, reparseIDs := []string{}, []string{}
	for _, item := range items {
		if item.Stage == "awaiting_association" || item.ResultKind == "metadata_only" {
			continue
		}
		if item.LibraryPaperID != "" {
			var entry orm.PaperLibraryEntry
			e := store.DB().WithContext(ctx).Where("dataset_id=? AND id=? AND removed_at IS NULL", b.TargetDatasetID, item.LibraryPaperID).Take(&entry).Error
			if errors.Is(e, gorm.ErrRecordNotFound) {
				continue
			}
			if e != nil {
				replyError(w, 500, "IMPORT_QUERY_FAILED", "无法读取论文状态")
				return
			}
		}
		if item.DocumentID != "" {
			var document orm.Document
			e := store.DB().WithContext(ctx).Where("id=? AND dataset_id=? AND deleted_at IS NULL", item.DocumentID, b.TargetDatasetID).Take(&document).Error
			if errors.Is(e, gorm.ErrRecordNotFound) {
				continue
			}
			if e != nil {
				replyError(w, 500, "IMPORT_QUERY_FAILED", "无法读取文件状态")
				return
			}
		}
		view, e := sourceItemView(ctx, b, item, c)
		if e != nil {
			replyError(w, 409, "RETRY_FAILED", doc.SafeProcessingMessage(e.Error()))
			return
		}
		if !view.Retryable || view.RetryBatchID != b.ID || view.RetryItemID != item.ID {
			continue
		}
		retryIDs = append(retryIDs, item.ID)
		if item.DocumentID != "" {
			reparseIDs = append(reparseIDs, item.ID)
		}
	}
	noFailed, active := errors.New("没有可重试的失败项"), errors.New("本次导入正在处理中")
	var queued orm.AsyncJob
	err = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := lockSourceTarget(ctx, tx, c, b.TargetDatasetID); e != nil {
			return e
		}
		if e := tx.Where("id=? AND created_by=?", b.ID, c.UserID).Take(&b).Error; e != nil {
			return e
		}
		if e := tx.Where("job_type=? AND idempotency_key=? AND resource_id=?", paperImportJobType, key, b.ID).Take(&queued).Error; e == nil {
			return nil
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if b.AsyncJobID != "" {
			var previous orm.AsyncJob
			if e := tx.Where("id=?", b.AsyncJobID).Take(&previous).Error; e != nil {
				return e
			}
			if previous.Status == "pending" || previous.Status == "running" {
				return active
			}
			if previous.Status == "failed" || previous.Status == "canceled" {
				if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id=? AND status IN ?", b.ID, []string{"pending", "running"}).Update("status", "failed").Error; e != nil {
					return e
				}
			}
		}
		if len(retryIDs) == 0 {
			return noFailed
		}
		var running int64
		if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id=? AND status IN ?", b.ID, []string{"pending", "running"}).Count(&running).Error; e != nil {
			return e
		}
		if running > 0 {
			return active
		}
		job, e := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: paperImportJobType, ResourceType: "paper_import_batch", ResourceID: b.ID, IdempotencyKey: key, Payload: paperImportPayload{RequestHash: requestHash, BatchID: b.ID, ItemIDs: retryIDs, UserID: b.CreatedBy, UserName: c.UserName}, CreateUserID: c.UserID, CreateUserName: c.UserName})
		if e != nil {
			return e
		}
		queued = *job
		if len(reparseIDs) > 0 {
			if e = tx.Model(&orm.PaperImportItem{}).Where("batch_id=? AND id IN ?", b.ID, reparseIDs).Update("stage", "reparse").Error; e != nil {
				return e
			}
		}
		if e = tx.Model(&orm.PaperImportItem{}).Where("batch_id=? AND id IN ?", b.ID, retryIDs).Updates(map[string]any{"status": "pending", "error_details_json": json.RawMessage("{}"), "updated_at": time.Now().UTC()}).Error; e != nil {
			return e
		}
		return tx.Model(&b).Updates(map[string]any{"async_job_id": job.ID, "status": "pending", "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		code := "RETRY_FAILED"
		if errors.Is(err, noFailed) {
			code = "NO_FAILED_ITEMS"
		}
		if errors.Is(err, active) {
			code = "IMPORT_ALREADY_PROCESSING"
		}
		replyError(w, 409, code, doc.SafeProcessingMessage(err.Error()))
		return
	}
	reply(queued)
}
