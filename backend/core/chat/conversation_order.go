package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type conversationOrderUpdate struct {
	ConversationID string `json:"conversation_id"`
	HistoryOrder   int64  `json:"history_order"`
}

type conversationOrderResult struct {
	ConversationID string                    `json:"conversation_id"`
	IsPinned       bool                      `json:"is_pinned"`
	PinnedAt       *time.Time                `json:"pinned_at"`
	HistoryOrder   *int64                    `json:"history_order"`
	OrderUpdates   []conversationOrderUpdate `json:"order_updates"`
}

var errConversationOrderConflict = errors.New("conversation order changed")

// Lock the owner's active roots in a stable order. Pinning and reordering share
// this checkpoint; child conversations keep their existing parent relationship.
func lockConversationHistory(tx *gorm.DB, userID string) ([]orm.Conversation, error) {
	var rows []orm.Conversation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "pinned_at", "history_order", "updated_at").
		Where("create_user_id = ? AND deleted_at IS NULL AND archived_at IS NULL AND is_ephemeral = ? AND (parent_conversation_id IS NULL OR parent_conversation_id = '')", userID, false).
		Order("id ASC").Find(&rows).Error
	sort.SliceStable(rows, func(i, j int) bool { return conversationHistoryLess(rows[i], rows[j]) })
	return rows, err
}

func conversationHistoryLess(left, right orm.Conversation) bool {
	if (left.PinnedAt != nil) != (right.PinnedAt != nil) {
		return left.PinnedAt != nil
	}
	// New conversations appear first; activity does not move an existing manual row.
	if (left.HistoryOrder == nil) != (right.HistoryOrder == nil) {
		return left.HistoryOrder == nil
	}
	if left.HistoryOrder != nil && *left.HistoryOrder != *right.HistoryOrder {
		return *left.HistoryOrder < *right.HistoryOrder
	}
	if left.PinnedAt != nil && !left.PinnedAt.Equal(*right.PinnedAt) {
		return left.PinnedAt.After(*right.PinnedAt)
	}
	if !left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.UpdatedAt.After(right.UpdatedAt)
	}
	return left.ID < right.ID
}

func saveConversationOrder(tx *gorm.DB, rows []orm.Conversation) ([]conversationOrderUpdate, error) {
	updates := make([]conversationOrderUpdate, 0, len(rows))
	for index := range rows {
		order := int64(index + 1)
		if rows[index].HistoryOrder == nil || *rows[index].HistoryOrder != order {
			if err := tx.Model(&orm.Conversation{}).Where("id = ?", rows[index].ID).UpdateColumn("history_order", order).Error; err != nil {
				return nil, err
			}
		}
		rows[index].HistoryOrder = &order
		updates = append(updates, conversationOrderUpdate{rows[index].ID, order})
	}
	return updates, nil
}

func replyConversationOrderError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
	case errors.Is(err, errConversationOrderConflict):
		common.ReplyAppErr(w, common.NewAppError(http.StatusConflict, common.ErrCodeConflict, "Conversation order changed; refresh and retry"))
	default:
		log.Ctx(r.Context()).Error().Err(err).Msg("Update conversation order failed")
		common.ReplyAppErr(w, common.NewAppError(http.StatusInternalServerError, common.ErrCodeInternal, "Unable to update conversation order"))
	}
}

