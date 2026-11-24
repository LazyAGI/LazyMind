package workflow

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/subagent"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
)

// NativeChatRecovery fences automatic takeover against a newer dispatch or stop.
type NativeChatRecovery struct {
	SessionID            string `json:"session_id"`
	AttemptID            string `json:"attempt_id"`
	ExpectedStateVersion int64  `json:"expected_state_version"`
}

func inlineChatNeedsRecovery(ctx context.Context, db *gorm.DB, session orm.WorkflowSession) (bool, error) {
	if session.ControllerHost != "lazymind" || session.Dismissed || session.Status != SessionStatusActive {
		return false, nil
	}
	view, err := displaystate.Read(db.WithContext(ctx), session)
	if err != nil {
		return false, err
	}
	if view.Status != "failed" || view.Reason != "controller_failed" {
		return false, nil
	}
	var active int64
	if err := db.WithContext(ctx).Model(&orm.ChatHistory{}).Where("conversation_id = ? AND run_status = ?", session.ConversationID, "generating").Count(&active).Error; err != nil {
		return false, err
	}
	return active == 0, nil
}

// Both terminal events call this admission point. The completed attempt can
// replace its failed Chat only once, regardless of which event arrived first.
func startNativeChatRecovery(ctx context.Context, db *gorm.DB, cache state.Store, sessionID string, onSSE func(string, map[string]any)) (bool, error) {
	var recovery NativeChatRecovery
	var pctx *WorkflowChatContext
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		recovery, pctx = NativeChatRecovery{}, nil
		var session orm.WorkflowSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id = ?", sessionID).Error; err != nil {
			return err
		}
		eligible, err := inlineChatNeedsRecovery(ctx, tx, session)
		if err != nil || !eligible {
			return err
		}
		var step orm.WorkflowSessionStep
		err = tx.Where("session_id = ? AND validity = ?", sessionID, "effective").Order("created_at DESC, attempt DESC, id DESC").First(&step).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if step.Status != "succeeded" {
			return nil
		}
		var binding nativeTurnBinding
		if session.ControlBindingJSON != "" {
			if err := json.Unmarshal([]byte(session.ControlBindingJSON), &binding); err != nil {
				return err
			}
		}
		if binding.RecoveredAttemptID == step.ID {
			return nil
		}
		var task orm.SubAgentTask
		if err := tx.First(&task, "id = ? AND conversation_id = ?", step.TaskID, session.ConversationID).Error; err != nil {
			return err
		}
		var params WorkflowStepParams
		if err := json.Unmarshal(task.Params, &params); err != nil {
			return err
		}
		if task.AgentType != "workflow_step" || params.HandOff == nil || *params.HandOff || params.HostedTaskID != "" {
			return nil
		}
		pctx = &WorkflowChatContext{SessionID: sessionID, ConvID: session.ConversationID, WorkflowID: session.WorkflowID, StepID: step.StepID, WorkflowMode: session.WorkflowMode, UserID: session.CreateUserID}
		binding.RecoveredAttemptID = step.ID
		encoded, err := json.Marshal(binding)
		if err != nil {
			return err
		}
		session.StateVersion++
		if err := tx.Model(&session).Updates(map[string]any{"control_binding_json": string(encoded), "state_version": session.StateVersion}).Error; err != nil {
			return err
		}
		recovery = NativeChatRecovery{SessionID: sessionID, AttemptID: step.ID, ExpectedStateVersion: session.StateVersion}
		return nil
	})
	if err != nil || pctx == nil {
		return false, err
	}
	go func() {
		finish := beginDriverActivity(cache, pctx.ConvID, sessionID)
		defer finish()
		triggerNextChatTurn(pctx.ConvID, sessionID, pctx.WorkflowID, pctx.StepID, pctx.WorkflowMode, pctx.UserID,
			"上一次回复意外中断，已恢复的步骤已成功完成。请保留已有结果，从当前工作流的下一可执行步骤继续，并遵守现有审批和用户输入要求。", func() {
				finish()
				onSSE("auto_chat_started", map[string]any{"session_id": sessionID, "conversation_id": pctx.ConvID})
			}, "auto_continue", recovery)
		// A rejected delivery still owns only its reservation version. Once
		// adopted, the Chat handler owns cleanup; never fail a newer controller.
		if err := FinishNativeControlTurn(context.Background(), db, sessionID, recovery.ExpectedStateVersion); err != nil {
			onSSE("workflow_error", map[string]any{"session_id": sessionID, "message": err.Error()})
		}
	}()
	return true, nil
}

