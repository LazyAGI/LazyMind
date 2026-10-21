package product

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ProductDecisionRequest struct {
	Action               string `json:"action"`
	IdempotencyKey       string `json:"idempotency_key"`
	ExpectedStateVersion int64  `json:"expected_state_version"`
	ExpectedDecisionHash string `json:"expected_decision_hash"`
	Handling             string `json:"handling,omitempty"`
}

func productDecisionContent(decision map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range decision {
		switch key {
		case "status", "accepted_by", "acceptance_ref", "reopens", "pending_revisions", "deferred", "deferral_ref":
			continue
		}
		result[key] = value
	}
	return result
}

func productDecisionSnapshot(decision map[string]any) (string, string) {
	snapshot, _ := json.Marshal(productDecisionContent(decision))
	return string(snapshot), "sha256:" + requestHash(snapshot)
}

func productCurrentDecisions(workspace map[string]any) []map[string]any {
	entries, _ := workspace["decisions"].([]any)
	result := []map[string]any{}
	indices := map[string]int{}
	for _, entry := range entries {
		decision, _ := entry.(map[string]any)
		id := productString(decision["decision_id"])
		if id == "" {
			continue
		}
		if index, exists := indices[id]; exists {
			result[index] = decision
		} else {
			indices[id] = len(result)
			result = append(result, decision)
		}
	}
	return result
}

func productDecisionCandidate(decision map[string]any) map[string]any {
	pending, _ := decision["pending_revisions"].([]any)
	for index := len(pending) - 1; index >= 0; index-- {
		candidate, _ := pending[index].(map[string]any)
		if candidate["decision_id"] == decision["decision_id"] && candidate["status"] != "superseded" {
			return candidate
		}
	}
	return decision
}

func productDecisionBusinessValue(value any) any {
	switch object := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, nested := range object {
			if strings.HasPrefix(key, "internal_") || strings.HasPrefix(key, "runtime_") || key == "visibility" || key == "host_artifact" || key == "source_session_id" {
				continue
			}
			result[key] = productDecisionBusinessValue(nested)
		}
		return result
	case []any:
		result := make([]any, len(object))
		for index, nested := range object {
			result[index] = productDecisionBusinessValue(nested)
		}
		return result
	default:
		return value
	}
}

func productPublicDecisions(workspace map[string]any) []map[string]any {
	result := []map[string]any{}
	for _, baseline := range productCurrentDecisions(workspace) {
		decision := productDecisionCandidate(baseline)
		if decision["status"] == "superseded" {
			continue
		}
		visible := productBusinessRecords([]any{decision}, "decision_id", "title", "statement", "decision", "summary", "question", "decision_question", "status", "risk", "owner", "rationale", "deferred")
		if len(visible) == 0 {
			continue
		}
		entry := visible[0]
		_, digest := productDecisionSnapshot(decision)
		entry["decision_hash"] = digest
		entry["decision_content"] = productDecisionBusinessValue(productDecisionContent(decision))
		for _, key := range []string{"value", "hard_gates"} {
			if value, exists := decision[key]; exists {
				entry[key] = productDecisionBusinessValue(value)
			}
		}
		if entry["statement"] == nil && entry["decision"] != nil {
			entry["statement"] = entry["decision"]
		}
		if entry["status"] == nil {
			entry["status"] = "proposed"
		}
		required := decision["risk"] == "high" || decision["risk"] == "irreversible"
		switch gates := decision["hard_gates"].(type) {
		case []any:
			required = required || len(gates) > 0
		case map[string]any:
			required = required || len(gates) > 0
		case bool:
			required = required || gates
		}
		entry["confirmation_required"] = required
		entry["has_accepted_baseline"] = baseline["status"] == "accepted" && decision["status"] != "accepted"
		if entry["has_accepted_baseline"] == true {
			entry["accepted_baseline_value"] = productDecisionBusinessValue(baseline["value"])
		}
		run, _ := workspace["current_run"].(map[string]any)
		for _, stage := range productStages {
			if stage.ID == run["selected_stage"] {
				artifact := productRegisteredView(workspace, stage.Slot)
				entry["artifact_title"], entry["artifact_version"] = artifact["title"], artifact["version"]
			}
		}
		result = append(result, entry)
	}
	return result
}

