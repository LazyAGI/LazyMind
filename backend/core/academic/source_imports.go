package academic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
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
		return orm.Dataset{}, errors.New("target unavailable")
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
	unique := map[string]SourceInspection{}
	for _, s := range inspected {
		if !s.Eligible {
			replyError(w, 422, "SOURCE_NOT_IMPORTABLE", s.Reason)
			return
		}
		unique[s.PaperKey] = s
	}
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
	now := time.Now().UTC()
	policy, _ := json.Marshal(sourceImportPolicy{OriginKey: req.SourceContext.key(), Context: *req.SourceContext, RequestHash: hash, TenantID: c.TenantID, CreatedLibrary: req.NewKnowledgeBase != nil})
	batch := orm.PaperImportBatch{ID: batchID, EntryType: "conversation_sources", TargetDatasetID: target, SourceDocumentIDsJSON: json.RawMessage("[]"), PolicySnapshotJSON: policy, Status: "pending", TotalItems: len(unique), IdempotencyKey: req.IdempotencyKey, CreatedBy: user, CreatedAt: now, UpdatedAt: now}
	e = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		dataset, e := lockSourceTarget(ctx, tx, c, target)
		if e != nil {
			return e
		}
		if scene, err := learning.NormalizeScene(dataset.Scene); err != nil || scene != "academic_papers" {
			return errors.New("来源论文只能加入论文集")
		}
		var replay orm.PaperImportBatch
		if e := tx.Where("id = ?", batchID).Take(&replay).Error; e == nil {
			if sourcePolicy(replay).RequestHash != hash {
				return errors.New("idempotency conflict")
			}
			batch = replay
			return nil
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e := tx.Create(&batch).Error; e != nil {
			return e
		}
		keys := make([]string, 0, len(unique))
		for key := range unique {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			s := unique[key]
			work, e := sourceWork(ctx, tx, s)
			if e != nil {
				return e
			}
			candidate, _ := json.Marshal(s.Candidate)
			refs, _ := json.Marshal([]string{s.SourceID})
			presence, _ := json.Marshal(map[string]string{"paper_key": key})
			item := orm.PaperImportItem{ID: uuid.NewString(), BatchID: batch.ID, AcademicWorkID: work.ID, ReferenceIDsJSON: refs, PresenceSnapshotJSON: presence, SelectedCandidateJSON: candidate, Status: "pending", Stage: "acquire", ErrorDetailsJSON: json.RawMessage("{}"), CreatedAt: now, UpdatedAt: now}
			// An alias follows the original durable item, including its processing state.
			var active orm.PaperImportItem
			q := tx.Table("paper_import_items pi").Select("pi.*").Joins("JOIN paper_import_batches pb ON pb.id = pi.batch_id").Where("pb.target_dataset_id = ? AND pi.academic_work_id = ? AND pi.status IN ?", target, work.ID, []string{"pending", "running"}).Order("pi.created_at ASC").Take(&active)
			if q.Error == nil {
				item.Stage = "follow"
				item.Status = "succeeded"
				item.PresenceSnapshotJSON = mustSourceJSON(map[string]string{"paper_key": key, "follows_item_id": active.ID})
			} else if !errors.Is(q.Error, gorm.ErrRecordNotFound) {
				return q.Error
			}
			if e := tx.Create(&item).Error; e != nil {
				return e
			}
		}
		job, e := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: paperImportJobType, ResourceType: "paper_import_batch", ResourceID: batch.ID, IdempotencyKey: "paper-import:" + batch.ID, Payload: paperImportPayload{BatchID: batch.ID, UserID: user, UserName: c.UserName}, CreateUserID: user, CreateUserName: c.UserName})
		if e != nil {
			return e
		}
		batch.AsyncJobID = job.ID
		return tx.Model(&batch).Update("async_job_id", job.ID).Error
	})
	if e != nil {
		sourceSubmitError(w, target, e)
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "target_dataset_id": target, "total_items": batch.TotalItems})
}
func sourceSubmitError(w http.ResponseWriter, target string, e error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(409)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": "IMPORT_NOT_CREATED", "message": e.Error(), "target_dataset_id": target})
}
func mustSourceJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func sourceWork(ctx context.Context, tx *gorm.DB, s SourceInspection) (orm.AcademicWork, error) {
	if s.Work.WorkID != "" {
		var w orm.AcademicWork
		e := tx.Where("id = ?", s.Work.WorkID).Take(&w).Error
		return w, e
	}
	var existing orm.AcademicWork
	if e := tx.Where("arxiv_id_base = ?", s.Work.ArxivID).Take(&existing).Error; e == nil {
		return existing, nil
	} else if !errors.Is(e, gorm.ErrRecordNotFound) {
		return existing, e
	}
	// Deterministic primary key closes the cross-dataset creation race as well.
	now := time.Now().UTC()
	work := orm.AcademicWork{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(s.PaperKey)).String(), CanonicalTitle: firstNonEmpty(s.Title, s.Work.ArxivID), NormalizedTitle: NormalizeTitle(s.Title), AuthorsJSON: json.RawMessage("[]"), ArxivIDBase: s.Work.ArxivID, ExternalIDsJSON: mustSourceJSON(map[string]string{"arxiv": s.Work.ArxivID}), ProvenanceJSON: json.RawMessage("{}"), ResolutionConfidence: 1, CreatedAt: now, UpdatedAt: now}
	if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&work).Error; e != nil {
		return work, e
	}
	return work, tx.Where("id = ?", work.ID).Take(&work).Error
}

