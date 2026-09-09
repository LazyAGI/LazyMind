package controlstore

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// ApplyLifecycle shares stop/resume effects across command formats. The caller
// holds the session lock and commits the version, event and receipt together.
func ApplyLifecycle(tx *gorm.DB, session *orm.WorkflowSession, commandID string, stopped bool) (actionID string, changed bool, err error) {
	controlled := Controlled(*session)
	if controlled && session.Dismissed {
		return "", false, Reject("SESSION_STOPPED", "workflow has been dismissed")
	}
	var binding Binding
	if controlled {
		binding, err = DecodeBinding(*session)
		if err != nil {
			return "", false, err
		}
	}
	now := time.Now().UTC()
	if stopped {
		if session.Status == "completed" || (!controlled && session.Status == "failed") {
			code := "WORKFLOW_TERMINAL"
			if controlled {
				code = "SESSION_TERMINAL"
			}
			return "", false, Reject(code, "the workflow is already terminal")
		}
		changed = session.Status != "stopped"
		if changed {
			attempts := tx.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND status IN ?", session.ID,
				[]string{"queued", "claimed", "running", "pending"})
			updates := map[string]any{"status": "interrupted", "terminal_code": "WORKFLOW_STOPPED", "lease_expires_at": nil, "updated_at": now}
			if controlled {
				attempts = attempts.Where("validity = ?", "effective")
				updates = map[string]any{"status": "cancelled", "lease_token": "", "lease_expires_at": nil,
					"fencing_generation": gorm.Expr("fencing_generation + 1"), "updated_at": now}
				binding.Generation++
			}
			if err := attempts.Updates(updates).Error; err != nil {
				return "", false, err
			}
			if err := tx.Model(&orm.WorkflowOutbox{}).Where("session_id = ? AND status IN ?", session.ID, []string{"pending", "claimed"}).
				Updates(map[string]any{"status": "cancelled", "updated_at": now}).Error; err != nil {
				return "", false, err
			}
			if controlled {
				if err := tx.Model(&orm.WorkflowHostAction{}).Where("session_id = ? AND status = ? AND kind = ?", session.ID, "pending", "continue").
					Updates(map[string]any{"status": "superseded", "updated_at": now}).Error; err != nil {
					return "", false, err
				}
			}
			session.Status = "stopped"
		}
	} else {
		if session.Status != "stopped" {
			return "", false, Reject("WORKFLOW_NOT_STOPPED", "the workflow is not stopped")
		}
		if controlled {
			var cancelling int64
			if err := tx.Model(&orm.WorkflowHostAction{}).Where("session_id = ? AND binding_generation = ? AND kind = 'cancel' AND status IN ?", session.ID, binding.Generation,
				[]string{"pending", "dispatching", "unknown"}).Count(&cancelling).Error; err != nil {
				return "", false, err
			}
			if cancelling != 0 {
				return "", false, Reject("DELIVERY_PENDING", "wait for the host to acknowledge cancellation before resuming")
			}
			binding.Generation++
			session.Status = "waiting"
		} else {
			session.Status = "active"
		}
		changed = true
	}
	if changed {
		updates := map[string]any{"status": session.Status, "updated_at": now}
		if controlled {
			encoded, err := json.Marshal(binding)
			if err != nil {
				return "", false, err
			}
			session.ControlBindingJSON = string(encoded)
			updates["control_binding_json"] = session.ControlBindingJSON
		}
		if err := tx.Model(session).Updates(updates).Error; err != nil {
			return "", false, err
		}
	}
	if stopped && controlled && binding.DriverSession != "" {
		actionID, err = EnqueueHostAction(tx, *session, commandID, "cancel", "")
	}
	return actionID, changed, err
}

func EnqueueHostAction(tx *gorm.DB, session orm.WorkflowSession, commandID, kind, executionID string) (string, error) {
	binding, err := DecodeBinding(session)
	if err != nil {
		return "", err
	}
	if binding.DriverSession == "" || binding.ConnectorID == "" {
		return "", Reject("BINDING_REQUIRED", "a paired host is required to resume automatically")
	}
	var previous orm.WorkflowHostAction
	err = tx.Where("session_id = ? AND binding_generation = ? AND kind = ? AND consumed_at IS NULL AND status IN ?",
		session.ID, binding.Generation, kind, []string{"pending", "dispatching", "unknown", "accepted"}).Order("created_at DESC").First(&previous).Error
	if err == nil {
		if previous.Status == "unknown" {
			return "", Reject("DELIVERY_UNKNOWN", "reconcile the previous host delivery before sending another")
		}
		if previous.ExecutionID != executionID {
			return "", Reject("DELIVERY_PENDING", "another host action is still being delivered")
		}
		return previous.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	now := time.Now().UTC()
	action := orm.WorkflowHostAction{ID: uuid.NewString(), SessionID: session.ID, CommandID: commandID, Kind: kind,
		BindingGeneration: binding.Generation, ConnectorID: binding.ConnectorID, NativeSessionID: binding.DriverSession,
		ExecutionID: executionID, Status: "pending", CreatedAt: now, UpdatedAt: now}
	return action.ID, tx.Create(&action).Error
}
