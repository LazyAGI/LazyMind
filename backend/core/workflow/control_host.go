package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
)

type WorkflowHostIdentity struct {
	ConnectorID string `json:"connector_id"`
	Credential  string `json:"credential"`
	InstanceID  string `json:"instance_id"`
}

func (h WorkflowControlHandler) Capabilities(w http.ResponseWriter, r *http.Request) {
	db := h.Service.DB
	ready := db.Migrator().HasTable(&orm.WorkflowReviewCheckpoint{}) && db.Migrator().HasTable(&orm.WorkflowHostAction{}) &&
		db.Migrator().HasColumn(&orm.WorkflowSession{}, "control_protocol") && db.Migrator().HasColumn(&orm.WorkflowSessionStep{}, "submission_hash") && db.Migrator().HasColumn(&orm.WorkflowSessionStep{}, "executor_host")
	common.ReplyOK(w, map[string]any{"protocol": controlpolicy.Protocol, "schema_ready": ready})
}

type WorkflowHostBindingRequest struct {
	ConnectorID     string `json:"connector_id"`
	Credential      string `json:"credential"`
	Provider        string `json:"provider"`
	DriverSessionID string `json:"driver_session_id"`
}

func (s WorkflowControlService) Bind(ctx context.Context, owner, sessionID string, input WorkflowHostBindingRequest) (*controlstore.Snapshot, error) {
	var result *controlstore.Snapshot
	if input.ConnectorID == "" || len(input.ConnectorID) > 128 || len(input.Credential) < 32 || len(input.Credential) > 256 ||
		input.DriverSessionID == "" || len(input.DriverSessionID) > 255 || input.Provider == "" {
		return nil, controlstore.Reject("INVALID_COMMAND", "connector, credential, provider and driver session are required")
	}
	err := controlstore.Transaction(ctx, s.DB, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		if err := controlOwner(*session, owner); err != nil {
			return err
		}
		binding, err := controlstore.DecodeBinding(*session)
		if err != nil {
			return err
		}
		if binding.Provider != "" && binding.Provider != input.Provider {
			return controlstore.Reject("BINDING_CONFLICT", "workflow was prepared for another host provider")
		}
		if binding.DriverSession != "" {
			if _, err := controlstore.AuthorizeHost(*session, input.ConnectorID, input.Credential); err != nil {
				return err
			}
			if binding.DriverSession != input.DriverSessionID {
				return controlstore.Reject("BINDING_CONFLICT", "opening a workflow does not transfer its driver")
			}
		} else {
			if err := ensureNoActiveAttempts(tx, session.ID); err != nil {
				return err
			}
			binding.Provider, binding.ConnectorID, binding.DriverSession = input.Provider, input.ConnectorID, input.DriverSessionID
			binding.CredentialHash = controlstore.Hash([]byte(input.Credential))
			binding.Generation++
		}
		encoded, err := json.Marshal(binding)
		if err != nil {
			return err
		}
		if string(encoded) != session.ControlBindingJSON {
			session.ControlBindingJSON = string(encoded)
			if err := tx.Model(session).Update("control_binding_json", session.ControlBindingJSON).Error; err != nil {
				return err
			}
			if err := controlstore.BumpEvent(tx, session, "binding.changed", session.ID, "", map[string]any{"generation": binding.Generation}); err != nil {
				return err
			}
		}
		result, err = controlstore.Read(tx, *session)
		return err
	})
	return result, err
}

type WorkflowHostActionPage struct {
	Actions       []orm.WorkflowHostAction `json:"actions"`
	NextPageToken string                   `json:"next_page_token,omitempty"`
}

func (s WorkflowControlService) HostActions(ctx context.Context, owner string, identity WorkflowHostIdentity, after string) (WorkflowHostActionPage, error) {
	const pageSize = 100
	page := WorkflowHostActionPage{Actions: []orm.WorkflowHostAction{}}
	if owner == "" || identity.ConnectorID == "" || identity.Credential == "" {
		return page, controlstore.Reject("HOST_AUTH_REQUIRED", "paired connector identity is required")
	}
	var candidates []orm.WorkflowHostAction
	query := s.DB.WithContext(ctx).Model(&orm.WorkflowHostAction{}).
		Joins("JOIN plugin_sessions ON plugin_sessions.id = workflow_host_actions.session_id"). // workflow-naming: persistence
		Where("plugin_sessions.create_user_id = ?", owner).                                     // workflow-naming: persistence
		Where("workflow_host_actions.connector_id = ? AND workflow_host_actions.status IN ?",
			identity.ConnectorID, []string{"pending", "dispatching", "unknown"}).Where("workflow_host_actions.consumed_at IS NULL OR workflow_host_actions.status = 'dispatching'")
	if after != "" {
		query = query.Where("workflow_host_actions.id > ?", after)
	}
	if err := query.Select("workflow_host_actions.*").Order("workflow_host_actions.id ASC").Limit(pageSize).Find(&candidates).Error; err != nil {
		return page, err
	}
	for _, action := range candidates {
		var session orm.WorkflowSession
		if err := s.DB.WithContext(ctx).Where("id = ?", action.SessionID).First(&session).Error; err != nil {
			return page, err
		}
		binding, err := controlstore.AuthorizeHost(session, identity.ConnectorID, identity.Credential)
		if err != nil {
			return page, err
		}
		if action.BindingGeneration == binding.Generation {
			page.Actions = append(page.Actions, action)
		}
	}
	if len(candidates) == pageSize {
		page.NextPageToken = candidates[len(candidates)-1].ID
	}
	return page, nil
}

