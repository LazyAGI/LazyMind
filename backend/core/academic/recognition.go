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
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

func init() {
	asyncjob.Register("academic.identify", runIdentify)
	doc.OnAcademicDocumentSubmitted = func(tx *gorm.DB, row orm.Document) error {
		var ds orm.Dataset
		if err := tx.Where("id=?", row.DatasetID).Take(&ds).Error; err != nil {
			return err
		}
		if ds.Scene != "academic_papers" || strings.ToLower(filepath.Ext(row.DisplayName)) != ".pdf" {
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
			err := db.Transaction(func(tx *gorm.DB) error {
				if e := bindImportedPaper(tx, row, work); e != nil {
					return e
				}
				_, e := registerRecognizedLibraryDocument(ctx, tx, row, work, false, "", "", "upload")
				return e
			})
			return asyncjob.Result{}, err
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
			if _, e := registerRecognizedLibraryDocument(ctx, tx, row, work, false, "", "", "upload"); e != nil {
				return e
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
	row, err := service.GetDatasetAccess(r.Context(), doc.DatasetGetRequest{UserID: user, DatasetID: dataset, Caller: caller(r)})
	return err == nil && row.Scene == "academic_papers"
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
	updatedWorkID := ""
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
			// Library metadata has the same transaction and attachment checks as the per-entry API.
			var entry orm.PaperLibraryEntry
			entryErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Table("paper_library_entries e").Select("e.*").Joins("JOIN paper_library_attachments a ON a.dataset_id=e.dataset_id AND a.paper_id=e.id AND a.detached_at IS NULL").Where("e.dataset_id=? AND a.document_id=? AND e.removed_at IS NULL", row.DatasetID, id).Take(&entry).Error
			if entryErr == nil {
				var currentWork orm.AcademicWork
				if e := tx.Where("id=?", entry.AcademicWorkID).Take(&currentWork).Error; e != nil {
					return e
				}
				m, e := paperlibrary.EffectiveMetadata(tx, entry, currentWork)
				if e != nil {
					return e
				}
				var fields map[string]json.RawMessage
				if e = json.Unmarshal(metadata, &fields); e != nil {
					return e
				}
				if _, ok := fields["title"]; ok {
					m.Title = req.Paper.Title
				}
				if _, ok := fields["authors"]; ok {
					m.Authors = req.Paper.Authors
				}
				if _, ok := fields["venue"]; ok {
					m.Venue = req.Paper.Venue
				}
				if _, ok := fields["year"]; ok {
					m.Year = req.Paper.Year
				}
				if _, ok := fields["abstract"]; ok {
					m.Abstract = req.Paper.Abstract
				}
				if _, ok := fields["doi"]; ok {
					m.Identifiers.DOI = req.Paper.DOI
				}
				if _, ok := fields["arxiv_id"]; ok {
					m.Identifiers.Arxiv = req.Paper.ArxivID
				}
				if e = validateLibraryMetadata(&m); e != nil {
					return e
				}
				if e = applyLibraryMetadata(r.Context(), tx, &entry, m); e != nil {
					return e
				}
				if e = paperlibrary.SaveEntry(tx, &entry); e != nil {
					return e
				}
				work.ID = entry.AcademicWorkID
			} else if !errors.Is(entryErr, gorm.ErrRecordNotFound) {
				return entryErr
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
		updatedWorkID = workID
		return tx.Model(&state).Updates(map[string]any{"kind": req.Kind, "status": "succeeded", "manual": true, "metadata_json": metadata, "revision": state.Revision + 1, "paper_id": workID, "error": "", "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		var invalid *libraryMutationError
		if errors.As(err, &invalid) {
			replyLibraryError(w, err)
			return
		}
		replyError(w, 500, "UPDATE_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]string{"status": "succeeded", "paper_id": updatedWorkID})
}
func SetImportance(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	id := mux.Vars(r)["paper_id"]
	var req struct {
		DatasetID   string `json:"dataset_id"`
		IsImportant bool   `json:"is_important"`
	}
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "invalid importance")
		return
	}
	req.DatasetID = strings.TrimSpace(req.DatasetID)
	if req.DatasetID == "" || len(req.DatasetID) > 255 {
		replyError(w, 400, "INVALID_REQUEST", "dataset_id is required")
		return
	}
	if !academicDataset(r, user, req.DatasetID) {
		replyError(w, 403, "FORBIDDEN", "academic knowledge base is not accessible")
		return
	}
	var work orm.AcademicWork
	if store.DB().Where("id=?", id).Take(&work).Error != nil {
		replyError(w, 404, "NOT_FOUND", "paper not found")
		return
	}
	row := orm.AcademicUserPaperState{UserID: user, DatasetID: req.DatasetID, PaperID: id, IsImportant: req.IsImportant, UpdatedAt: time.Now().UTC()}
	if err := store.DB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "dataset_id"}, {Name: "paper_id"}}, DoUpdates: clause.AssignmentColumns([]string{"is_important", "updated_at"})}).Create(&row).Error; err != nil {
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
