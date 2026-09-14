package conversationgroup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"time"
)

func freeConversationQuery(db *gorm.DB, uid string) *gorm.DB {
	return db.Table("conversations c").Joins("LEFT JOIN conversation_group_members m ON m.conversation_id=c.id").Where("c.create_user_id=? AND c.deleted_at IS NULL AND c.archived_at IS NULL AND c.is_ephemeral=? AND c.is_task_conv=? AND c.parent_conversation_id IS NULL AND m.conversation_id IS NULL", uid, false, false)
}

func buildSnapshot(ctx context.Context, tx *gorm.DB, runID, uid string) (organizerSnapshot, []orm.ConversationOrganizerSnapshotItem, error) {
	type itemRow struct {
		ID, DisplayName, Summary        string
		TitleRevision, MetadataRevision int64
	}
	var rows []itemRow
	err := freeConversationQuery(tx.WithContext(ctx), uid).Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "c"}}).
		Select("c.id,c.display_name,COALESCE(o.summary,'') AS summary,c.title_revision,COALESCE(o.metadata_revision,0) AS metadata_revision").
		Joins("LEFT JOIN conversation_opening_metadata o ON o.conversation_id=c.id AND o.user_id=c.create_user_id").
		Order("c.created_at,c.id").Scan(&rows).Error
	if err != nil {
		return organizerSnapshot{}, nil, err
	}
	snap := organizerSnapshot{ID: runID, Conversations: make([]snapshotConversation, 0, len(rows)), Groups: make([]snapshotGroup, 0)}
	items := make([]orm.ConversationOrganizerSnapshotItem, 0, len(rows))
	now := time.Now().UTC()
	for _, row := range rows {
		items = append(items, orm.ConversationOrganizerSnapshotItem{ConversationID: row.ID, UserID: uid, Title: row.DisplayName, Summary: row.Summary, TitleRevision: row.TitleRevision, MetadataRevision: row.MetadataRevision, CreatedAt: now})
	}
	var groups []orm.ConversationGroup
	if err := tx.Where("user_id=? AND deleted_at IS NULL", uid).Order("created_at,id").Find(&groups).Error; err != nil {
		return organizerSnapshot{}, nil, err
	}
	for _, group := range groups {
		sg := snapshotGroup{ID: group.ID, Name: group.Name, Scope: group.Scope, Version: group.Version}
		snap.Groups = append(snap.Groups, sg)
	}
	return snap, items, nil
}

func snapshotDigest(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
