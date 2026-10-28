package common

import (
	"strings"

	"gorm.io/gorm"
)

// ConversationGroupKeywordIDs matches the organization labels used by every
// conversation view, including a project's directory path.
func ConversationGroupKeywordIDs(db *gorm.DB, userID, keyword string) *gorm.DB {
	pattern := "%" + strings.TrimSpace(keyword) + "%"
	return db.Table("conversation_groups AS keyword_groups").Select("keyword_groups.id").
		Where("keyword_groups.user_id = ? AND keyword_groups.deleted_at IS NULL", userID).
		Where("LOWER(keyword_groups.name) LIKE LOWER(?) OR LOWER(keyword_groups.project_path) LIKE LOWER(?)", pattern, pattern)
}

// ConversationKeywordIDs keeps keyword matching independent of pinning and
// placement. Callers retain their source, mode and lifecycle visibility filters.
// A matching group name or project path includes all its otherwise visible members.
func ConversationKeywordIDs(db *gorm.DB, userID, keyword string) *gorm.DB {
	// Keep both sides on the database's case-folding rules. Go's Unicode
	// lowercasing would lose exact non-ASCII matches with SQLite's ASCII LOWER.
	pattern := "%" + strings.TrimSpace(keyword) + "%"
	summaries := db.Table("conversation_opening_metadata AS keyword_opening").Select("1").
		Where("keyword_opening.conversation_id = keyword_conversations.id AND keyword_opening.user_id = ?", userID).
		Where("LOWER(keyword_opening.summary) LIKE LOWER(?)", pattern)
	groups := db.Table("conversation_group_members AS keyword_members").Select("1").
		Where("keyword_members.conversation_id = keyword_conversations.id AND keyword_members.user_id = ?", userID).
		Where("keyword_members.group_id IN (?)", ConversationGroupKeywordIDs(db, userID, keyword))
	return db.Table("conversations AS keyword_conversations").Select("keyword_conversations.id").
		Where("keyword_conversations.create_user_id = ?", userID).
		Where("LOWER(keyword_conversations.display_name) LIKE LOWER(?) OR LOWER(keyword_conversations.source_display_name) LIKE LOWER(?) OR LOWER(keyword_conversations.source_document_id) LIKE LOWER(?) OR EXISTS (?) OR EXISTS (?)",
			pattern, pattern, pattern, summaries, groups)
}