// All action reads and writes revalidate the binding under the session lock.
func authorizeHostAction(tx *gorm.DB, session orm.WorkflowSession, owner, id string, identity WorkflowHostIdentity, action *orm.WorkflowHostAction) (controlstore.Binding, error) {
	if err := controlOwner(session, owner); err != nil {
		return controlstore.Binding{}, err
	}
	binding, err := controlstore.AuthorizeHost(session, identity.ConnectorID, identity.Credential)
	if err != nil {
		return binding, err
	}
	if err := tx.Where("id = ? AND session_id = ?", id, session.ID).First(action).Error; err != nil {
		return binding, err
	}
	if action.BindingGeneration != binding.Generation {
		return binding, controlstore.Reject("BINDING_STALE", "host action belongs to an older binding")
	}
	return binding, nil
}

func (s WorkflowControlService) HostAction(ctx context.Context, owner, id string, identity WorkflowHostIdentity) (WorkflowHostClaim, error) {
	var result WorkflowHostClaim
	if err := s.DB.WithContext(ctx).Where("id = ?", id).First(&result.Action).Error; err != nil {
		return result, err
	}
	err := controlstore.Transaction(ctx, s.DB, result.Action.SessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		_, err := authorizeHostAction(tx, *session, owner, id, identity, &result.Action)
		if err != nil {
			return err
		}
		result.Control, err = controlstore.Read(tx, *session)
		return err
	})
	return result, err
}

type WorkflowHostClaim struct {
	Action        orm.WorkflowHostAction `json:"action"`
	DispatchToken string                 `json:"dispatch_token,omitempty"`
	Control       *controlstore.Snapshot `json:"control"`
}

func (s WorkflowControlService) ClaimHostAction(ctx context.Context, owner, actionID string, identity WorkflowHostIdentity) (WorkflowHostClaim, error) {
	var result WorkflowHostClaim
	if identity.InstanceID == "" || len(identity.InstanceID) > 128 {
		return result, controlstore.Reject("INVALID_COMMAND", "instance_id is required")
	}
	var hint orm.WorkflowHostAction
	if err := s.DB.WithContext(ctx).Where("id = ?", actionID).First(&hint).Error; err != nil {
		return result, err
	}
	err := controlstore.Transaction(ctx, s.DB, hint.SessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		action := &result.Action
		binding, err := authorizeHostAction(tx, *session, owner, actionID, identity, action)
		if err != nil {
			return err
		}
		if action.NativeSessionID != binding.DriverSession {
			return controlstore.Reject("BINDING_STALE", "host action belongs to an older binding")
		}
		if action.ConsumedAt != nil && action.Status == "pending" {
			return controlstore.Reject("ACTION_CONSUMED", "workflow execution already consumed this continuation")
		}
		now := time.Now().UTC()
		if action.Status == "dispatching" {
			if action.DispatchExpiresAt != nil && action.DispatchExpiresAt.After(now) {
				return controlstore.Reject("DELIVERY_PENDING", "another dispatch owns this action")
			}
			// Expiry cannot prove that the former sender never reached the host.
			if err := tx.Model(action).Updates(map[string]any{"status": "unknown", "updated_at": now}).Error; err != nil {
				return err
			}
			action.Status = "unknown"
			if err := controlstore.BumpEvent(tx, session, "delivery.changed", action.ID, "", map[string]any{"status": "unknown"}); err != nil {
				return err
			}
			result.Control, err = controlstore.Read(tx, *session)
			return err
		}
		if action.Status != "pending" {
			result.Control, err = controlstore.Read(tx, *session)
			return err
		}
		if action.Kind == "continue" {
			if action.ExecutionID == "" {
				if err := controlstore.GuardBegin(tx, *session); err != nil {
					return err
				}
				if err := ensureNoActiveAttempts(tx, session.ID); err != nil {
					return err
				}
			} else {
				if err := controlstore.GuardClaim(tx, *session); err != nil {
					return err
				}
			}
		} else if action.Kind == "cancel" && session.Status != "stopped" {
			return controlstore.Reject("BINDING_STALE", "workflow no longer requests cancellation")
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		result.DispatchToken = hex.EncodeToString(secret)
		expires := now.Add(30 * time.Second)
		if err := tx.Model(action).Updates(map[string]any{"status": "dispatching", "dispatch_owner": identity.InstanceID,
			"dispatch_token_hash": controlstore.Hash([]byte(result.DispatchToken)), "dispatch_expires_at": expires,
			"updated_at": now}).Error; err != nil {
			return err
		}
		action.Status = "dispatching"
		action.DispatchOwner = identity.InstanceID
		action.DispatchExpiresAt = &expires
		result.Control, err = controlstore.Read(tx, *session)
		return err
	})
	return result, err
}

