package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf16"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// Retain tool/thinking history while replacing only the visible assistant body.
func replaceChatExportResult(result, body string) string {
	boundary := 0
	for _, tag := range []string{"</tp>", "</trp>"} {
		if index := strings.LastIndex(result, tag); index >= 0 && index+len(tag) > boundary {
			boundary = index + len(tag)
		}
	}
	prefix := result[:boundary]
	if think := extractThinkContent(result[boundary:]); think != "" {
		prefix += "<think>" + think + "</think>"
	}
	return prefix + body
}

// ChatExport offsets address the finalized assistant body in UTF-16 code units.
type ChatExport struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	ExportID    string `json:"export_id"`
}

type ChatExportSnapshot struct {
	Content string       `json:"content"`
	Exports []ChatExport `json:"exports"`
}

func chatExportsFromExt(ext json.RawMessage) []ChatExport {
	var data struct {
		Exports []ChatExport `json:"exports"`
	}
	_ = json.Unmarshal(ext, &data)
	return data.Exports
}

func withChatExports(ext json.RawMessage, exports []ChatExport) json.RawMessage {
	data := map[string]json.RawMessage{}
	_ = json.Unmarshal(ext, &data)
	if data == nil {
		data = map[string]json.RawMessage{}
	}
	delete(data, "exports")
	if len(exports) > 0 {
		data["exports"], _ = json.Marshal(exports)
	}
	raw, _ := json.Marshal(data)
	return raw
}

func validChatExportFilename(name string) bool {
	return validArtifactFilename(name) && strings.HasSuffix(name, ".md")
}

func finalizeChatExports(snapshot *ChatExportSnapshot, conversationID, historyID, runID string) []ChatExport {
	exports := make([]ChatExport, 0, len(snapshot.Exports))
	size := len(utf16.Encode([]rune(snapshot.Content)))
	end := 0
	for _, item := range snapshot.Exports {
		if item.Start < end || item.End <= item.Start || item.End > size ||
			item.ContentType != "text/markdown" || !validChatExportFilename(item.Filename) || item.Title == "" {
			continue
		}
		item.Index = len(exports)
		item.ExportID = uuid.NewSHA1(uuid.NameSpaceOID,
			[]byte(fmt.Sprintf("chat-export:%s:%s:%s:%d", conversationID, historyID, runID, item.Index))).String()
		exports = append(exports, item)
		end = item.End
	}
	return exports
}

type CreateChatExportRequest struct {
	HistoryID   string `json:"history_id"`
	ExportID    string `json:"export_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
}

var errChatExportUnavailable = errors.New("artifact unavailable")
var errChatExportInvalid = errors.New("invalid chat export")

// The history lock serializes saves with regeneration. The artifact primary key
// provides idempotency across tabs/processes without overwriting the first save.
func saveChatExport(ctx context.Context, db *gorm.DB, userID, conversationID string, req CreateChatExportRequest) (*ConversationArtifactDTO, bool, error) {
	var dto *ConversationArtifactDTO
	created := false
	err := common.ImmediateTransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		dto, created = nil, false
		var conv orm.Conversation
		if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).First(&conv).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errChatExportUnavailable
			}
			return err
		}
		var history orm.ChatHistory
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND conversation_id = ?", req.HistoryID, conversationID).First(&history).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var candidate orm.MultiAnswersChatHistory
			err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND conversation_id = ?", req.HistoryID, conversationID).First(&candidate).Error
			history = orm.ChatHistory{ID: candidate.ID, Ext: candidate.Ext, RunStatus: candidate.RunStatus}
		}
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errChatExportUnavailable
			}
			return err
		}
		var flags struct {
			ReadOnly bool `json:"fork_read_only"`
		}
		_ = json.Unmarshal(history.Ext, &flags)
		if history.RunStatus != "completed" || flags.ReadOnly {
			return errChatExportUnavailable
		}
		var selected *ChatExport
		for _, item := range chatExportsFromExt(history.Ext) {
			if item.ExportID == req.ExportID {
				copy := item
				selected = &copy
				break
			}
		}
		if selected == nil {
			return errChatExportUnavailable
		}
		if selected.Filename != req.Filename || selected.ContentType != req.ContentType || req.ContentType != "text/markdown" || !validChatExportFilename(req.Filename) {
			return errChatExportInvalid
		}
		value, _ := json.Marshal(map[string]any{"text": req.Content, "chat_export": true})
		if req.Content == "" || len(value) > maxConversationArtifactBytes {
			return errChatExportInvalid
		}
		var existing orm.ConversationArtifact
		loadExisting := func() error {
			err := tx.Where("id = ? AND conversation_id = ? AND history_id = ? AND create_user_id = ?", req.ExportID, conversationID, req.HistoryID, userID).First(&existing).Error
			if err == nil {
				dto = &ConversationArtifactDTO{ArtifactID: existing.ID, ConversationID: conversationID, HistoryID: req.HistoryID,
					ProducerType: "main_agent", Filename: existing.Filename, Slot: existing.Slot, ContentType: existing.ContentType,
					Seq: 1, Value: existing.Value, Caption: existing.Caption, CreatedAt: existing.CreatedAt}
			}
			return err
		}
		if err := loadExisting(); err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		dto, err = persistConversationArtifact(ctx, tx, conversationID, req.HistoryID, userID, &ArtifactCreatedEvent{
			ArtifactID: req.ExportID, Filename: req.Filename, ContentType: "text", Value: value, Caption: &selected.Title,
		})
		if err != nil {
			if loadErr := loadExisting(); loadErr == nil {
				return nil
			}
			return err
		}
		created = true
		return nil
	})
	return dto, created, err
}

// CreateConversationArtifact saves a finalized Main Chat export, without model calls.
func CreateConversationArtifact(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxConversationArtifactBytes+64*1024)
	var req CreateChatExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid artifact request", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	conversationID := common.PathVar(r, "conversation_id")
	dto, created, err := saveChatExport(r.Context(), store.DB(), userID, conversationID, req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errChatExportUnavailable) {
			status = http.StatusNotFound
		}
		if errors.Is(err, errChatExportInvalid) {
			status = http.StatusBadRequest
		}
		common.ReplyErr(w, http.StatusText(status), status)
		return
	}
	if created && store.State() != nil {
		_ = AppendConvEvent(r.Context(), store.State(), conversationID, &ConvEvent{Type: "artifact_created", Payload: dto})
	}
	common.ReplyOK(w, dto)
}
