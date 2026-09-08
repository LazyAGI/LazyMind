// Package controlstore implements transactional persistence for Workflow control.
package controlstore

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string          { return e.Code + ": " + e.Message }
func Reject(code, message string) error { return &Error{Code: code, Message: message} }

type Binding struct {
	Required       bool     `json:"required"`
	Provider       string   `json:"provider,omitempty"`
	ConnectorID    string   `json:"connector_id,omitempty"`
	DriverSession  string   `json:"driver_session_id,omitempty"`
	Workers        []string `json:"worker_session_ids,omitempty"`
	Generation     int64    `json:"generation"`
	CredentialHash string   `json:"credential_hash,omitempty"`
}

type BindingView struct {
	Provider      string `json:"provider,omitempty"`
	ConnectorID   string `json:"connector_id,omitempty"`
	DriverSession string `json:"driver_session_id,omitempty"`
	Generation    int64  `json:"generation"`
	Bound         bool   `json:"bound"`
}

type Snapshot struct {
	Protocol           string                         `json:"protocol"`
	SessionID          string                         `json:"session_id"`
	StateVersion       int64                          `json:"state_version"`
	Continuation       string                         `json:"continuation"`
	Admission          controlpolicy.Admission        `json:"admission"`
	Reviews            []orm.WorkflowReviewCheckpoint `json:"reviews"`
	Binding            BindingView                    `json:"binding"`
	Delivery           *orm.WorkflowHostAction        `json:"delivery"`
	ActiveExecutions   int64                          `json:"active_executions"`
	ActiveExecutionIDs []string                       `json:"active_execution_ids"`
	AvailableActions   []string                       `json:"available_actions"`
}

func Hash(value []byte) string { h := sha256.Sum256(value); return hex.EncodeToString(h[:]) }

func DecodeBinding(session orm.WorkflowSession) (Binding, error) {
	var binding Binding
	if session.ControlBindingJSON == "" {
		return binding, nil
	}
	if err := json.Unmarshal([]byte(session.ControlBindingJSON), &binding); err != nil {
		return binding, Reject("BINDING_INVALID", "stored host binding is invalid")
	}
	return binding, nil
}

func Controlled(session orm.WorkflowSession) bool {
	return session.ControlProtocol == controlpolicy.Protocol
}

func LockSession(tx *gorm.DB, sessionID string) (orm.WorkflowSession, error) {
	var session orm.WorkflowSession
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sessionID).First(&session).Error
	return session, err
}

// ValidateExecution runs at the actual write boundary, not merely at request admission.
func ValidateExecution(tx *gorm.DB, session orm.WorkflowSession, attemptID, handle string) error {
	if session.Status == "stopped" || session.Dismissed {
		return Reject("SESSION_STOPPED", "the workflow is stopped")
	}
	if handle == "" {
		return Reject("EXECUTION_FENCED", "execution_handle is required")
	}
	var count int64
	if err := tx.Model(&orm.WorkflowSessionStep{}).Where("id = ? AND session_id = ? AND validity = 'effective' AND lease_token = ? AND lease_expires_at >= ? AND status IN ?",
		attemptID, session.ID, handle, time.Now().UTC(), []string{"claimed", "running"}).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return Reject("EXECUTION_FENCED", "the execution no longer owns this attempt; resume before submitting")
	}
	return nil
}

func Transaction(ctx context.Context, db *gorm.DB, sessionID string, fn func(*gorm.DB, *orm.WorkflowSession) error) error {
	return common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		session, err := LockSession(tx, sessionID)
		if err != nil {
			return err
		}
		return fn(tx, &session)
	})
}