type WorkflowHostReceipt struct {
	ConnectorID    string `json:"connector_id"`
	Credential     string `json:"credential"`
	InstanceID     string `json:"instance_id"`
	DispatchToken  string `json:"dispatch_token"`
	Status         string `json:"status"`
	NativeEventSeq int64  `json:"native_event_seq,omitempty"`
	Error          string `json:"error,omitempty"`
}

func (s WorkflowControlService) SettleHostAction(ctx context.Context, owner, actionID string, receipt WorkflowHostReceipt) (orm.WorkflowHostAction, error) {
	var action orm.WorkflowHostAction
	if err := s.DB.WithContext(ctx).Where("id = ?", actionID).First(&action).Error; err != nil {
		return action, err
	}
	err := controlstore.Transaction(ctx, s.DB, action.SessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		identity := WorkflowHostIdentity{ConnectorID: receipt.ConnectorID, Credential: receipt.Credential, InstanceID: receipt.InstanceID}
		if _, err := authorizeHostAction(tx, *session, owner, actionID, identity, &action); err != nil {
			return err
		}
		if !controlpolicy.CanSettleDelivery(action.Status, receipt.Status) {
			return controlstore.Reject("DELIVERY_CONFLICT", "invalid delivery state transition")
		}
		if action.Status == receipt.Status {
			return nil
		}
		// A paired plugin can reconcile the exact standard input event after restart.
		if receipt.Status != "accepted" || receipt.NativeEventSeq <= 0 {
			if action.Status == "unknown" {
				return controlstore.Reject("DELIVERY_UNKNOWN", "reconciliation requires the exact durable host event")
			}
			if action.DispatchOwner != receipt.InstanceID || receipt.DispatchToken == "" || controlstore.Hash([]byte(receipt.DispatchToken)) != action.DispatchTokenHash {
				return controlstore.Reject("HOST_AUTH_REQUIRED", "receipt does not belong to the dispatch owner")
			}
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": receipt.Status, "native_event_seq": receipt.NativeEventSeq, "last_error": receipt.Error, "updated_at": now}
		if receipt.Status == "accepted" {
			updates["accepted_at"] = now
		}
		if err := tx.Model(&action).Updates(updates).Error; err != nil {
			return err
		}
		return controlstore.BumpEvent(tx, session, "delivery.changed", action.ID, "", map[string]any{"status": receipt.Status})
	})
	return action, err
}

func (h WorkflowControlHandler) Action(w http.ResponseWriter, r *http.Request) {
	identity := WorkflowHostIdentity{ConnectorID: r.URL.Query().Get("connector_id"), Credential: r.Header.Get("X-Workflow-Host-Credential")}
	result, err := h.Service.HostAction(r.Context(), strings.TrimSpace(r.Header.Get("X-User-Id")), common.PathVar(r, "action_id"), identity)
	if err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h WorkflowControlHandler) Bind(w http.ResponseWriter, r *http.Request) {
	var input WorkflowHostBindingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input); err != nil {
		writeWorkflowControlError(w, controlstore.Reject("INVALID_COMMAND", "invalid host binding"))
		return
	}
	state, err := h.Service.Bind(r.Context(), strings.TrimSpace(r.Header.Get("X-User-Id")), common.PathVar(r, "session_id"), input)
	if err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"control": state})
}

func (h WorkflowControlHandler) Actions(w http.ResponseWriter, r *http.Request) {
	identity := WorkflowHostIdentity{ConnectorID: r.URL.Query().Get("connector_id"), Credential: r.Header.Get("X-Workflow-Host-Credential")}
	page, err := h.Service.HostActions(r.Context(), strings.TrimSpace(r.Header.Get("X-User-Id")), identity, r.URL.Query().Get("after"))
	if err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, page)
}

func (h WorkflowControlHandler) ClaimAction(w http.ResponseWriter, r *http.Request) {
	var identity WorkflowHostIdentity
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&identity); err != nil {
		writeWorkflowControlError(w, controlstore.Reject("INVALID_COMMAND", "invalid host claim"))
		return
	}
	result, err := h.Service.ClaimHostAction(r.Context(), strings.TrimSpace(r.Header.Get("X-User-Id")), common.PathVar(r, "action_id"), identity)
	if err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h WorkflowControlHandler) SettleAction(w http.ResponseWriter, r *http.Request) {
	var receipt WorkflowHostReceipt
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&receipt); err != nil {
		writeWorkflowControlError(w, controlstore.Reject("INVALID_COMMAND", "invalid host receipt"))
		return
	}
	if len(receipt.Error) > 4096 {
		receipt.Error = receipt.Error[:4096]
	}
	result, err := h.Service.SettleHostAction(r.Context(), strings.TrimSpace(r.Header.Get("X-User-Id")), common.PathVar(r, "action_id"), receipt)
	if err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"action": result})
}
