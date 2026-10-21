package taskcenter

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
)

func chatExecutionsKey(convID string) string { return "rag/chat/executions:" + convID }

// RegisterChatExecution records the exact Python execution before it can start.
// The conversation lock serializes registration with moving it to Trash.
func RegisterChatExecution(ctx context.Context, db *gorm.DB, owner, convID, executionID string) error {
	return updateChatExecution(ctx, db, owner, convID, executionID, true)
}

// FinishChatExecution removes only this execution, preserving newer executions.
func FinishChatExecution(ctx context.Context, db *gorm.DB, owner, convID, executionID string) error {
	return updateChatExecution(ctx, db, owner, convID, executionID, false)
}

func updateChatExecution(ctx context.Context, db *gorm.DB, owner, convID, executionID string, add bool) error {
	return conversationgroup.UserTransaction(ctx, db, owner, func(tx *gorm.DB) error {
		var conv orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND create_user_id=?", convID, owner).Take(&conv).Error; err != nil {
			return err
		}
		if add && conv.DeletedAt != nil {
			return gorm.ErrRecordNotFound
		}
		shared := store.State()
		if shared == nil {
			return errors.New("chat execution store unavailable")
		}
		if add {
			// No expiry: a long-running execution must remain cancellable.
			return shared.HSet(ctx, chatExecutionsKey(convID), map[string]any{executionID: "active"}, 0)
		}
		return shared.HDel(ctx, chatExecutionsKey(convID), executionID)
	})
}

func removalChatExecutionIDs(ctx context.Context, convID string, before time.Time) ([]string, error) {
	ids := []string{}
	shared := store.State()
	if shared == nil {
		return ids, nil // Registration requires shared state, so no tracked execution can start here.
	}
	active, err := shared.HGetAll(ctx, chatExecutionsKey(convID))
	if err != nil {
		return nil, err
	}
	for id := range active {
		prefix := convID + "_"
		started, err := strconv.ParseInt(strings.TrimPrefix(id, prefix), 10, 64)
		if strings.HasPrefix(id, prefix) && err == nil && started <= before.UnixMilli() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