type SourceImportView struct {
	ItemID           string `json:"item_id"`
	BatchID          string `json:"batch_id"`
	Title            string `json:"title"`
	PaperKey         string `json:"paper_key"`
	TargetDatasetID  string `json:"target_dataset_id"`
	TargetName       string `json:"target_name"`
	CreatedLibrary   bool   `json:"created_library"`
	Status           string `json:"status"`
	Stage            string `json:"stage"`
	ProcessingStatus string `json:"processing_status,omitempty"`
	DocumentID       string `json:"document_id,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
	Retryable        bool   `json:"retryable"`
	RetryBatchID     string `json:"retry_batch_id,omitempty"`
	RetryItemID      string `json:"retry_item_id,omitempty"`
}

func sourceItemView(ctx context.Context, b orm.PaperImportBatch, item orm.PaperImportItem, c doc.DatasetCatalogCaller, cached ...map[string]map[string]doc.DocumentMetadata) (SourceImportView, error) {
	v := SourceImportView{ItemID: item.ID, BatchID: b.ID, TargetDatasetID: b.TargetDatasetID, CreatedLibrary: sourcePolicy(b).CreatedLibrary, Status: item.Status, Stage: item.Stage, DocumentID: item.DocumentID}
	var ds orm.Dataset
	if e := store.DB().Where("id = ?", b.TargetDatasetID).Take(&ds).Error; e != nil {
		return v, e
	}
	v.TargetName = ds.DisplayName
	var work orm.AcademicWork
	if e := store.DB().Where("id = ?", item.AcademicWorkID).Take(&work).Error; e != nil {
		return v, e
	}
	v.Title = work.CanonicalTitle
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
	var detail map[string]string
	_ = json.Unmarshal(item.ErrorDetailsJSON, &detail)
	v.ErrorMessage = detail["message"]
	if item.DocumentID != "" {
		var m doc.DocumentMetadata
		if len(cached) > 0 {
			var ok bool
			m, ok = cached[0][b.TargetDatasetID][item.DocumentID]
			if !ok {
				return v, errors.New("document status unavailable")
			}
		} else {
			svc, e := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
			if e != nil {
				return v, e
			}
			m, e = svc.GetDocumentMetadata(ctx, doc.DocumentGetRequest{UserID: c.UserID, DatasetID: b.TargetDatasetID, DocumentID: item.DocumentID, Caller: c})
			if e != nil {
				return v, e
			}
		}
		v.ProcessingStatus = doc.DocumentProcessingStatus(m.ProcessingLevel, m.ProcessingState)
		if m.ProcessingError != nil {
			v.ProcessingStatus = "failed"
		}
		if m.ProcessingError != nil {
			v.ErrorMessage = m.ProcessingError.Message
			v.Stage = m.ProcessingError.Stage
		}
		if item.ErrorCode == "already_present" {
			v.Status = "exists"
		} else {
			switch v.ProcessingStatus {
			case "ready", "not_requested":
				v.Status = "added"
			case "failed":
				v.Status = "failed"
			case "unknown":
				v.Status = "unknown"
			default:
				v.Status = "running"
			}
		}
		v.Retryable = v.ProcessingStatus == "failed"
	} else if item.Status == "failed" {
		v.Retryable = true
	}
	if (item.Status == "running" || item.Status == "pending") && v.Status != "added" && v.Status != "exists" {
		var job orm.AsyncJob
		if e := store.DB().Where("id = ?", b.AsyncJobID).Take(&job).Error; e == nil && (job.Status == "failed" || job.Status == "canceled") {
			v.Status = "failed"
			v.Retryable = true
			v.ErrorMessage = "后台任务已中断，请重试"
		}
	}
	if item.Stage == "reparse" && (item.Status == "pending" || item.Status == "running") && v.ErrorMessage != "后台任务已中断，请重试" {
		v.Status, v.Stage, v.ErrorMessage, v.Retryable = item.Status, "processing", "", false
	}
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
			v.ErrorMessage = "状态暂时无法获取"
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
	if !parseJSON(r, &req) || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 || len(req.ItemIDs) > 500 {
		replyError(w, 400, "INVALID_REQUEST", "idempotency_key and optional item_ids required")
		return
	}
	c := sourceCaller(r)
	ctx, e := prepareSourceTarget(r.Context(), c, b.TargetDatasetID)
	if e != nil {
		replyError(w, 403, "TARGET_UNAVAILABLE", e.Error())
		return
	}
	var items []orm.PaperImportItem
	q := store.DB().Where("batch_id = ?", b.ID)
	if len(req.ItemIDs) > 0 {
		q = q.Where("id IN ?", req.ItemIDs)
	}
	if e = q.Find(&items).Error; e != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", e.Error())
		return
	}
	if len(req.ItemIDs) > 0 {
		wanted := map[string]bool{}
		for _, id := range req.ItemIDs {
			wanted[id] = true
		}
		if len(wanted) != len(items) {
			replyError(w, 400, "INVALID_ITEMS", "selected items do not belong to this batch")
			return
		}
	}
	retryIDs := []string{}
	reparseIDs := []string{}
	failures := []string{}
	for _, item := range items {
		view, e := sourceItemView(ctx, b, item, c)
		if e != nil {
			failures = append(failures, item.ID+": "+e.Error())
			continue
		}
		if !view.Retryable {
			continue
		}
		if item.DocumentID != "" && item.Status != "running" && item.Status != "pending" {
			reparseIDs = append(reparseIDs, item.ID)
		}
		retryIDs = append(retryIDs, item.ID)
	}
	if len(retryIDs) > 0 {
		e = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if _, e := lockSourceTarget(ctx, tx, c, b.TargetDatasetID); e != nil {
				return e
			}
			// Do not run two workers against the same batch, even for different selections.
			var previousJob orm.AsyncJob
			if e := tx.Where("id = ?", b.AsyncJobID).Take(&previousJob).Error; e != nil {
				return e
			}
			if previousJob.Status == "failed" || previousJob.Status == "canceled" {
				if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status IN ?", b.ID, []string{"pending", "running"}).Updates(map[string]any{"status": "failed"}).Error; e != nil {
					return e
				}
			}
			var active int64
			if e := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status IN ?", b.ID, []string{"pending", "running"}).Count(&active).Error; e != nil {
				return e
			}
			if active > 0 {
				return errors.New("batch is already processing")
			}
			job, e := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: paperImportJobType, ResourceType: "paper_import_batch", ResourceID: b.ID, IdempotencyKey: "source-retry:" + sourceHash([]string{b.ID, req.IdempotencyKey}), Payload: paperImportPayload{BatchID: b.ID, UserID: b.CreatedBy, UserName: c.UserName}, CreateUserID: c.UserID, CreateUserName: c.UserName})
			if e != nil {
				return e
			}
			if string(job.Status) == "succeeded" {
				return nil
			}
			if len(reparseIDs) > 0 {
				if e = tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND id IN ?", b.ID, reparseIDs).Update("stage", "reparse").Error; e != nil {
					return e
				}
			}
			if e = tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND id IN ?", b.ID, retryIDs).Updates(map[string]any{"status": "pending", "error_details_json": json.RawMessage("{}"), "updated_at": time.Now().UTC()}).Error; e != nil {
				return e
			}
			return tx.Model(&b).Updates(map[string]any{"async_job_id": job.ID, "status": "pending", "updated_at": time.Now().UTC()}).Error
		})
		if e != nil {
			failures = append(failures, e.Error())
		}
	}
	if len(failures) > 0 {
		replyError(w, 409, "RETRY_FAILED", strings.Join(failures, "; "))
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": b.ID, "status": "accepted"})
}
