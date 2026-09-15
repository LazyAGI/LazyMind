package conversationgroup

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// Membership and placement share the owner's transaction: an invalid anchor or
// failed order write rolls back the move as well. Reuse durable history_order;
// UpdateColumn deliberately leaves conversation activity timestamps unchanged.
// An empty target prepends a newly assigned member without reusing its old rank.
func reorderGroupMemberTx(tx *gorm.DB, uid, groupID, movedID, targetID, position string) error {
	memberIDs := tx.Model(&orm.ConversationGroupMember{}).Select("conversation_id").Where("user_id=? AND group_id=?", uid, groupID)
	var rows []orm.Conversation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN (?) AND create_user_id=? AND pinned_at IS NULL AND deleted_at IS NULL AND archived_at IS NULL AND is_ephemeral=? AND (parent_conversation_id IS NULL OR parent_conversation_id='')", memberIDs, uid, false).
		Order("CASE WHEN history_order IS NULL THEN 0 ELSE 1 END, history_order ASC, updated_at DESC, id ASC").Find(&rows).Error; err != nil {
		return err
	}
	foundMoved, foundTarget := false, targetID == ""
	ids := make([]string, 0, len(rows))
	if targetID == "" {
		ids = append(ids, movedID)
	}
	for _, row := range rows {
		if row.ID == movedID {
			foundMoved = true
			continue
		}
		if row.ID == targetID {
			foundTarget = true
			if position == "before" {
				ids = append(ids, movedID)
			}
		}
		ids = append(ids, row.ID)
		if row.ID == targetID && position == "after" {
			ids = append(ids, movedID)
		}
	}
	if !foundMoved || !foundTarget {
		return gorm.ErrRecordNotFound
	}
	for index, id := range ids {
		if err := tx.Model(&orm.Conversation{}).Where("id=? AND create_user_id=?", id, uid).UpdateColumn("history_order", index+1).Error; err != nil {
			return err
		}
	}
	return nil
}
