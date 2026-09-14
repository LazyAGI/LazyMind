package chat

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/taskcenter"
)

type conversationTerminalResult struct {
	Status  string
	Version string
}

// Read only metadata from the latest reply, including replies without a terminal
// status. An older failure or success must not leak into a newer execution.
func conversationTerminalStatuses(ctx context.Context, db *gorm.DB, ids []string) (map[string]conversationTerminalResult, error) {
	type history struct {
		ID, ConversationID, RunID, RunStatus string
		Seq                                  int
		CreateTime                           time.Time
	}
	latest := map[string]history{}
	for _, table := range []string{"chat_histories", "multi_answers_chat_histories"} {
		var rows []history
		if err := db.WithContext(ctx).Table(table+" AS history").
			Select("history.id, history.conversation_id, history.seq, history.create_time, history.run_id, history.run_status").
			Where("history.conversation_id IN ?", ids).
			Where(`NOT EXISTS (SELECT 1 FROM ` + table + ` newer WHERE newer.conversation_id = history.conversation_id
				AND (newer.seq > history.seq OR (newer.seq = history.seq AND newer.create_time > history.create_time)
				OR (newer.seq = history.seq AND newer.create_time = history.create_time AND newer.id > history.id)))`).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			previous, exists := latest[row.ConversationID]
			if !exists || row.Seq > previous.Seq || (row.Seq == previous.Seq && (row.CreateTime.After(previous.CreateTime) || (row.CreateTime.Equal(previous.CreateTime) && row.ID > previous.ID))) {
				latest[row.ConversationID] = row
			}
		}
	}
	result := map[string]conversationTerminalResult{}
	versions := map[string][]string{}
	historyIDs := make([]string, 0, len(latest))
	for id, row := range latest {
		result[id] = conversationTerminalResult{Status: conversationTerminalStatus(row.RunStatus)}
		versions[id] = []string{"reply:" + row.ID + ":" + row.RunID + ":" + row.RunStatus}
		historyIDs = append(historyIDs, row.ID)
	}
	if len(historyIDs) == 0 {
		return result, nil
	}
	// A workflow belongs to its initiating reply, not whichever reply happened
	// to finish before the workflow's last update. Select the latest session for
	// that reply so a replaced session cannot override its successor.
	var sessions []struct {
		ID, ConversationID, TriggerHistoryID, Status string
		UpdatedAt                                    time.Time
	}
	if err := db.WithContext(ctx).Table("plugin_sessions AS session").
		Select("session.id, session.conversation_id, session.trigger_history_id, session.status, session.updated_at").
		Where("session.conversation_id IN ? AND session.trigger_history_id IN ?", ids, historyIDs).
		Where(`NOT EXISTS (SELECT 1 FROM plugin_sessions newer WHERE newer.conversation_id = session.conversation_id
			AND newer.trigger_history_id = session.trigger_history_id
			AND (newer.created_at > session.created_at OR (newer.created_at = session.created_at AND newer.id > session.id)))`).
		Find(&sessions).Error; err != nil {
		return nil, err
	}
	for _, session := range sessions {
		if latest[session.ConversationID].ID != session.TriggerHistoryID {
			continue
		}
		terminal := conversationTerminalStatus(session.Status)
		if session.Status == "waiting" && taskcenter.WorkflowWasStopped(ctx, db, session.ID) {
			terminal = "canceled"
		}
		result[session.ConversationID] = conversationTerminalResult{Status: terminal}
		versions[session.ConversationID] = append(versions[session.ConversationID], "workflow:"+session.ID+":"+session.Status+":"+session.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	var tasks []struct {
		ID, ConversationID, TriggerHistoryID, Status string
		UpdatedAt                                    time.Time
	}
	if err := db.WithContext(ctx).Table("sub_agent_tasks").Select("id, conversation_id, trigger_history_id, status, updated_at").
		Where("conversation_id IN ? AND trigger_history_id IN ? AND agent_type <> ?", ids, historyIDs, "workflow_step").
		Where("NOT EXISTS (SELECT 1 FROM plugin_session_steps step WHERE step.task_id = sub_agent_tasks.id)").Find(&tasks).Error; err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if latest[task.ConversationID].ID != task.TriggerHistoryID {
			continue
		}
		// Live tasks are handled by the activity snapshot. A failed or stopped
		// child still matters after its parent has already returned a reply.
		versions[task.ConversationID] = append(versions[task.ConversationID], "task:"+task.ID+":"+task.Status+":"+task.UpdatedAt.UTC().Format(time.RFC3339Nano))
		terminal := conversationTerminalStatus(task.Status)
		if terminal == "failed" || (terminal == "canceled" && result[task.ConversationID].Status != "failed") {
			result[task.ConversationID] = conversationTerminalResult{Status: terminal}
		}
	}
	// An opaque, stable version lets clients acknowledge one result without
	// suppressing a later run that finishes with the same status.
	for id, terminal := range result {
		if terminal.Status == "" {
			continue
		}
		sort.Strings(versions[id])
		terminal.Version = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(versions[id], "\n")+"\n"+terminal.Status)))
		result[id] = terminal
	}
	return result, nil
}

func conversationTerminalStatus(status string) string {
	switch status {
	case "completed", "succeeded":
		return "completed"
	case "failed":
		return "failed"
	case "canceled", "cancelled", "stopped", "interrupted":
		return "canceled"
	default:
		return ""
	}
}
