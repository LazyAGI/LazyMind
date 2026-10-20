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
	"lazymind/core/conversationgroup"
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
		Select("id", "pinned_at", "history_order", "unpinned_history_order", "updated_at", "is_task_conv").
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

func saveConversationOrder(tx *gorm.DB, rows []orm.Conversation, includePinnedPlaceholders bool) ([]conversationOrderUpdate, error) {
	updates := make([]conversationOrderUpdate, 0, len(rows))
	for index := range rows {
		order := int64(index + 1)
		if includePinnedPlaceholders && rows[index].PinnedAt != nil {
			if err := tx.Model(&orm.Conversation{}).Where("id = ?", rows[index].ID).UpdateColumn("unpinned_history_order", order).Error; err != nil {
				return nil, err
			}
			rows[index].UnpinnedHistoryOrder = &order
			continue
		}
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
	case errors.Is(err, conversationgroup.ErrConversationOrganizing):
		common.ReplyAppErr(w, common.NewAppError(http.StatusConflict, common.ErrCodeConflict, "Conversation is being organized; retry later"))
	default:
		log.Ctx(r.Context()).Error().Err(err).Msg("Update conversation order failed")
		common.ReplyAppErr(w, common.NewAppError(http.StatusInternalServerError, common.ErrCodeInternal, "Unable to update conversation order"))
	}
}

// The server derives the list from durable relationships, never from client scope.
// Pinned roots share the pinned list; other roots belong to their group or recent
// list. Children can only move among siblings. Membership is never written here.
func lockConversationOrderScope(ctx context.Context, tx *gorm.DB, userID, id, targetID string) ([]orm.Conversation, error) {
	active := tx.Model(&orm.Conversation{}).Where("create_user_id=? AND deleted_at IS NULL AND archived_at IS NULL AND is_ephemeral=?", userID, false)
	var anchors []orm.Conversation
	if err := active.Session(&gorm.Session{}).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", []string{id, targetID}).Order("id").Find(&anchors).Error; err != nil {
		return nil, err
	}
	if len(anchors) != 2 {
		return nil, gorm.ErrRecordNotFound
	}
	var origins []orm.ConversationForkOrigin
	if err := tx.Where("conversation_id IN ?", []string{id, targetID}).Find(&origins).Error; err != nil {
		return nil, err
	}
	forkParents := make(map[string]string, len(origins))
	for _, origin := range origins {
		forkParents[origin.ConversationID] = origin.SourceConversationID
	}
	left, right := anchors[0], anchors[1]
	parent := func(row orm.Conversation) string {
		// Fork provenance takes precedence, matching the sidebar relation resolver.
		if source, ok := forkParents[row.ID]; ok {
			return source
		}
		if row.ParentConversationID != nil {
			return *row.ParentConversationID
		}
		return ""
	}
	if (parent(left) == "") != (parent(right) == "") {
		return nil, gorm.ErrRecordNotFound
	}
	if left.IsTaskConv != right.IsTaskConv || parent(left) != parent(right) || (left.PinnedAt != nil) != (right.PinnedAt != nil) {
		return nil, errConversationOrderConflict
	}
	query := active.Session(&gorm.Session{}).Where("is_task_conv=?", left.IsTaskConv)
	forks := tx.Model(&orm.ConversationForkOrigin{}).Select("conversation_id")
	parentID := parent(left)
	if parentID != "" {
		var ownerParent orm.Conversation
		if err := active.Session(&gorm.Session{}).Where("id=? AND is_task_conv=?", parentID, left.IsTaskConv).Take(&ownerParent).Error; err != nil {
			return nil, err
		}
		if left.PinnedAt != nil {
			return nil, errConversationOrderConflict
		}
		siblingForks := tx.Model(&orm.ConversationForkOrigin{}).Select("conversation_id").Where("source_conversation_id=?", parentID)
		query = query.Where("id IN (?) OR (parent_conversation_id=? AND id NOT IN (?))", siblingForks, parentID, forks).
			Where("pinned_at IS NULL")
	} else {
		query = query.Where("(parent_conversation_id IS NULL OR parent_conversation_id='') AND id NOT IN (?)", forks)
		if left.PinnedAt != nil {
			query = query.Where("pinned_at IS NOT NULL")
		} else {
			var memberships []orm.ConversationGroupMember
			if err := tx.Where("user_id=? AND conversation_id IN ?", userID, []string{id, targetID}).Find(&memberships).Error; err != nil {
				return nil, err
			}
			groups := map[string]string{}
			for _, member := range memberships {
				groups[member.ConversationID] = member.GroupID
			}
			if groups[id] != groups[targetID] {
				return nil, errConversationOrderConflict
			}
			members := tx.Model(&orm.ConversationGroupMember{}).Select("conversation_id").Where("user_id=?", userID)
			if groupID := groups[id]; groupID != "" {
				var group orm.ConversationGroup
				if err := tx.Where("id=? AND user_id=? AND deleted_at IS NULL AND is_task_conv=?", groupID, userID, left.IsTaskConv).Take(&group).Error; err != nil {
					return nil, err
				}
				query = query.Where("id IN (?)", members.Where("group_id=?", groupID))
			} else {
				query = query.Where("id NOT IN (?)", members)
			}
			query = query.Where("pinned_at IS NULL OR unpinned_history_order IS NOT NULL")
		}
	}
	var rows []orm.Conversation
	if err := query.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if parentID != "" {
		ids = append(ids, parentID)
	}
	if err := conversationgroup.RequireOrganizerUnlocked(ctx, tx, userID, ids, ""); err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return conversationHistoryLess(rows[i], rows[j]) })
	return rows, nil
}

