package academic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

func init() {
	asyncjob.Register("academic.identify", runIdentify)
	doc.OnAcademicDocumentSubmitted = func(tx *gorm.DB, row orm.Document) error {
		var ds orm.Dataset
		if err := tx.Where("id=?", row.DatasetID).Take(&ds).Error; err != nil {
			return err
		}
		var classification struct {
			LibraryKind string `json:"library_kind"`
		}
		_ = json.Unmarshal(ds.Ext, &classification)
		if (ds.ScenarioKey != "academic_papers" && classification.LibraryKind != "paper") || strings.ToLower(filepath.Ext(row.DisplayName)) != ".pdf" {
			return nil
		}
		return enqueueIdentify(context.Background(), tx, row, false)
	}
}
func enqueueIdentify(ctx context.Context, tx *gorm.DB, row orm.Document, force bool) error {
	state := orm.AcademicDocumentState{DocumentID: row.ID, DatasetID: row.DatasetID, Kind: "unknown", Status: "pending", Revision: 1, UpdatedAt: time.Now().UTC()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return err
	}
	if err := tx.Where("document_id=?", row.ID).Take(&state).Error; err != nil {
		return err
	}
	if force {
		state.Revision++
		if err := tx.Model(&state).Updates(map[string]any{"revision": state.Revision, "manual": false, "status": "pending", "error": ""}).Error; err != nil {
			return err
		}
	} else if state.Manual || state.Kind == "paper" {
		return nil
	}
	_, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: "academic.identify", ResourceType: "document", ResourceID: row.ID, CreateUserID: row.CreateUserID, IdempotencyKey: row.ID + ":" + time.Now().UTC().Format(time.RFC3339Nano), Payload: map[string]any{"revision": state.Revision, "force": force}, MaxAttempts: 5})
	return err
}
func runIdentify(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var payload struct {
		Revision int64 `json:"revision"`
		Force    bool  `json:"force"`
	}
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	db := store.DB().WithContext(ctx)
	var state orm.AcademicDocumentState
	var row orm.Document
	if err := db.Where("id=? AND deleted_at IS NULL", job.ResourceID).Take(&row).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	if err := db.Where("document_id=?", row.ID).Take(&state).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	if state.Manual || state.Revision != payload.Revision {
		return asyncjob.Result{}, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://internal/", nil)
	req.Header.Set("X-User-Id", job.CreateUserID)
	if !payload.Force {
		var known orm.AcademicWorkDocument
		if err := db.Where("document_id=?", row.ID).Take(&known).Error; err == nil {
			var work orm.AcademicWork
			if err := db.Where("id=?", known.AcademicWorkID).Take(&work).Error; err != nil {
				return asyncjob.Result{}, err
			}
			return asyncjob.Result{}, bindImportedPaper(db, row, work)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return asyncjob.Result{}, err
		}
	}
	text, err := documentText(ctx, req, job.CreateUserID, row.DatasetID, row.ID)
	if err != nil {
		return identifyFailure(db, state, job, err)
	}
	// Restrict identity extraction to the document's front matter, never bibliography.
	sample := text
	if len([]rune(sample)) > 16000 {
		sample = string([]rune(sample)[:16000])
	}
	var extracted struct {
		Kind string `json:"kind"`
		WorkInput
	}
	llmConfig, err := modelconfig.LoadLLMConfig(ctx, db, job.CreateUserID)
	if err != nil {
		return identifyFailure(db, state, job, err)
	}
	if err = internalAcademic(ctx, "metadata:extract", map[string]any{"text": sample, "llm_config": llmConfig}, &extracted); err != nil {
		return identifyExtractionFailure(db, state, job, err)
	}
	if extracted.Kind != "paper" && extracted.Kind != "other" && extracted.Kind != "unknown" {
		return identifyExtractionFailure(db, state, job, errors.New("invalid classification"))
	}
	hash := sha256.Sum256([]byte(text))
	err = db.Transaction(func(tx *gorm.DB) error {
		var current orm.AcademicDocumentState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("document_id=?", row.ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Manual || current.Revision != payload.Revision {
			return nil
		}
		workID := ""
		if extracted.Kind == "paper" {
			work, method, e := findOrCreateWork(ctx, tx, extracted.WorkInput)
			if e != nil {
				return e
			}
			workID = work.ID
			link := orm.AcademicWorkDocument{AcademicWorkID: work.ID, DatasetID: row.DatasetID, DocumentID: row.ID, VersionKind: "unknown", SourceProvider: "upload", ContentSHA256: documentFileHash(row), MatchMethod: method, MatchConfidence: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if err := saveDocumentWorkLink(tx, link); err != nil {
				return err
			}
		}
		return tx.Model(&current).Updates(map[string]any{"kind": extracted.Kind, "status": "succeeded", "paper_id": workID, "metadata_json": marshal(extracted.WorkInput), "fingerprint": hex.EncodeToString(hash[:]), "error": "", "updated_at": time.Now().UTC()}).Error
	})
	return asyncjob.Result{}, err
}
func identifyExtractionFailure(db *gorm.DB, state orm.AcademicDocumentState, job asyncjob.Job, err error) (asyncjob.Result, error) {
	// Parsing may be retried, but one recognition task makes only one LLM call.
	if updateErr := db.Model(&orm.AcademicDocumentState{}).Where("document_id=? AND revision=? AND manual=?", state.DocumentID, state.Revision, false).Updates(map[string]any{"status": "failed", "error": err.Error(), "updated_at": time.Now().UTC()}).Error; updateErr != nil {
		return asyncjob.Result{}, updateErr
	}
	return asyncjob.Result{Permanent: true}, err
}
func identifyFailure(db *gorm.DB, state orm.AcademicDocumentState, job asyncjob.Job, err error) (asyncjob.Result, error) {
	status := "pending"
	if job.AttemptCount >= job.MaxAttempts {
		status = "failed"
	}
	if e := db.Model(&orm.AcademicDocumentState{}).Where("document_id=? AND revision=? AND manual=?", state.DocumentID, state.Revision, false).Updates(map[string]any{"status": status, "error": err.Error(), "updated_at": time.Now().UTC()}).Error; e != nil {
		return asyncjob.Result{}, e
	}
	return asyncjob.Result{}, err
}
func academicDataset(r *http.Request, user, dataset string) bool {
	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: store.DB()})
	if err != nil {
		return false
	}
	result, err := service.ListDatasets(r.Context(), doc.DatasetListRequest{UserID: user, Limit: 100, Caller: caller(r)})
	for {
		if err != nil {
			return false
		}
		for _, ds := range result.Datasets {
			if ds.DatasetID == dataset {
				return ds.ScenarioKey == "academic_papers"
			}
		}
		if !result.HasMore {
			return false
		}
		result, err = service.ListDatasets(r.Context(), doc.DatasetListRequest{UserID: user, Limit: 100, Offset: result.NextOffset, Caller: caller(r)})
	}
}
func DocumentStates(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	dataset := r.URL.Query().Get("dataset_id")
	if !academicDataset(r, user, dataset) {
		replyError(w, 403, "FORBIDDEN", "academic knowledge base is not accessible")
		return
	}
	var states []orm.AcademicDocumentState
	ids := strings.Split(r.URL.Query().Get("document_ids"), ",")
	q := store.DB().Where("dataset_id=?", dataset)
	if len(ids) > 0 && ids[0] != "" {
		q = q.Where("document_id IN ?", ids)
	}
	if err := q.Find(&states).Error; err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	workIDs := []string{}
	for _, state := range states {
		if state.PaperID != "" {
			workIDs = append(workIDs, state.PaperID)
		}
	}
	var works []orm.AcademicWork
	var marks []orm.AcademicUserPaperState
	if len(workIDs) > 0 {
		if err := store.DB().Where("id IN ?", workIDs).Find(&works).Error; err != nil {
			replyError(w, 500, "READ_FAILED", err.Error())
			return
		}
		if err := store.DB().Where("user_id=? AND paper_id IN ?", user, workIDs).Find(&marks).Error; err != nil {
			replyError(w, 500, "READ_FAILED", err.Error())
			return
		}
	}
	papers := map[string]PaperRef{}
	for _, work := range works {
		papers[work.ID] = paperRef(work)
	}
	important := map[string]bool{}
	for _, mark := range marks {
		important[mark.PaperID] = mark.IsImportant
	}
	items := make([]map[string]any, 0, len(states))
	for _, state := range states {
		item := map[string]any{"state": state, "is_important": important[state.PaperID]}
		if paper, ok := papers[state.PaperID]; ok {
			item["paper"] = documentPaperRef(paper, state.MetadataJSON)
		}
		var document orm.Document
		if err := store.DB().Where("id=? AND dataset_id=? AND deleted_at IS NULL", state.DocumentID, dataset).Take(&document).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			replyError(w, 500, "READ_FAILED", err.Error())
			return
		}
		var link orm.AcademicWorkDocument
		if err := store.DB().Where("dataset_id=? AND document_id=? AND academic_work_id=?", dataset, state.DocumentID, state.PaperID).Take(&link).Error; err == nil {
			item["source_provider"], item["source_version"], item["content_sha256"] = link.SourceProvider, link.SourceVersion, link.ContentSHA256
			if paper, ok := item["paper"].(PaperRef); ok {
				paper.SourceProvider, paper.SourceVersion = link.SourceProvider, link.SourceVersion
				pinPaperURLs(&paper)
				item["paper"] = paper
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			replyError(w, 500, "READ_FAILED", err.Error())
			return
		}
		_, status, message, err := newArxivImportService(store.DB()).processingStatus(r.Context(), document, nil)
		if err != nil {
			status, message = "unknown", err.Error()
		}
		item["processing_status"], item["processing_error"] = status, message
		items = append(items, item)
	}
	common.ReplyJSON(w, map[string]any{"items": items})
}
func UpdateDocumentIdentity(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	id := mux.Vars(r)["document_id"]
	var row orm.Document
	if store.DB().Where("id=? AND deleted_at IS NULL", id).Take(&row).Error != nil {
		replyError(w, 404, "NOT_FOUND", "document not found")
		return
	}
	if !doc.HasDatasetPermission(r, row.DatasetID, acl.PermissionDatasetWrite) {
		replyError(w, 403, "FORBIDDEN", "document is not writable")
		return
	}
	var req struct {
		Kind       string    `json:"kind"`
		Reidentify bool      `json:"reidentify"`
		Paper      WorkInput `json:"paper"`
	}
	var raw json.RawMessage
	if !parseJSON(r, &raw) || json.Unmarshal(raw, &req) != nil {
		replyError(w, 400, "INVALID_REQUEST", "invalid identity update")
		return
	}
	var original map[string]json.RawMessage
	_ = json.Unmarshal(raw, &original)
	metadata := original["paper"]
	if len(metadata) == 0 || string(metadata) == "null" {
		metadata = json.RawMessage(`{}`)
	}
	if req.Reidentify {
		row.CreateUserID = user
		err := store.DB().Transaction(func(tx *gorm.DB) error { return enqueueIdentify(r.Context(), tx, row, true) })
		if err != nil {
			replyError(w, 500, "QUEUE_FAILED", err.Error())
			return
		}
		common.ReplyJSON(w, map[string]string{"status": "pending"})
		return
	}
	if req.Kind != "paper" && req.Kind != "other" && req.Kind != "unknown" {
		replyError(w, 400, "INVALID_KIND", "kind must be paper, other or unknown")
		return
	}
	err := store.DB().Transaction(func(tx *gorm.DB) error {
		state := orm.AcademicDocumentState{DocumentID: id, DatasetID: row.DatasetID, Kind: "unknown", Status: "pending", Revision: 1, UpdatedAt: time.Now().UTC()}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; e != nil {
			return e
		}
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("document_id=?", id).Take(&state).Error; e != nil {
			return e
		}
		workID := ""
		if req.Kind == "paper" {
			if state.PaperID != "" {
				var existing orm.AcademicWork
				if e := tx.Where("id=?", state.PaperID).Take(&existing).Error; e != nil {
					return e
				}
				arxiv, _ := NormalizeArxivID(req.Paper.ArxivID)
				if (req.Paper.DOI == "" || NormalizeDOI(req.Paper.DOI) == existing.DOINormalized) && (req.Paper.ArxivID == "" || arxiv == existing.ArxivIDBase) {
					req.Paper.WorkID = state.PaperID
				}
			}

			if req.Paper.Title == "" {
				req.Paper.Title = strings.TrimSuffix(row.DisplayName, filepath.Ext(row.DisplayName))
			}
			work, method, e := findOrCreateWork(r.Context(), tx, req.Paper)
			if e != nil {
				return e
			}
			workID = work.ID
			link := orm.AcademicWorkDocument{AcademicWorkID: workID, DatasetID: row.DatasetID, DocumentID: id, VersionKind: "unknown", SourceProvider: "manual", MatchMethod: method, MatchConfidence: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if e := saveDocumentWorkLink(tx, link); e != nil {
				return e
			}
		} else {
			if e := tx.Where("document_id=?", id).Delete(&orm.AcademicWorkDocument{}).Error; e != nil {
				return e
			}
		}
		return tx.Model(&state).Updates(map[string]any{"kind": req.Kind, "status": "succeeded", "manual": true, "metadata_json": metadata, "revision": state.Revision + 1, "paper_id": workID, "error": "", "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		replyError(w, 500, "UPDATE_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]string{"status": "succeeded"})
}
func SetImportance(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	id := mux.Vars(r)["paper_id"]
	var req struct {
		IsImportant bool `json:"is_important"`
	}
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "invalid importance")
		return
	}
	var work orm.AcademicWork
	if store.DB().Where("id=?", id).Take(&work).Error != nil {
		replyError(w, 404, "NOT_FOUND", "paper not found")
		return
	}
	row := orm.AcademicUserPaperState{UserID: user, PaperID: id, IsImportant: req.IsImportant, UpdatedAt: time.Now().UTC()}
	if err := store.DB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "paper_id"}}, DoUpdates: clause.AssignmentColumns([]string{"is_important", "updated_at"})}).Create(&row).Error; err != nil {
		replyError(w, 500, "UPDATE_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]bool{"is_important": req.IsImportant})
}

// Structured imports already bind a verified source identity; a queued PDF
// recognizer must not replace that binding or its source/version information.
func bindImportedPaper(tx *gorm.DB, document orm.Document, work orm.AcademicWork) error {
	state := orm.AcademicDocumentState{DocumentID: document.ID, DatasetID: document.DatasetID, Kind: "paper", Status: "succeeded", PaperID: work.ID, Revision: 1, UpdatedAt: time.Now().UTC()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "document_id"}}, DoUpdates: clause.Assignments(map[string]any{"kind": "paper", "status": "succeeded", "paper_id": work.ID, "error": "", "updated_at": time.Now().UTC()}), Where: clause.Where{Exprs: []clause.Expression{clause.Eq{Column: clause.Column{Table: "academic_document_states", Name: "manual"}, Value: false}}}}).Create(&state).Error
}

func saveDocumentWorkLink(tx *gorm.DB, link orm.AcademicWorkDocument) error {
	var prior orm.AcademicWorkDocument
	if err := tx.Where("document_id=?", link.DocumentID).Take(&prior).Error; err == nil {
		link.SourceProvider = prior.SourceProvider
		link.SourceLocator = prior.SourceLocator
		link.SourceVersion = prior.SourceVersion
		link.VersionKind = prior.VersionKind
		link.ContentSHA256 = prior.ContentSHA256
		link.IsPreferredVersion = prior.IsPreferredVersion
		link.CreatedAt = prior.CreatedAt
		if err := tx.Where("document_id=?", link.DocumentID).Delete(&orm.AcademicWorkDocument{}).Error; err != nil {
			return err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&link).Error
}
