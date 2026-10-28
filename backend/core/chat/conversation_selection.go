package chat

import (
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Read the complete, lightweight selection in one SQL statement. Unlike the
// activity-sorted history pages, this set cannot skip rows when activity changes
// between HTTP requests. The caller supplies the normal visibility predicates.
func listConversationSelection(w http.ResponseWriter, r *http.Request, query *gorm.DB, userID string) {
	items := make([]struct {
		ConversationID  string     `json:"conversation_id"`
		GroupID         *string    `json:"group_id"`
		PinnedAt        *time.Time `json:"pinned_at"`
		OrganizingRunID *string    `json:"organizing_run_id"`
	}, 0)
	query = query.WithContext(r.Context()).
		Where("conversations.parent_conversation_id IS NULL OR conversations.parent_conversation_id = ''")
	groupSQL := `(SELECT sg.id FROM conversation_group_members sm
		JOIN conversation_groups sg ON sg.id = sm.group_id
		WHERE sm.conversation_id = conversations.id AND sm.user_id = ?
		AND sg.user_id = ? AND sg.deleted_at IS NULL LIMIT 1)`
	if groupID := strings.TrimSpace(r.URL.Query().Get("selection_group_id")); groupID != "" {
		query = query.Where(groupSQL+" = ?", userID, userID, groupID)
	}
	// Aggregate the user's active family locks once. A correlated lookup per root
	// would repeatedly scan historical snapshots, including already finished runs.
	// Archive/delete also lock dependent side chats; project members retain their
	// existing organizer-lock exemption.
	rootIDSQL := `CASE WHEN locked_conversation.relation_type = 'sidechat'
		AND locked_conversation.parent_conversation_id IS NOT NULL AND locked_conversation.parent_conversation_id <> ''
		THEN locked_conversation.parent_conversation_id ELSE locked_conversation.id END`
	// Project the real ID column rather than the CASE expression so SQLite can
	// index the derived join key with the same text affinity as conversations.id.
	lockSQL := `LEFT JOIN (SELECT family_root.id AS conversation_id, MIN(si.run_id) AS organizing_run_id
		FROM conversation_organizer_snapshot_items si
		JOIN conversation_organizer_runs sr ON sr.id = si.run_id
		JOIN conversations locked_conversation ON locked_conversation.id = si.conversation_id AND locked_conversation.create_user_id = ?
		JOIN conversations family_root ON family_root.id = (` + rootIDSQL + `) AND family_root.create_user_id = ?
		WHERE si.user_id = ? AND sr.status IN ('pending', 'running', 'applying')
		AND NOT EXISTS (SELECT 1 FROM conversation_group_members pm
			JOIN conversation_groups pg ON pg.id = pm.group_id
			WHERE pm.conversation_id = si.conversation_id AND pg.kind = 'project')
		GROUP BY family_root.id) selection_locks ON selection_locks.conversation_id = conversations.id`
	if err := query.Joins(lockSQL, userID, userID, userID).
		Select("conversations.id AS conversation_id, "+groupSQL+" AS group_id, conversations.pinned_at, selection_locks.organizing_run_id", userID, userID).
		Order("conversations.id").Scan(&items).Error; err != nil {
		writeConversationJSON(w, http.StatusInternalServerError, map[string]string{"message": "load conversation selection failed"})
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{
		"conversations": items, "total_size": len(items), "next_page_token": "", "selection_complete": true,
	})
}
