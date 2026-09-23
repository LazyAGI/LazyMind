package common

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// ConversationSourceIDs uses external bindings to classify conversations, including
// the visibility rules for imported sessions. An empty filter selects all sources.
func ConversationSourceIDs(db *gorm.DB, userID, assistants string) (*gorm.DB, error) {
	q := db.Model(&orm.Conversation{}).Select("conversations.id").Where("create_user_id = ?", userID)
	external := db.Model(&orm.ExternalAgentBinding{}).Select("1").Where("external_agent_bindings.conversation_id = conversations.id AND external_agent_bindings.created_by_user_id = ?", userID)
	visible := db.Table("external_agent_bindings AS visible_bindings").Select("1").
		Joins("LEFT JOIN external_agent_sessions AS visible_sessions ON visible_sessions.owner_user_id = visible_bindings.created_by_user_id AND visible_sessions.provider = visible_bindings.provider AND visible_sessions.host_id = visible_bindings.host_id AND visible_sessions.provider_thread_id = visible_bindings.provider_thread_id").
		Where("visible_bindings.conversation_id = conversations.id AND visible_bindings.created_by_user_id = ?", userID).
		Where("visible_bindings.managed_by_lazymind = ? OR visible_sessions.active = ?", true, true)
	assistants = strings.ToLower(strings.TrimSpace(assistants))
	if assistants == "" {
		return q.Where("NOT EXISTS (?) OR EXISTS (?)", external, visible), nil
	}
	providers := []string{}
	includeLazyMind := false
	for _, candidate := range strings.Split(assistants, ",") {
		candidate = strings.TrimSpace(candidate)
		switch candidate {
		case "lazymind":
			includeLazyMind = true
		case "codex", "cursor", "workbuddy":
			providers = append(providers, candidate)
		default:
			return nil, errors.New("assistants contains an unsupported chat executor")
		}
	}
	if includeLazyMind && len(providers) > 0 {
		return q.Where("NOT EXISTS (?) OR EXISTS (?)", external, visible.Where("visible_bindings.provider IN ?", providers)), nil
	}
	if includeLazyMind {
		return q.Where("NOT EXISTS (?)", external), nil
	}
	return q.Where("EXISTS (?)", visible.Where("visible_bindings.provider IN ?", providers)), nil
}
