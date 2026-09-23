package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// InitializeConversationResultReads runs before accepting requests or starting
// background jobs. The receipts and completion marker share one snapshot/commit.
func InitializeConversationResultReads(ctx context.Context, db *gorm.DB) error {
	var state orm.ConversationResultReadState
	if err := db.WithContext(ctx).Where("id = ?", 1).Find(&state).Error; err != nil {
		return err
	}
	if state.Initialized {
		return nil
	}
	initialize := func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&orm.ConversationResultReadState{ID: 1}).Error; err != nil {
			return err
		}
		var state orm.ConversationResultReadState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&state, "id = ?", 1).Error; err != nil {
			return err
		}
		if state.Initialized {
			return nil
		}
		lastID := ""
		for {
			var conversations []orm.Conversation
			if err := tx.Unscoped().Select("id, create_user_id").Where("is_ephemeral = ? AND id > ?", false, lastID).Order("id").Limit(conversationStatusBatchLimit).Find(&conversations).Error; err != nil {
				return err
			}
			if len(conversations) == 0 {
				break
			}
			ids := make([]string, 0, len(conversations))
			for _, conversation := range conversations {
				ids = append(ids, conversation.ID)
			}
			results, err := conversationTerminalStatuses(ctx, tx, ids)
			if err != nil {
				return err
			}
			reads := make([]orm.ConversationResultRead, 0, len(conversations))
			for _, conversation := range conversations {
				if version := results[conversation.ID].Version; version != "" {
					reads = append(reads, orm.ConversationResultRead{UserID: conversation.CreateUserID, ConversationID: conversation.ID, TerminalVersion: version})
				}
			}
			if len(reads) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reads).Error; err != nil {
					return err
				}
			}
			lastID = conversations[len(conversations)-1].ID
		}
		return tx.Model(&orm.ConversationResultReadState{}).Where("id = ?", 1).Update("initialized", true).Error
	}
	if db.Dialector.Name() == "sqlite" {
		return common.ImmediateTransactionWithSQLiteBusyRetry(ctx, db, initialize)
	}
	// Competing first-start transactions may acquire snapshots before the winner
	// commits. PostgreSQL requires retrying the entire snapshot in that case.
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = db.WithContext(ctx).Transaction(initialize, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		var sqlError interface{ SQLState() string }
		if !errors.As(err, &sqlError) || sqlError.SQLState() != "40001" {
			return err
		}
	}
	return err
}

// ReadConversationResult acknowledges the result version supplied by its owner.
func ReadConversationResult(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TerminalVersion string `json:"terminal_version"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		replyResultReadError(w, http.StatusBadRequest, "Invalid result confirmation")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) || body.TerminalVersion == "" || len(body.TerminalVersion) > 64 || strings.TrimSpace(body.TerminalVersion) != body.TerminalVersion {
		replyResultReadError(w, http.StatusBadRequest, "Invalid result confirmation")
		return
	}
	id, owner := common.PathVar(r, "conversation_id"), recoveryUserID(r)
	stale := errors.New("result version changed")
	err := common.ImmediateTransactionWithSQLiteBusyRetry(r.Context(), store.DB(), func(tx *gorm.DB) error {
		var conversation orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ? AND create_user_id = ? AND archived_at IS NULL AND deleted_at IS NULL AND is_ephemeral = ?", id, owner, false).Take(&conversation).Error; err != nil {
			return err
		}
		results, err := conversationTerminalStatuses(r.Context(), tx, []string{id})
		if err != nil {
			return err
		}
		if results[id].Version != body.TerminalVersion {
			return stale
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&orm.ConversationResultRead{UserID: owner, ConversationID: id, TerminalVersion: body.TerminalVersion}).Error
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		replyResultReadError(w, http.StatusNotFound, "Conversation not found")
	case errors.Is(err, stale):
		replyResultReadError(w, http.StatusConflict, "Result version changed")
	case err != nil:
		replyResultReadError(w, http.StatusInternalServerError, "Unable to confirm result")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func replyResultReadError(w http.ResponseWriter, status int, message string) {
	common.ReplyAppErr(w, &common.AppError{Code: common.ErrorCodeFromHTTPStatus(status), HTTPStatus: status, Message: message})
}
