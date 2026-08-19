package workflow

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type workflowTrashItem struct {
	ID                         string `json:"id"`
	Name                       string `json:"name"`
	WorkflowID                 string `json:"workflow_id,omitempty"`
	PublishedWorkflowRef       string `json:"published_workflow_ref,omitempty"`
	PublishedStatusBeforeTrash string `json:"published_status_before_trash,omitempty"`
	DeletedAt                  string `json:"deleted_at"`
	TrashExpiresAt             string `json:"trash_expires_at"`
	UpdatedAt                  string `json:"updated_at"`
}

func workflowTrashItemFromDraft(db *gorm.DB, draft orm.WorkflowDraft) workflowTrashItem {
	item := workflowTrashItem{
		ID: draft.ID, Name: draft.Name, WorkflowID: draft.WorkflowID,
		PublishedStatusBeforeTrash: draft.PublishedStatusBeforeTrash,
		UpdatedAt:                  draft.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if draft.DeletedAt != nil {
		item.DeletedAt = draft.DeletedAt.UTC().Format(time.RFC3339)
	}
	if draft.TrashExpiresAt != nil {
		item.TrashExpiresAt = draft.TrashExpiresAt.UTC().Format(time.RFC3339)
	}
	if draft.WorkflowID != "" {
		var resource orm.WorkflowResource
		if db.Where("owner_user_id = ? AND plugin_id = ?", draft.CreatedBy, draft.WorkflowID).First(&resource).Error == nil {
			item.PublishedWorkflowRef = resource.WorkflowRef
		}
	}
	return item
}

func ListWorkflowDraftTrash(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	page, pageSize := 1, 20
	if value, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && value > 0 {
		page = value
	}
	if value, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && value > 0 && value <= 100 {
		pageSize = value
	}
	db := store.DB()
	query := db.Model(&orm.WorkflowDraft{}).Where("created_by = ? AND deleted_at IS NOT NULL", userID)
	if keyword := strings.TrimSpace(r.URL.Query().Get("keyword")); keyword != "" {
		like := "%" + strings.ToLower(keyword) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(plugin_id) LIKE ?", like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}
	var drafts []orm.WorkflowDraft
	if err := query.Order("deleted_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&drafts).Error; err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}
	items := make([]workflowTrashItem, 0, len(drafts))
	for _, draft := range drafts {
		items = append(items, workflowTrashItemFromDraft(db, draft))
	}
	common.ReplyOK(w, map[string]any{"records": items, "total": total, "page": page, "page_size": pageSize})
}

func RestoreWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	draftID, userID := common.PathVar(r, "draft_id"), common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var draft orm.WorkflowDraft
		if err := tx.Where("id = ? AND created_by = ? AND deleted_at IS NOT NULL", draftID, userID).First(&draft).Error; err != nil {
			return err
		}
		if draft.WorkflowID != "" {
			var conflicts int64
			if err := tx.Model(&orm.WorkflowDraft{}).
				Where("created_by = ? AND plugin_id = ? AND deleted_at IS NULL AND id <> ?", userID, draft.WorkflowID, draft.ID). // workflow-naming: persistence
				Count(&conflicts).Error; err != nil {
				return err
			}
			if conflicts > 0 {
				return errWorkflowDraftRestoreConflict
			}
		}
		if draft.WorkflowID != "" && draft.PublishedStatusBeforeTrash != "" {
			if err := tx.Model(&orm.WorkflowResource{}).
				Where("owner_user_id = ? AND plugin_id = ?", userID, draft.WorkflowID). // workflow-naming: persistence
				Updates(map[string]any{"status": draft.PublishedStatusBeforeTrash, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&orm.WorkflowDraft{}).
			Where("id = ? AND created_by = ? AND deleted_at IS NOT NULL", draftID, userID).
			Updates(map[string]any{
				"deleted_at": nil, "trash_expires_at": nil,
				"published_status_before_trash": "", "updated_at": time.Now().UTC(),
			}).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, errWorkflowDraftRestoreConflict) {
		common.ReplyErr(w, "workflow id already exists", http.StatusConflict)
		return
	}
	if err != nil {
		common.ReplyErr(w, "restore failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}

var errWorkflowDraftRestoreConflict = errors.New("workflow id already exists")

func purgeWorkflowDraft(tx *gorm.DB, draft orm.WorkflowDraft) error {
	if err := tx.Where("draft_id = ? AND user_id = ?", draft.ID, draft.CreatedBy).Delete(&orm.WorkflowRepairRun{}).Error; err != nil {
		return err
	}
	if err := tx.Where("draft_id = ? AND user_id = ?", draft.ID, draft.CreatedBy).Delete(&orm.WorkflowGenerationAnalysis{}).Error; err != nil {
		return err
	}
	if draft.WorkflowID != "" {
		var siblingDrafts int64
		if err := tx.Model(&orm.WorkflowDraft{}).
			Where("created_by = ? AND plugin_id = ? AND id <> ?", draft.CreatedBy, draft.WorkflowID, draft.ID). // workflow-naming: persistence
			Count(&siblingDrafts).Error; err != nil {
			return err
		}
		// A replacement draft with the same workflow identifier owns the shared
		// published resource. Purging the old trash item must not delete it.
		if siblingDrafts == 0 {
			var resource orm.WorkflowResource
			err := tx.Where("owner_user_id = ? AND plugin_id = ?", draft.CreatedBy, draft.WorkflowID).First(&resource).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				var revisionIDs []string
				if err := tx.Model(&orm.WorkflowRevision{}).Where("plugin_resource_id = ?", resource.ID).Pluck("id", &revisionIDs).Error; err != nil {
					return err
				}
				var blobHashes []string
				if len(revisionIDs) > 0 {
					if err := tx.Model(&orm.WorkflowRevisionEntry{}).
						Where("revision_id IN ? AND blob_hash IS NOT NULL", revisionIDs).Distinct().Pluck("blob_hash", &blobHashes).Error; err != nil {
						return err
					}
					if err := tx.Where("revision_id IN ?", revisionIDs).Delete(&orm.WorkflowRevisionEntry{}).Error; err != nil {
						return err
					}
					if err := tx.Where("id IN ?", revisionIDs).Delete(&orm.WorkflowRevision{}).Error; err != nil {
						return err
					}
				}
				if err := tx.Where("user_id = ? AND plugin_ref = ?", draft.CreatedBy, resource.WorkflowRef).Delete(&orm.UserWorkflowSetting{}).Error; err != nil {
					return err
				}
				if err := tx.Delete(&resource).Error; err != nil {
					return err
				}
				for _, hash := range blobHashes {
					var references int64
					if err := tx.Model(&orm.WorkflowRevisionEntry{}).Where("blob_hash = ?", hash).Count(&references).Error; err != nil {
						return err
					}
					if references == 0 {
						if err := tx.Delete(&orm.WorkflowBlob{}, "hash = ?", hash).Error; err != nil {
							return err
						}
					}
				}
			}
		}
	}
	result := tx.Where("id = ? AND created_by = ? AND deleted_at IS NOT NULL", draft.ID, draft.CreatedBy).
		Delete(&orm.WorkflowDraft{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func PurgeWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	draftID, userID := common.PathVar(r, "draft_id"), common.UserID(r)
	db := store.DB().WithContext(r.Context())
	err := db.Transaction(func(tx *gorm.DB) error {
		var draft orm.WorkflowDraft
		if err := tx.Where("id = ? AND created_by = ? AND deleted_at IS NOT NULL", draftID, userID).First(&draft).Error; err != nil {
			return err
		}
		return purgeWorkflowDraft(tx, draft)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, "purge failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}

func EmptyWorkflowDraftTrash(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	db := store.DB().WithContext(r.Context())
	var drafts []orm.WorkflowDraft
	if err := db.Where("created_by = ? AND deleted_at IS NOT NULL", userID).Find(&drafts).Error; err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		for _, draft := range drafts {
			if err := purgeWorkflowDraft(tx, draft); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		common.ReplyErr(w, "empty trash failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"purged": len(drafts)})
}

// PurgeExpiredWorkflowTrash applies the same permanent-delete operation as the
// manual endpoint and isolates failures per draft.
func PurgeExpiredWorkflowTrash(ctx context.Context, db *gorm.DB, now time.Time) (purged, failed int) {
	var drafts []orm.WorkflowDraft
	if err := db.WithContext(ctx).
		Where("deleted_at IS NOT NULL AND trash_expires_at IS NOT NULL AND trash_expires_at <= ?", now).
		Find(&drafts).Error; err != nil {
		return 0, 1
	}
	for _, draft := range drafts {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return purgeWorkflowDraft(tx, draft)
		})
		if err != nil {
			failed++
			continue
		}
		purged++
	}
	return purged, failed
}
