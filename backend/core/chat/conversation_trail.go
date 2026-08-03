package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const maxConversationTrailDepth = 1

type conversationTrailMetadata struct {
	ParentHistoryID     string   `json:"parent_history_id,omitempty"`
	Depth               int      `json:"depth,omitempty"`
	Source              string   `json:"source,omitempty"`
	ReferenceHistoryIDs []string `json:"reference_history_ids,omitempty"`
}

// ConversationTrailItem is the lightweight navigation projection of one chat turn.
// The parent/depth values are read from persisted reference metadata; this endpoint
// deliberately does not infer relationships from question text.
type ConversationTrailItem struct {
	HistoryID       string `json:"history_id"`
	Seq             int    `json:"seq"`
	Summary         string `json:"summary"`
	Question        string `json:"question"`
	Depth           int    `json:"depth"`
	ParentHistoryID string `json:"parent_history_id,omitempty"`
	Source          string `json:"source,omitempty"`
	CreateTime      string `json:"create_time"`
}

type ConversationTrailListResponse struct {
	ConversationID string                  `json:"conversation_id"`
	Name           string                  `json:"name"`
	Items          []ConversationTrailItem `json:"items"`
	TotalSize      int                     `json:"total_size"`
	NextPageToken  string                  `json:"next_page_token,omitempty"`
}

// GetConversationTrail handles GET /conversations/{name}:trail.
// It returns only navigation metadata and the persisted reference hierarchy.
func GetConversationTrail(w http.ResponseWriter, r *http.Request) {
	name := conversationNameFromPath(r)
	convID := conversationIDFromName(name)
	if convID == "" {
		common.ReplyErr(w, "invalid conversation name", http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	var conversation orm.Conversation
	if err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ?", convID, userID).
		First(&conversation).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "conversation not found", err), http.StatusNotFound)
		return
	}

	histories, err := loadConversationTrailHistories(r, convID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("load conversation trail failed: %v", err), http.StatusInternalServerError)
		return
	}

	items := make([]ConversationTrailItem, 0, len(histories))
	for _, history := range histories {
		items = append(items, conversationTrailItem(history))
	}

	pageSize, offset := parseConversationHistoryPage(r)
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}

	nextPageToken := ""
	if end < total {
		nextPageToken = encodeListPageToken(end, pageSize, total)
	}

	writeConversationJSON(w, http.StatusOK, ConversationTrailListResponse{
		ConversationID: convID,
		Name:           "conversations/" + convID,
		Items:          items[offset:end],
		TotalSize:      total,
		NextPageToken:  nextPageToken,
	})
}

func loadConversationTrailHistories(r *http.Request, convID string) ([]orm.ChatHistory, error) {
	var histories []orm.ChatHistory
	err := store.DB().WithContext(r.Context()).
		Select("id, seq, conversation_id, raw_content, ext, create_time, update_time").
		Where("conversation_id = ?", convID).
		Find(&histories).Error
	if err != nil {
		return nil, err
	}

	stateStore := store.State()
	if stateStore != nil {
		ids, _ := getGeneratingHistoryIDs(r.Context(), stateStore, convID)
		exists := make(map[string]struct{}, len(histories))
		for _, history := range histories {
			exists[history.ID] = struct{}{}
		}
		for _, historyID := range ids {
			if _, ok := exists[historyID]; ok {
				continue
			}
			input, inputErr := getChatInput(r.Context(), stateStore, convID, historyID)
			if inputErr != nil || input == nil || strings.TrimSpace(input.RawContent) == "" {
				continue
			}
			createdAt := time.UnixMilli(input.CreatedAt)
			histories = append(histories, orm.ChatHistory{
				ID:             historyID,
				Seq:            input.Seq,
				ConversationID: convID,
				RawContent:     input.RawContent,
				Ext:            input.Ext,
				TimeMixin:      orm.TimeMixin{CreateTime: createdAt, UpdateTime: createdAt},
			})
		}
	}

	sort.SliceStable(histories, func(i, j int) bool {
		if histories[i].Seq != histories[j].Seq {
			return histories[i].Seq < histories[j].Seq
		}
		return histories[i].CreateTime.Before(histories[j].CreateTime)
	})
	return histories, nil
}

func conversationTrailItem(history orm.ChatHistory) ConversationTrailItem {
	question := displayChatHistoryContent(history.RawContent)
	metadata := conversationTrailMetadataFromExt(history.Ext)
	if metadata.Depth < 0 {
		metadata.Depth = 0
	}
	if metadata.Depth > maxConversationTrailDepth {
		metadata.Depth = maxConversationTrailDepth
	}

	return ConversationTrailItem{
		HistoryID:       history.ID,
		Seq:             history.Seq,
		Summary:         summarizeConversationTrailQuestion(question),
		Question:        question,
		Depth:           metadata.Depth,
		ParentHistoryID: metadata.ParentHistoryID,
		Source:          metadata.Source,
		CreateTime:      history.CreateTime.UTC().Format(time.RFC3339),
	}
}

func conversationTrailMetadataFromExt(ext json.RawMessage) conversationTrailMetadata {
	if len(ext) == 0 {
		return conversationTrailMetadata{}
	}
	var payload struct {
		Trail conversationTrailMetadata `json:"trail"`
	}
	if err := json.Unmarshal(ext, &payload); err != nil {
		return conversationTrailMetadata{}
	}
	return payload.Trail
}

func summarizeConversationTrailQuestion(question string) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(question)), " ")
	if text == "" {
		return "未命名问题"
	}
	runes := []rune(text)
	if len(runes) <= 15 {
		return text
	}
	return string(runes[:14]) + "…"
}
