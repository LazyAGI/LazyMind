package conversationgroup

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// DetachPinnedConversation removes ordinary membership using the same undo
// fence as a manual move. Project membership remains bound to its directory.
// The caller owns the user transaction and conversation lock.
func DetachPinnedConversation(ctx context.Context, tx *gorm.DB, uid, conversationID string) error {
	if !tx.Migrator().HasTable(&orm.ConversationGroupMember{}) {
		return nil
	}
	var group orm.ConversationGroup
	err := tx.Table("conversation_groups g").Select("g.*").Joins("JOIN conversation_group_members m ON m.group_id=g.id").Where("m.conversation_id=? AND m.user_id=? AND g.user_id=?", conversationID, uid, uid).Take(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if group.Kind == KindProject {
		return nil
	}
	if err := RequireOrganizerUnlocked(ctx, tx, uid, []string{conversationID}, ""); err != nil {
		return err
	}
	_, err = moveMembershipTx(tx, uid, conversationID, nil, CreatedByUser, "")
	return err
}
