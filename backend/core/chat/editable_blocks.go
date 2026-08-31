package chat

import (
	"encoding/json"
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
	var conversation orm.Conversation
	if err := db.Where("id = ? AND create_user_id = ?", body.ConversationID, userID).
		First(&conversation).Error; err != nil {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}
	var history orm.ChatHistory
	if err := db.Where("id = ? AND conversation_id = ?", body.HistoryID, body.ConversationID).
		First(&history).Error; err != nil {
		common.ReplyErr(w, "history not found", http.StatusNotFound)
		return
	}
	nextResult, found := replaceEditableBlock(history.Result, body.BaseContent, body.Content)
	if !found {
		common.ReplyErr(w, "editable block changed; refresh and retry", http.StatusConflict)
		return
	}
	now := time.Now()
	updated := db.Model(&orm.ChatHistory{}).
		Where("id = ? AND conversation_id = ? AND result = ?", history.ID, body.ConversationID, history.Result).
		Updates(map[string]any{"result": nextResult, "update_time": now})
	if updated.Error != nil {
		common.ReplyErr(w, "save editable block failed", http.StatusInternalServerError)
		return
	}
	if updated.RowsAffected != 1 {
		common.ReplyErr(w, "editable block changed; refresh and retry", http.StatusConflict)
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{
		"content": body.Content,
		"result":  nextResult,
	})
}
