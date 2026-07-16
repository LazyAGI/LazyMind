package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
)

const maxConversationArtifactBytes = 2 * 1024 * 1024

// ConversationArtifactDTO is the common download-card shape for both main-Agent
// and SubAgent artifacts.
type ConversationArtifactDTO struct {
	ArtifactID     string          `json:"artifact_id"`
	ConversationID string          `json:"conversation_id"`
	HistoryID      string          `json:"history_id"`
	ProducerType   string          `json:"producer_type"`
	ProducerID     string          `json:"producer_id,omitempty"`
	Filename       string          `json:"filename,omitempty"`
	Slot           string          `json:"slot"`
	ContentType    string          `json:"content_type"`
	Seq            int             `json:"seq"`
	Value          json.RawMessage `json:"value"`
	Caption        *string         `json:"caption,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

func validArtifactFilename(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 255 ||
		strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func persistConversationArtifact(
	ctx context.Context, db *gorm.DB, conversationID, historyID, userID string,
	event *ArtifactCreatedEvent,
) (*ConversationArtifactDTO, error) {
	if event == nil {
		return nil, errors.New("artifact event is required")
	}
	artifactID := strings.TrimSpace(event.ArtifactID)
	if _, err := uuid.Parse(artifactID); err != nil {
		return nil, errors.New("invalid artifact id")
	}
	if len(artifactID) > 36 ||
		conversationID == "" || historyID == "" || userID == "" ||
		!validArtifactFilename(event.Filename) {
		return nil, errors.New("invalid artifact metadata")
	}
	contentType := strings.ToLower(strings.TrimSpace(event.ContentType))
	if contentType != "text" && contentType != "json" {
		return nil, errors.New("unsupported artifact content type")
	}
	if len(event.Value) == 0 || len(event.Value) > maxConversationArtifactBytes || !json.Valid(event.Value) {
		return nil, errors.New("invalid artifact value")
	}
	var value map[string]any
	if json.Unmarshal(event.Value, &value) != nil {
		return nil, errors.New("artifact value must be an object")
	}
	if contentType == "text" {
		if _, ok := value["text"].(string); !ok {
			return nil, errors.New("text artifact value must contain text")
		}
	} else if _, ok := value["data"]; !ok {
		return nil, errors.New("json artifact value must contain data")
	}
	if event.Caption != nil && utf8.RuneCountInString(*event.Caption) > 2000 {
		return nil, errors.New("artifact caption is too long")
	}
	now := time.Now().UTC()
	row := orm.ConversationArtifact{
		ID:             artifactID,
		ConversationID: conversationID,
		HistoryID:      historyID,
		Filename:       strings.TrimSpace(event.Filename),
		Slot:           strings.TrimSpace(event.Filename),
		ContentType:    contentType,
		Value:          event.Value,
		Caption:        event.Caption,
		CreateUserID:   userID,
		CreatedAt:      now,
	}
	result := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, errors.New("artifact id already exists")
	}
	return &ConversationArtifactDTO{
		ArtifactID: row.ID, ConversationID: row.ConversationID, HistoryID: row.HistoryID,
		ProducerType: "main_agent", Filename: row.Filename, Slot: row.Slot,
		ContentType: row.ContentType, Seq: 1, Value: row.Value, Caption: row.Caption,
		CreatedAt: row.CreatedAt,
	}, nil
}

// ListConversationArtifacts handles GET /conversations/{conversation_id}/artifacts.
func ListConversationArtifacts(w http.ResponseWriter, r *http.Request) {
	conversationID := common.PathVar(r, "conversation_id")
	if conversationID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var conversation orm.Conversation
	if err := db.WithContext(r.Context()).Where(
		"id = ? AND create_user_id = ?", conversationID, userID,
	).First(&conversation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		} else {
			common.ReplyErr(w, "query conversation failed", http.StatusInternalServerError)
		}
		return
	}

	out := make([]ConversationArtifactDTO, 0)
	var direct []orm.ConversationArtifact
	if err := db.WithContext(r.Context()).Where(
		"conversation_id = ? AND create_user_id = ?", conversationID, userID,
	).Find(&direct).Error; err != nil {
		common.ReplyErr(w, "query conversation artifacts failed", http.StatusInternalServerError)
		return
	}
	for _, artifact := range direct {
		out = append(out, ConversationArtifactDTO{
			ArtifactID: artifact.ID, ConversationID: conversationID, HistoryID: artifact.HistoryID,
			ProducerType: "main_agent", Filename: artifact.Filename, Slot: artifact.Slot,
			ContentType: artifact.ContentType, Seq: 1, Value: artifact.Value,
			Caption: artifact.Caption, CreatedAt: artifact.CreatedAt,
		})
	}

	subagentArtifacts, err := subagent.ListArtifactsByConversationForUser(
		r.Context(), db, conversationID, userID,
	)
	if err != nil {
		common.ReplyErr(w, "query subagent artifacts failed", http.StatusInternalServerError)
		return
	}
	for _, artifact := range subagentArtifacts {
		out = append(out, ConversationArtifactDTO{
			ArtifactID: artifact.ArtifactID, ConversationID: conversationID,
			HistoryID: artifact.TriggerHistoryID, ProducerType: "subagent", ProducerID: artifact.TaskID,
			Slot: artifact.Slot, ContentType: artifact.ContentType, Seq: artifact.Seq,
			Value: subagent.SignArtifactValue(
				artifact.ContentType, artifact.Value, artifact.WorkspacePath,
			),
			Caption: artifact.Caption, CreatedAt: artifact.CreatedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ArtifactID < out[j].ArtifactID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	common.ReplyOK(w, map[string]any{"artifacts": out})
}
