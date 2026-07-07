package plugin

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// loadConversationSearchConfig reads search_config JSON from the conversation row.
func loadConversationSearchConfig(db *gorm.DB, convID string) map[string]any {
	if db == nil || strings.TrimSpace(convID) == "" {
		return nil
	}
	var conv orm.Conversation
	if err := db.Where("id = ?", convID).First(&conv).Error; err != nil {
		return nil
	}
	if len(conv.SearchConfig) == 0 || string(conv.SearchConfig) == "{}" {
		return nil
	}
	var sc map[string]any
	if json.Unmarshal(conv.SearchConfig, &sc) != nil {
		return nil
	}
	return sc
}

// persistConversationSearchConfig overwrites search_config on the conversation row.
func persistConversationSearchConfig(db *gorm.DB, convID, userID string, sc map[string]any) error {
	if db == nil || strings.TrimSpace(convID) == "" || len(sc) == 0 {
		return nil
	}
	raw, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	q := db.Model(&orm.Conversation{}).Where("id = ?", convID)
	if strings.TrimSpace(userID) != "" {
		q = q.Where("create_user_id = ?", userID)
	}
	return q.Update("search_config", json.RawMessage(raw)).Error
}

// filtersFromConversation builds kb/creator/tag filters from conversation.search_config.
// Mirrors chat.buildChatRequestBody fallback when the client did not send filters.
func filtersFromConversation(db *gorm.DB, convID string) map[string]any {
	sc := loadConversationSearchConfig(db, convID)
	if len(sc) == 0 {
		return nil
	}
	out := map[string]any{}
	if kbIDs := datasetIDsFromSearchConfig(sc); len(kbIDs) > 0 {
		out["kb_id"] = kbIDs
	}
	if creators := stringSliceFromAny(sc["creators"]); len(creators) > 0 {
		out["creator"] = creators
	}
	if tags := stringSliceFromAny(sc["tags"]); len(tags) > 0 {
		out["tags"] = tags
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func datasetIDsFromSearchConfig(sc map[string]any) []string {
	if ids := stringSliceFromAny(sc["dataset_ids"]); len(ids) > 0 {
		return ids
	}
	rawList, _ := sc["dataset_list"].([]any)
	if len(rawList) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rawList))
	for _, item := range rawList {
		selector, _ := item.(map[string]any)
		if selector == nil {
			continue
		}
		id, _ := selector["id"].(string)
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func stringSliceFromAny(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