func (r *Repository) DecideProductDecision(ctx context.Context, owner, sessionID, decisionID string, req ProductDecisionRequest) (json.RawMessage, error) {
	if err := r.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return nil, err
	}
	if decisionID == "" || len(decisionID) > 256 || (req.Action != "accept" && req.Action != "defer") || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 || req.ExpectedStateVersion < 1 || !strings.HasPrefix(req.ExpectedDecisionHash, "sha256:") || len([]rune(req.Handling)) > 2000 {
		return nil, repositoryError("INVALID_PRODUCT_DECISION")
	}
	body, _ := json.Marshal(map[string]any{"session_id": sessionID, "decision_id": decisionID, "request": req})
	command, _, err := r.AtomicCommand(ctx, owner, sessionID, req.IdempotencyKey, "workflow.v1", body, func(tx *gorm.DB) (int, json.RawMessage, error) {
		txRepo := New(tx)
		session, workspace, artifacts, err := txRepo.productRelayState(ctx, owner, sessionID)
		if err != nil {
			return 0, nil, err
		}
		if err := txRepo.validateProductCommand(ctx, owner, session, workspace, req.ExpectedStateVersion, true); err != nil {
			return 0, nil, err
		}
		run, _ := workspace["current_run"].(map[string]any)
		decisions := productCurrentDecisions(workspace)
		index := -1
		for i, decision := range decisions {
			if decision["decision_id"] == decisionID {
				index = i
				break
			}
		}
		if index < 0 {
			return 0, nil, repositoryError("PRODUCT_DECISION_NOT_FOUND")
		}
		baseline := decisions[index]
		candidate := productDecisionCandidate(baseline)
		snapshot, digest := productDecisionSnapshot(candidate)
		if digest != req.ExpectedDecisionHash {
			return 0, nil, repositoryError("PRODUCT_DECISION_CHANGED")
		}
		if candidate["status"] == "accepted" || candidate["status"] == "superseded" {
			return 0, nil, repositoryError("INVALID_PRODUCT_DECISION")
		}
		// A just-edited document must be assessed again before its decision can be
		// accepted. The approval is for exact registered content, not a stale card.
		manifest := productManifest(artifacts)
		host, _ := manifest["host_artifact"].(map[string]any)
		if !txRepo.productArtifactMatches(ctx, session, artifacts, host) {
			return 0, nil, repositoryError("PRODUCT_ARTIFACT_CHANGED")
		}
		now := time.Now().UTC()
		reference := "workflow-command:" + req.IdempotencyKey + ":decision:" + decisionID
		event := map[string]any{"approval_id": uuid.NewString(), "action": "accept-decision", "decision_id": decisionID,
			"decision_hash": digest, "decision_snapshot_json": snapshot, "stage": run["selected_stage"], "workspace_id": workspace["workspace_id"],
			"command_id": req.IdempotencyKey, "command_request_json": string(body),
			"artifact_id": manifest["artifact_id"], "version": manifest["version"], "content_sha256": host["content_sha256"],
			"approved_by": owner, "approved_at": now.Format(time.RFC3339Nano), "source": "user-interface", "reference": reference,
			"source_session_id": sessionID, "handling": strings.TrimSpace(req.Handling)}
		updated := map[string]any{}
		for key, value := range candidate {
			updated[key] = value
		}
		if req.Action == "accept" {
			updated["status"], updated["accepted_by"], updated["acceptance_ref"] = "accepted", owner, reference
			delete(updated, "deferred")
			delete(updated, "deferral_ref")
			delete(updated, "pending_revisions")
			history, _ := workspace["decision_history"].([]any)
			workspace["decision_history"] = append(history, baseline)
			decisions[index] = updated
		} else {
			event["action"] = "defer-decision"
			updated["status"], updated["deferred"], updated["deferral_ref"] = "proposed", true, reference
			delete(updated, "accepted_by")
			delete(updated, "acceptance_ref")
			if baseline["status"] == "accepted" {
				pending, _ := baseline["pending_revisions"].([]any)
				if len(pending) > 0 {
					pending[len(pending)-1] = updated
					baseline["pending_revisions"] = pending
				}
			} else {
				decisions[index] = updated
			}
		}
		workspace["decisions"] = decisions
		approvals, _ := workspace["approval_events"].([]any)
		if approvals == nil {
			approvals, _ = workspace["approvals"].([]any)
		}
		approvals = append(approvals, event)
		workspace["approvals"], workspace["approval_events"] = approvals, approvals
		if err := txRepo.saveProductWorkspace(tx, session, workspace, now); err != nil {
			return 0, nil, err
		}
		result, _ := json.Marshal(map[string]any{"session_id": sessionID, "state_version": session.StateVersion + 1, "decision_id": decisionID, "status": updated["status"], "deferred": req.Action == "defer"})
		return http.StatusOK, result, nil
	})
	if err != nil {
		return nil, err
	}
	return command.ResponseJSON, nil
}
