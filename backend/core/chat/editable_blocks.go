package chat

import (
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
	"regexp"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const maxEditableBlockBytes = 2 * 1024 * 1024

var editableFencePattern = regexp.MustCompile("(?ms)```editable[ \\t]*\\r?\\n(.*?)\\r?\\n```")

func replaceEditableBlock(result, baseContent, nextContent string) (string, bool) {
	matches := editableFencePattern.FindAllStringSubmatchIndex(result, -1)
	for _, match := range matches {
		if len(match) < 4 || result[match[2]:match[3]] != baseContent {
			continue
		}
		return result[:match[2]] + nextContent + result[match[3]:], true
	}
	return result, false
}

// PatchEditableBlock persists one completed main-Agent ```editable block.
func PatchEditableBlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationID string `json:"conversation_id"`
		HistoryID      string `json:"history_id"`
		BaseContent    string `json:"base_content"`
		Content        string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	body.ConversationID = strings.TrimSpace(body.ConversationID)
	body.HistoryID = strings.TrimSpace(body.HistoryID)
	if body.ConversationID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	if body.HistoryID == "" {
		common.ReplyErr(w, "history_id required", http.StatusBadRequest)
		return
	}
	if len(body.Content) > maxEditableBlockBytes {
		common.ReplyErr(w, "editable content exceeds the 2 MiB limit", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	db := store.DB()
	var nextResult string
	status := http.StatusInternalServerError
	err := conversationCheckpoint(r.Context(), db, body.ConversationID, func(tx *gorm.DB) error {
		var conversation orm.Conversation
		if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", body.ConversationID, userID).Take(&conversation).Error; err != nil {
			status = http.StatusNotFound
			return err
		}
		var history orm.ChatHistory
		if err := tx.Where("id = ? AND conversation_id = ?", body.HistoryID, body.ConversationID).Take(&history).Error; err != nil {
			status = http.StatusNotFound
			return err
		}
		var flags struct {
			ReadOnly bool `json:"fork_read_only"`
		}
		_ = json.Unmarshal(history.Ext, &flags)
		if flags.ReadOnly || history.RunStatus == "generating" {
			status = http.StatusConflict
			return forkFail("SOURCE_NOT_SETTLED")
		}
		var found bool
		nextResult, found = replaceEditableBlock(history.Result, body.BaseContent, body.Content)
		if !found {
			status = http.StatusConflict
			return forkFail("SOURCE_CHANGED")
		}
		result := tx.Model(&orm.ChatHistory{}).Where("id = ? AND conversation_id = ? AND result = ?", history.ID, body.ConversationID, history.Result).Updates(map[string]any{"result": nextResult, "update_time": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			status = http.StatusConflict
			return forkFail("SOURCE_CHANGED")
		}
		return nil
	})
	if err != nil {
		common.ReplyErr(w, "editable block unavailable or changed; refresh and retry", status)
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{
		"content": body.Content,
		"result":  nextResult,
	})
}