// ReorderConversation moves relative to an anchor within the same list, keeping
// filtered-out and unloaded conversations and leaving activity dates unchanged.
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
	err = conversationgroup.UserTransaction(r.Context(), store.DB(), userID, func(tx *gorm.DB) error {
		rows, err := lockConversationOrderScope(r.Context(), tx, userID, id, body.TargetID)
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
		candidates := make([]orm.Conversation, 0, len(rows))
		for _, row := range rows {
			if (pinned && row.PinnedAt == nil) || (!pinned && row.PinnedAt != nil && row.UnpinnedHistoryOrder == nil) {
				continue
			}
			candidates = append(candidates, row)
		}
		if !pinned {
			// Keep temporarily pinned rows as invisible placeholders in their
			// original history positions when the user rearranges that history.
			sort.SliceStable(candidates, func(i, j int) bool {
				left, right := candidates[i], candidates[j]
				if left.PinnedAt != nil {
					left.HistoryOrder = left.UnpinnedHistoryOrder
				}
				if right.PinnedAt != nil {
					right.HistoryOrder = right.UnpinnedHistoryOrder
				}
				left.PinnedAt, right.PinnedAt = nil, nil
				return conversationHistoryLess(left, right)
			})
		}
		ordered := make([]orm.Conversation, 0, len(candidates))
		for _, row := range candidates {
			if row.ID == id {
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
		updates, err := saveConversationOrder(tx, ordered, !pinned)
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
	err := conversationgroup.UserTransaction(ctx, db, userID, func(tx *gorm.DB) error {
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
		if pinned {
			if err := conversationgroup.DetachPinnedConversation(ctx, tx, userID, id); err != nil {
				return err
			}
		}
		result.ConversationID = id
		if pinned == (moved.PinnedAt != nil) {
			result.IsPinned = pinned
			result.PinnedAt = moved.PinnedAt
			result.HistoryOrder = moved.HistoryOrder
			return nil
		}
		moved.PinnedAt = nil
		if pinned {
			moved.UnpinnedHistoryOrder = moved.HistoryOrder
			moved.HistoryOrder = nil
			now := time.Now().UTC()
			moved.PinnedAt = &now
		} else {
			moved.HistoryOrder = moved.UnpinnedHistoryOrder
			moved.UnpinnedHistoryOrder = nil
		}
		if err := tx.Model(&orm.Conversation{}).Where("id = ?", id).UpdateColumns(map[string]any{
			"pinned_at": moved.PinnedAt, "history_order": moved.HistoryOrder,
			"unpinned_history_order": moved.UnpinnedHistoryOrder,
		}).Error; err != nil {
			return err
		}
		if !pinned {
			result.IsPinned = false
			result.HistoryOrder = moved.HistoryOrder
			return nil
		}
		ordered := make([]orm.Conversation, 0, len(rows))
		manual := false
		for _, row := range rows {
			if row.ID != id && row.IsTaskConv == moved.IsTaskConv && (row.PinnedAt != nil) == pinned {
				ordered = append(ordered, row)
				manual = manual || row.HistoryOrder != nil
			}
		}
		if manual {
			at := 0
			ordered = append(ordered, orm.Conversation{})
			copy(ordered[at+1:], ordered[at:])
			ordered[at] = *moved
			result.OrderUpdates, err = saveConversationOrder(tx, ordered, false)
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