// AdmitNativeChatRecovery runs after Chat admission. Binding the actual new
// history makes live projections and pause ownership follow the replacement.
func AdmitNativeChatRecovery(ctx context.Context, db *gorm.DB, owner, convID, historyID string, recovery NativeChatRecovery) (int64, error) {
	var version int64
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		var session orm.WorkflowSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND conversation_id = ? AND create_user_id = ?", recovery.SessionID, convID, owner).First(&session).Error; err != nil {
			return err
		}
		if session.Dismissed || session.ControllerHost != "lazymind" || controlstore.Controlled(session) || session.Status != SessionStatusActive || session.StateVersion != recovery.ExpectedStateVersion {
			return controlstore.Reject("STATE_VERSION_CONFLICT", "workflow changed before Chat recovery")
		}
		var binding nativeTurnBinding
		if session.ControlBindingJSON != "" {
			if err := json.Unmarshal([]byte(session.ControlBindingJSON), &binding); err != nil {
				return err
			}
		}
		view, err := displaystate.Read(tx, session)
		if err != nil {
			return err
		}
		if recovery.AttemptID == "" || binding.RecoveredAttemptID != recovery.AttemptID || view.Status != "failed" || view.Reason != "controller_failed" {
			return controlstore.Reject("INVALID_RECOVERY", "no matching interrupted Chat to replace")
		}
		var active int64
		if err := tx.Model(&orm.ChatHistory{}).Where("conversation_id = ? AND run_status = ? AND id <> ?", convID, "generating", historyID).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return controlstore.Reject("INVALID_RECOVERY", "another Chat already owns continuation")
		}
		if err := tx.Model(&orm.ChatHistory{}).Where("id = ? AND conversation_id = ? AND run_status = ?", historyID, convID, "generating").Count(&active).Error; err != nil {
			return err
		}
		if active != 1 {
			return controlstore.Reject("INVALID_RECOVERY", "replacement Chat has not been admitted")
		}
		binding.HistoryID = historyID
		encoded, err := json.Marshal(binding)
		if err != nil {
			return err
		}
		session.StateVersion++
		session.ControlBindingJSON = string(encoded)
		if err := tx.Model(&session).Updates(map[string]any{"control_binding_json": session.ControlBindingJSON, "state_version": session.StateVersion}).Error; err != nil {
			return err
		}
		version = session.StateVersion
		return appendSessionStateEvent(tx, session, "workflow.patch", json.RawMessage(`{"status":"active"}`))
	})
	return version, err
}

// A child can finish just before its Chat failure is committed. Checking from
// the Chat terminal path closes that ordering gap without polling or retries.
func RecoverNativeChatAfterFailure(ctx context.Context, db *gorm.DB, cache state.Store, convID, historyID string) error {
	var sessions []orm.WorkflowSession
	if err := db.WithContext(ctx).Where("conversation_id = ? AND controller_host = ? AND status = ? AND dismissed = false", convID, "lazymind", SessionStatusActive).Find(&sessions).Error; err != nil {
		return err
	}
	for _, session := range sessions {
		bound, err := displaystate.ChatHistoryID(session)
		if err != nil {
			return err
		}
		if bound != historyID {
			continue
		}
		onSSE := func(kind string, payload map[string]any) {
			if subagent.EventHooks != nil {
				subagent.EventHooks.CallConversationEvent(context.Background(), cache, convID, "", kind, payload)
			}
		}
		if _, err := startNativeChatRecovery(ctx, db, cache, session.ID, onSSE); err != nil {
			return err
		}
	}
	return nil
}
