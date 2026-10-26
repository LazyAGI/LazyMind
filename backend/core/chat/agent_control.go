package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
)

func RegisterTaskCenterEnvCleanup() {
	taskcenter.OnRemovalCancelHook = func(ctx context.Context, owner, conversationID string, before time.Time, executionIDs []string) error {
		db := store.DB()
		if db == nil {
			return fmt.Errorf("cancellation store unavailable")
		}
		var conversation orm.Conversation
		if err := db.WithContext(ctx).Where("id=? AND create_user_id=?", conversationID, owner).Take(&conversation).Error; err != nil {
			return err
		}
		if conversation.DeletedAt == nil {
			return nil
		}
		if !isExternalChatProvider(conversation.ChatExecutor) {
			if err := workflow.CancelRemovedTaskExecutions(ctx, db, store.State(), owner, conversationID, before, executionIDs); err != nil {
				return err
			}
		}
		var runs []orm.ExternalChatRun
		if err := db.WithContext(ctx).Where("actor_user_id=? AND conversation_id=? AND created_at<=? AND status IN ?", owner, conversationID, before, []string{"pending", "running"}).Find(&runs).Error; err != nil {
			return err
		}
		for _, run := range runs {
			if err := newExternalChatApplication(db).requestStop(ctx, owner, conversationID, run.HistoryID); err != nil {
				return err
			}
		}
		for _, executionID := range executionIDs {
			if err := taskcenter.FinishChatExecution(ctx, db, owner, conversationID, executionID); err != nil {
				return err
			}
		}
		return nil
	}
	taskcenter.OnConversationTrashHook = func(conversationID string) {
		notifySessionEnvClear(conversationID)
	}
}

func notifyToolLimitDecision(convID, decisionID, action string) error {
	body, _ := json.Marshal(map[string]string{
		"conversation_id": convID,
		"decision_id":     decisionID,
		"action":          action,
	})
	url := common.JoinURL(common.ChatServiceEndpoint(), "/api/agent/tool-limit-decision")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("chat service returned status %d", resp.StatusCode)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("tool-limit decision is no longer active")
	}
	return nil
}

func notifySessionEnvClear(conversationIDs ...string) {
	ids := make([]string, 0, len(conversationIDs))
	for _, id := range conversationIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	go func() {
		body, _ := json.Marshal(map[string]any{"conversation_ids": ids})
		url := common.JoinURL(common.ChatServiceEndpoint(), "/api/chat/session-env:clear")
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			fmt.Printf("[chat] notifySessionEnvClear: %v\n", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("[chat] notifySessionEnvClear: %v\n", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			fmt.Printf("[chat] notifySessionEnvClear: chat service returned status %d\n", resp.StatusCode)
		}
	}()
}
