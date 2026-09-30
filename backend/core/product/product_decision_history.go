package product

import (
	"context"
	"encoding/json"

	"lazymind/core/common/orm"
)

// A model may rerun its finalizer using the Session's original immutable seed.
// Read-side reconciliation keeps later human confirmations durable without
// rewriting any old output, importing another Session, or trusting model events.
func (r *Repository) restoreProductDecisionEvents(ctx context.Context, owner string, session orm.WorkflowSession, workspace map[string]any) error {
	productReopenAutomaticDecisions(workspace)
	var revisions []orm.WorkflowSlotRevision
	if err := r.db.WithContext(ctx).Where("session_id = ? AND slot_id = 'workspace_state' AND change_source = 'human' AND human_artifact_id IS NULL", session.ID).Order("revision ASC, created_at ASC").Find(&revisions).Error; err != nil {
		return err
	}
	verified := []map[string]any{}
	seen := map[string]bool{}
	for _, revision := range revisions {
		previous := productObject(revision.ContentSnapshot)
		if previous["workspace_id"] != workspace["workspace_id"] {
			continue
		}
		events, _ := previous["approval_events"].([]any)
		for _, value := range events {
			event, _ := value.(map[string]any)
			id := productString(event["approval_id"])
			human := (event["action"] == "accept-decision" || event["action"] == "defer-decision") && event["approved_by"] == owner
			if id == "" || seen[id] || event["source_session_id"] != session.ID || event["workspace_id"] != workspace["workspace_id"] || !human {
				continue
			}
			snapshot := productString(event["decision_snapshot_json"])
			if snapshot == "" || event["decision_hash"] != "sha256:"+requestHash([]byte(snapshot)) || event["content_sha256"] == nil || event["artifact_id"] == nil || event["version"] == nil {
				continue
			}
			requestJSON := productString(event["command_request_json"])
			commandID := productString(event["command_id"])
			var request struct {
				SessionID  string                 `json:"session_id"`
				DecisionID string                 `json:"decision_id"`
				Request    ProductDecisionRequest `json:"request"`
			}
			if json.Unmarshal([]byte(requestJSON), &request) != nil || request.SessionID != session.ID || request.DecisionID != event["decision_id"] || request.Request.ExpectedDecisionHash != event["decision_hash"] || request.Request.IdempotencyKey != commandID {
				continue
			}
			wantAction := "accept-decision"
			if request.Request.Action == "defer" {
				wantAction = "defer-decision"
			}
			if (request.Request.Action != "accept" && request.Request.Action != "defer") || event["action"] != wantAction || event["reference"] != "workflow-command:"+commandID+":decision:"+request.DecisionID {
				continue
			}
			var command Command
			if err := r.db.WithContext(ctx).Where("command_id = ? AND owner_user_id = ? AND session_id = ? AND http_status < 400", commandID, owner, session.ID).First(&command).Error; err != nil || command.RequestHash != requestHash([]byte(requestJSON)) {
				continue
			}
			seen[id] = true
			verified = append(verified, event)
		}
	}
	if len(verified) == 0 {
		return nil
	}
	decisions := productCurrentDecisions(workspace)
	approvals, _ := workspace["approval_events"].([]any)
	known := map[string]bool{}
	for _, value := range approvals {
		entry, _ := value.(map[string]any)
		known[productString(entry["approval_id"])] = true
	}
	for _, event := range verified {
		if !known[productString(event["approval_id"])] {
			approvals = append(approvals, event)
			known[productString(event["approval_id"])] = true
		}
		var accepted map[string]any
		if json.Unmarshal([]byte(productString(event["decision_snapshot_json"])), &accepted) != nil {
			continue
		}
		index := -1
		for i, decision := range decisions {
			if decision["decision_id"] == event["decision_id"] {
				index = i
				break
			}
		}
		if event["action"] == "defer-decision" {
			if index < 0 {
				continue
			}
			candidate := productDecisionCandidate(decisions[index])
			_, digest := productDecisionSnapshot(candidate)
			if digest == event["decision_hash"] && candidate["status"] != "accepted" {
				candidate["deferred"], candidate["deferral_ref"] = true, event["reference"]
			}
			continue
		}
		accepted["status"], accepted["accepted_by"], accepted["acceptance_ref"] = "accepted", owner, event["reference"]
		if index < 0 {
			decisions = append(decisions, accepted)
			continue
		}
		current := decisions[index]
		_, baselineDigest := productDecisionSnapshot(current)
		if current["acceptance_ref"] == event["reference"] && baselineDigest == event["decision_hash"] {
			continue
		}
		candidate := productDecisionCandidate(current)
		_, currentDigest := productDecisionSnapshot(candidate)
		if currentDigest != event["decision_hash"] {
			// A changed model proposal cannot replace the previously accepted baseline.
			proposal := map[string]any{}
			for key, value := range candidate {
				proposal[key] = value
			}
			proposal["status"], proposal["reopens"] = "proposed", event["decision_id"]
			delete(proposal, "accepted_by")
			delete(proposal, "acceptance_ref")
			delete(proposal, "pending_revisions")
			accepted["pending_revisions"] = []any{proposal}
		}
		decisions[index] = accepted
	}
	values := make([]any, len(decisions))
	for index, decision := range decisions {
		values[index] = decision
	}
	workspace["decisions"], workspace["approvals"], workspace["approval_events"] = values, approvals, approvals
	return nil
}

// Previous versions marked navigation as approval. Keep the historical audit
// events, but require a real human decision before treating that content as a
// baseline again. Reconciliation above may restore a verified later approval.
func productReopenAutomaticDecisions(workspace map[string]any) {
	var reopen func(map[string]any)
	reopen = func(decision map[string]any) {
		if decision["accepted_by"] == "product-workflow-default" {
			decision["status"] = "proposed"
			delete(decision, "accepted_by")
			delete(decision, "acceptance_ref")
		}
		pending, _ := decision["pending_revisions"].([]any)
		for _, value := range pending {
			if child, ok := value.(map[string]any); ok {
				reopen(child)
			}
		}
	}
	for _, decision := range productCurrentDecisions(workspace) {
		reopen(decision)
	}
}
