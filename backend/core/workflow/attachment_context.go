package workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func historyFilesFromConversation(db *gorm.DB, conversationID string) map[string][]string {
	var histories []orm.ChatHistory
	if err := db.
		Where("conversation_id = ?", strings.TrimSpace(conversationID)).
		Order("seq ASC").
		Find(&histories).Error; err != nil {
		return nil
	}

	filesByTurn := make(map[string][]string)
	for _, history := range histories {
		var ext struct {
			Input []map[string]any `json:"input"`
		}
		if len(history.Ext) == 0 || json.Unmarshal(history.Ext, &ext) != nil {
			continue
		}
		turn := fmt.Sprintf("%d", history.Seq)
		for _, item := range ext.Input {
			inputType, _ := item["input_type"].(string)
			inputType = strings.ToLower(strings.TrimSpace(inputType))
			if inputType != "image" && inputType != "file" {
				continue
			}
			uri, _ := item["uri"].(string)
			uri = strings.TrimSpace(uri)
			if uri == "" {
				continue
			}
			filesByTurn[turn] = append(filesByTurn[turn], uri)
		}
	}
	if len(filesByTurn) == 0 {
		return nil
	}
	return filesByTurn
}

// historyFilesFromParentAgentic recovers attachment paths from the ChatAgent
// agentic_config snapshot when conversation history Ext has not yet persisted them.
func historyFilesFromParentAgentic(parent map[string]any) map[string][]string {
	if len(parent) == 0 {
		return nil
	}
	if raw, ok := parent["history_files_per_turn"]; ok {
		if converted := coerceHistoryFilesPerTurn(raw); len(converted) > 0 {
			return converted
		}
	}
	files := compactStrings(stringSliceFromAny(parent["files"]))
	if len(files) == 0 {
		return nil
	}
	return map[string][]string{"1": files}
}

func coerceHistoryFilesPerTurn(raw any) map[string][]string {
	switch typed := raw.(type) {
	case map[string][]string:
		out := make(map[string][]string, len(typed))
		for key, paths := range typed {
			cleaned := compactStrings(paths)
			if len(cleaned) == 0 {
				continue
			}
			out[strings.TrimSpace(key)] = cleaned
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case map[string]any:
		out := make(map[string][]string, len(typed))
		for key, value := range typed {
			paths := compactStrings(stringSliceFromAny(value))
			if len(paths) == 0 {
				continue
			}
			out[strings.TrimSpace(key)] = paths
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
