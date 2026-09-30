package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
)

// The caller holds the user's group transaction, so all descendants share one
// numbering sequence. Provenance survives deletion and avoids parsing titles
// that may legitimately contain parentheses or have been renamed by the user.
func nextForkTitle(tx *gorm.DB, userID string, source orm.Conversation) (string, error) {
	rootID, title := source.ID, source.DisplayName
	seen := map[string]bool{}
	for {
		if seen[rootID] {
			return "", forkFail("SOURCE_UNAVAILABLE")
		}
		seen[rootID] = true
		var origin orm.ConversationForkOrigin
		err := tx.Where("conversation_id=?", rootID).Take(&origin).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return "", err
		}
		rootID, title = origin.SourceConversationID, origin.SourceTitleSnapshot
	}
	if rootID != source.ID {
		var root orm.Conversation
		err := tx.Select("display_name").Where("id=? AND create_user_id=?", rootID, userID).Take(&root).Error
		if err == nil {
			title = root.DisplayName
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
	}
	var count int64
	if err := tx.Raw(`WITH RECURSIVE branches(conversation_id) AS (
		SELECT conversation_id FROM conversation_fork_origins WHERE source_conversation_id = ?
		UNION
		SELECT o.conversation_id FROM conversation_fork_origins o JOIN branches b ON o.source_conversation_id = b.conversation_id
	) SELECT COUNT(*) FROM branches`, rootID).Scan(&count).Error; err != nil {
		return "", err
	}
	for strings.HasSuffix(title, " · Fork") {
		title = strings.TrimSuffix(title, " · Fork")
	}
	suffix := fmt.Sprintf("（%d）", count+1)
	runes := []rune(title)
	if limit := maxConversationDisplayNameLength - len([]rune(suffix)); len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes) + suffix, nil
}

func inheritForkGroup(ctx context.Context, tx *gorm.DB, userID, sourceID, targetID string) error {
	if !tx.Migrator().HasTable(&orm.ConversationGroupMember{}) {
		return nil
	}
	var group orm.ConversationGroup
	err := tx.Table("conversation_groups g").Select("g.*").Joins("JOIN conversation_group_members m ON m.group_id=g.id").Where("m.conversation_id=? AND m.user_id=? AND g.user_id=?", sourceID, userID, userID).Take(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if group.Kind == conversationgroup.KindProject {
		return conversationgroup.InheritProject(ctx, tx, userID, sourceID, targetID)
	}
	return conversationgroup.AttachNewConversation(ctx, tx, userID, targetID, group.ID)
}