// ReorderConversation moves one root relative to another without overwriting
// filtered-out or unloaded conversations, and without modifying activity dates.
func ReorderConversation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(mux.Vars(r)["conversation_id"])
	var body struct {
		TargetID string `json:"target_conversation_id"`
		Position string `json:"position"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&body)
	if err != nil || decoder.Decode(&struct{}{}) != io.EOF || id == "" || len(id) > 255 || body.TargetID == "" || len(body.TargetID) > 255 || body.TargetID == id || (body.Position != "before" && body.Position != "after") {
		common.ReplyAppErr(w, common.NewAppError(http.StatusBadRequest, common.ErrCodeInvalidParams, "Invalid conversation order"))
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var result conversationOrderResult
	err = conversationCheckpoint(r.Context(), store.DB(), "", func(tx *gorm.DB) error {
		rows, err := lockConversationHistory(tx, userID)
		if err != nil {
			return err
		}
		var moved, target *orm.Conversation
		for i := range rows {
			if rows[i].ID == id {
				moved = &rows[i]
			}
			if rows[i].ID == body.TargetID {
				target = &rows[i]
			}
		}
		if moved == nil || target == nil {
			return gorm.ErrRecordNotFound
		}
		pinned := moved.PinnedAt != nil
		if pinned != (target.PinnedAt != nil) {
			return errConversationOrderConflict
		}
		ordered := make([]orm.Conversation, 0, len(rows))
		for _, row := range rows {
			if (row.PinnedAt != nil) != pinned || row.ID == id {
				continue
			}
			if row.ID == body.TargetID && body.Position == "before" {
				ordered = append(ordered, *moved)
			}
			ordered = append(ordered, row)
			if row.ID == body.TargetID && body.Position == "after" {
				ordered = append(ordered, *moved)
			}
		}
		updates, err := saveConversationOrder(tx, ordered)
		if err != nil {
			return err
		}
		for _, row := range ordered {
			if row.ID == id {
				result = conversationOrderResult{id, pinned, row.PinnedAt, row.HistoryOrder, updates}
				break
			}
		}
		return nil
	})
	if err != nil {
		replyConversationOrderError(w, r, err)
		return
	}
	writeConversationJSON(w, http.StatusOK, result)
}

func updateConversationPin(ctx context.Context, db *gorm.DB, userID, id string, pinned bool) (conversationOrderResult, error) {
	result := conversationOrderResult{OrderUpdates: []conversationOrderUpdate{}}
	err := conversationCheckpoint(ctx, db, "", func(tx *gorm.DB) error {
		rows, err := lockConversationHistory(tx, userID)
		if err != nil {
			return err
		}
		var moved *orm.Conversation
		for i := range rows {
			if rows[i].ID == id {
				moved = &rows[i]
				break
			}
		}
		if moved == nil {
			return gorm.ErrRecordNotFound
		}
		result.ConversationID = id
		if pinned == (moved.PinnedAt != nil) {
			result.IsPinned = pinned
			result.PinnedAt = moved.PinnedAt
			result.HistoryOrder = moved.HistoryOrder
			return nil
		}
		moved.PinnedAt = nil
		moved.HistoryOrder = nil
		if pinned {
			now := time.Now().UTC()
			moved.PinnedAt = &now
		}
		if err := tx.Model(&orm.Conversation{}).Where("id = ?", id).UpdateColumns(map[string]any{"pinned_at": moved.PinnedAt, "history_order": nil}).Error; err != nil {
			return err
		}
		ordered := make([]orm.Conversation, 0, len(rows))
		manual := false
		for _, row := range rows {
			if row.ID != id && (row.PinnedAt != nil) == pinned {
				ordered = append(ordered, row)
				manual = manual || row.HistoryOrder != nil
			}
		}
		if manual {
			at := 0
			if !pinned {
				// Use the conversation's chronological position, while preserving
				// the relative order of every other manually arranged row.
				for _, row := range ordered {
					if row.UpdatedAt.After(moved.UpdatedAt) {
						at++
					}
				}
			}
			ordered = append(ordered, orm.Conversation{})
			copy(ordered[at+1:], ordered[at:])
			ordered[at] = *moved
			result.OrderUpdates, err = saveConversationOrder(tx, ordered)
			if err != nil {
				return err
			}
			moved = &ordered[at]
		}
		result.IsPinned = pinned
		result.PinnedAt = moved.PinnedAt
		result.HistoryOrder = moved.HistoryOrder
		return nil
	})
	return result, err
}
