package chat

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
	"net/http"
	"time"
)

func trashConversationsTx(ctx context.Context, tx *gorm.DB, userID string, ids []string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	if err := tx.Model(&orm.Conversation{}).Where("id IN ? AND create_user_id=? AND deleted_at IS NULL", ids, userID).Updates(map[string]any{
		"deleted_at": now, "trash_expires_at": now.Add(30 * 24 * time.Hour), "archived_at": nil, "archive_folder_id": nil, "updated_at": now,
	}).Error; err != nil {
		return err
	}
	return taskcenter.ArchiveTasksForConversations(ctx, tx, userID, ids, taskcenter.ArchivedReasonConversationTrash, now)
}

func DeleteConversationGroup(w http.ResponseWriter, r *http.Request) {
	uid := store.UserID(r)
	if uid == "" {
		uid = "0"
	}
	id := common.PathVar(r, "group_id")
	var group orm.ConversationGroup
	if err := store.DB().Where("id=? AND user_id=? AND deleted_at IS NULL", id, uid).Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "conversation group not found", 404)
		} else {
			common.ReplyErr(w, "internal server error", 500)
		}
		return
	}
	if group.Kind != conversationgroup.KindProject {
		conversationgroup.DeleteGroup(w, r)
		return
	}
	var ids []string
	err := conversationgroup.UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", id, uid).Take(&group).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.Conversation{}).Where("create_user_id=? AND deleted_at IS NULL AND id IN (SELECT conversation_id FROM conversation_group_members WHERE group_id=? AND user_id=?)", uid, id, uid).Order("id").Pluck("id", &ids).Error; err != nil {
			return err
		}
		var err error
		ids, err = expandOwnedConversationFamilyIDs(r.Context(), tx, uid, ids)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := trashConversationsTx(r.Context(), tx, uid, ids, now); err != nil {
			return err
		}
		return tx.Model(&group).Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "conversation group not found", 404)
		} else {
			common.ReplyErr(w, "internal server error", 500)
		}
		return
	}
	notifySessionEnvClear(ids...)
	writeConversationJSON(w, 200, map[string]any{})
}
