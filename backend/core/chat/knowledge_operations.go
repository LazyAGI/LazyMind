package chat

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

func mergeKnowledgeOperation(ext json.RawMessage, operation map[string]any) json.RawMessage {
	callID, _ := operation["call_id"].(string)
	action, _ := operation["action"].(string)
	if callID == "" || action == "" {
		return ext
	}
	data := map[string]any{}
	_ = json.Unmarshal(ext, &data)
	if data == nil {
		data = map[string]any{}
	}
	items, _ := data["knowledge_operations"].([]any)
	for i, raw := range items {
		if item, ok := raw.(map[string]any); ok && item["call_id"] == callID {
			items[i] = operation
			data["knowledge_operations"] = items
			return marshalChatHistoryExt(data)
		}
	}
	data["knowledge_operations"] = append(items, operation)
	return marshalChatHistoryExt(data)
}

func persistKnowledgeOperation(ctx context.Context, db *gorm.DB, conversationID, historyID string, operation map[string]any) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var history orm.ChatHistory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND conversation_id = ?", historyID, conversationID).Take(&history).Error; err != nil {
			return err
		}
		return tx.Model(&history).Update("ext", mergeKnowledgeOperation(history.Ext, operation)).Error
	})
}
