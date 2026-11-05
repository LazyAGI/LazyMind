package academic

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

// DeleteLibraryPapers deletes this library's originals, not global scholarly works
// or copies in other libraries. Removed entries remain as background-import fences.
func DeleteLibraryPapers(w http.ResponseWriter, r *http.Request) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Items []libraryTarget `json:"items"`
	}
	if !parseJSON(r, &input) || !libraryTargets(input.Items) {
		replyError(w, 400, "INVALID_REQUEST", "请选择 1 至 500 篇论文并提供版本")
		return
	}
	ids := make([]string, 0, len(input.Items))
	for _, item := range input.Items {
		ids = append(ids, item.ID)
	}
	db := store.DB().WithContext(r.Context())
	// Include previously detached duplicates: otherwise their names remain occupied.
	var documents []orm.Document
	err := db.Where("dataset_id = ? AND deleted_at IS NULL AND id IN (?)", dataset,
		db.Model(&orm.PaperLibraryAttachment{}).Select("document_id").Where("dataset_id = ? AND paper_id IN ? AND document_id <> ''", dataset, ids)).Find(&documents).Error
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	refs := make([]doc.ResourceRef, 0, len(documents))
	for _, document := range documents {
		refs = append(refs, doc.ResourceRef{Kind: "document", KnowledgeBaseID: dataset, DocumentID: document.ID})
	}
	service, err := doc.NewResourceService(doc.ResourceServiceDeps{DB: db})
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	result, err := service.DeleteWithRelatedMutation(r.Context(), doc.ResourceOperationContext{Caller: sourceCaller(r)}, doc.DeleteResourcesRequest{Targets: refs}, func(tx *gorm.DB) error {
		// Any attachment addition/removal increments this revision. Lock all entries
		// before deleting anything, so stale confirmations roll the whole batch back.
		entries := make([]orm.PaperLibraryEntry, 0, len(input.Items))
		for _, item := range input.Items {
			entry, e := paperlibrary.Entry(tx, dataset, item.ID, item.Revision)
			if e != nil {
				return e
			}
			entries = append(entries, entry)
		}
		// Never delete a document still owned by a different paper. A conflict is
		// preferable to silently deleting another entry's attachment.
		if len(documents) > 0 {
			docIDs := make([]string, 0, len(documents))
			for _, d := range documents {
				docIDs = append(docIDs, d.ID)
			}
			var shared int64
			if e := tx.Model(&orm.PaperLibraryAttachment{}).Where("dataset_id = ? AND document_id IN ? AND paper_id NOT IN ? AND detached_at IS NULL", dataset, docIDs, ids).Count(&shared).Error; e != nil {
				return e
			}
			if shared > 0 {
				return invalidLibraryMutation("PAPER_ATTACHMENT_SHARED", "附件仍被其他论文使用，请先核对附件归属")
			}
		}
		now := time.Now().UTC()
		for _, entry := range entries {
			entry.RemovedAt = &now
			entry.FolderID = ""
			entry.TagsJSON = json.RawMessage(`[]`)
			if e := paperlibrary.SaveEntry(tx, &entry); e != nil {
				return e
			}
		}
		workIDs := make([]string, 0, len(entries))
		for _, entry := range entries {
			workIDs = append(workIDs, entry.AcademicWorkID)
		}
		if e := tx.Where("dataset_id = ? AND paper_id IN ?", dataset, workIDs).Delete(&orm.AcademicUserPaperState{}).Error; e != nil {
			return e
		}
		if e := paperlibrary.RemovePaperExtras(r.Context(), tx, dataset, ids); e != nil {
			return e
		}
		if e := tx.Where("dataset_id = ? AND paper_id IN ?", dataset, ids).Delete(&orm.PaperLibraryNote{}).Error; e != nil {
			return e
		}
		return tx.Model(&orm.PaperLibraryAttachment{}).Where("dataset_id = ? AND paper_id IN ?", dataset, ids).Updates(map[string]any{"detached_at": now, "is_primary": false, "updated_at": now}).Error
	})
	if err != nil {
		var resource *doc.ResourceError
		if errors.As(err, &resource) {
			status := 500
			switch resource.Code {
			case "FORBIDDEN":
				status = 403
			case "NOT_FOUND":
				status = 404
			case "CONFLICT":
				status = 409
			case "INVALID_ARGUMENT":
				status = 400
			case "OPERATION_TOO_LARGE":
				status = 413
			case "UNAVAILABLE":
				status = 503
			}
			replyError(w, status, resource.Code, resource.Message)
			return
		}
		replyLibraryError(w, err)
		return
	}
	cleanupPending := false
	for _, item := range result.Items {
		if item.CleanupStatus == "failed" {
			cleanupPending = true
		}
	}
	common.ReplyJSON(w, struct {
		libraryChanges
		CleanupPending bool `json:"cleanupPending,omitempty"`
	}{libraryChanges{Data: paperlibrary.EmptyData(), RemovedPaperIDs: ids}, cleanupPending})
}