func Read(tx *gorm.DB, session orm.WorkflowSession) (*Snapshot, error) {
	if !Controlled(session) {
		return nil, nil
	}
	binding, err := DecodeBinding(session)
	if err != nil {
		return nil, err
	}
	result := &Snapshot{Protocol: controlpolicy.Protocol, SessionID: session.ID, StateVersion: session.StateVersion,
		Reviews: []orm.WorkflowReviewCheckpoint{}, AvailableActions: []string{},
		Binding: BindingView{Provider: binding.Provider, ConnectorID: binding.ConnectorID,
			DriverSession: binding.DriverSession, Generation: binding.Generation, Bound: binding.DriverSession != ""}}
	if err := tx.Where("session_id = ?", session.ID).Order("created_at ASC, id ASC").Find(&result.Reviews).Error; err != nil {
		return nil, err
	}
	var pending int64
	for _, review := range result.Reviews {
		if review.Status == "pending" {
			pending++
		}
	}
	result.ActiveExecutionIDs = []string{}
	if err := tx.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND validity = 'effective' AND status IN ?",
		session.ID, []string{"queued", "pending", "claimed", "running"}).Order("id ASC").Pluck("id", &result.ActiveExecutionIDs).Error; err != nil {
		return nil, err
	}
	result.ActiveExecutions = int64(len(result.ActiveExecutionIDs))
	result.Continuation, result.Admission = controlpolicy.Decide(controlpolicy.Facts{Status: session.Status,
		Dismissed: session.Dismissed, PendingReviews: pending, ActiveAttempts: result.ActiveExecutions,
		BindingRequired: binding.Required, Bound: result.Binding.Bound})
	var action orm.WorkflowHostAction
	err = tx.Where("session_id = ? AND binding_generation = ?", session.ID, binding.Generation).Order("created_at DESC, id DESC").First(&action).Error
	if err == nil {
		result.Delivery = &action
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if session.Status != "stopped" && !session.Dismissed {
		if session.Status != "completed" {
			result.AvailableActions = append(result.AvailableActions, "stop")
		}
		if pending > 0 {
			result.AvailableActions = append(result.AvailableActions, "save", "confirm")
		}
		if result.ActiveExecutions == 0 {
			result.AvailableActions = append(result.AvailableActions, "retry", "rewind")
			if result.Binding.Bound {
				if pending == 1 {
					result.AvailableActions = append(result.AvailableActions, "confirm_and_continue")
				}
				if pending == 0 && session.Status != "completed" && session.Status != "failed" {
					result.AvailableActions = append(result.AvailableActions, "continue")
				}
			}
		}
	} else if !session.Dismissed {
		result.AvailableActions = append(result.AvailableActions, "resume")
	}
	return result, nil
}

func GuardBegin(tx *gorm.DB, session orm.WorkflowSession) error {
	if session.Status == "stopped" || session.Dismissed {
		return Reject("SESSION_STOPPED", "the workflow is stopped")
	}
	state, err := Read(tx, session)
	if err != nil || state == nil {
		return err
	}
	if !state.Admission.CanBegin {
		return Reject("WORKFLOW_ADMISSION_DENIED", state.Admission.Reason)
	}
	return nil
}

// Claim consumes a grant already issued before the review barrier; it creates no new work.
func GuardClaim(tx *gorm.DB, session orm.WorkflowSession) error {
	if session.Status == "stopped" || session.Dismissed {
		return Reject("SESSION_STOPPED", "the workflow is stopped")
	}
	if !Controlled(session) {
		return nil
	}
	binding, err := DecodeBinding(session)
	if err != nil {
		return err
	}
	if binding.Required && binding.DriverSession == "" {
		return Reject("BINDING_REQUIRED", "bind the workflow to its host before executing")
	}
	return nil
}

func AuthorizeHost(session orm.WorkflowSession, connectorID, credential string) (Binding, error) {
	binding, err := DecodeBinding(session)
	if err != nil {
		return binding, err
	}
	if connectorID == "" || credential == "" || connectorID != binding.ConnectorID || binding.CredentialHash == "" ||
		subtle.ConstantTimeCompare([]byte(Hash([]byte(credential))), []byte(binding.CredentialHash)) != 1 {
		return binding, Reject("HOST_AUTH_REQUIRED", "a paired host connection is required")
	}
	return binding, nil
}

// BumpEvent is called while the session row is locked, before allocating its event cursor.
func BumpEvent(tx *gorm.DB, session *orm.WorkflowSession, kind, entity, commandID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := tx.Model(&orm.WorkflowSession{}).Where("id = ?", session.ID).
		Updates(map[string]any{"state_version": gorm.Expr("state_version + 1"), "updated_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Where("id = ?", session.ID).First(session).Error; err != nil {
		return err
	}
	return tx.Create(&orm.WorkflowEvent{SessionID: session.ID, OwnerUserID: session.CreateUserID,
		ContractVersion: "workflow.v1", EventType: kind, EntityID: entity, StateVersion: session.StateVersion,
		CommandID: commandID, PayloadJSON: data, CreatedAt: now}).Error
}
