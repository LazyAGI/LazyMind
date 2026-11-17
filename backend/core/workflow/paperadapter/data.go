package paperadapter

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflowtool"
)

// PaperData is an internal, model-free adapter to the existing selected-knowledge
// service. Both native and external paper tools use this exact function.
func (h *Adapter) Data(w http.ResponseWriter, r *http.Request) {
	expected := os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")
	supplied := r.Header.Get("X-LazyMind-Internal-Token")
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) != 1 {
		reply(w, http.StatusUnauthorized, nil, &workflowtool.ToolError{Code: "IDENTITY_REQUIRED", Message: "Internal service identity required"})
		return
	}
	var input struct {
		ToolName  string         `json:"tool_name"`
		Arguments map[string]any `json:"arguments"`
		LeaseHash string         `json:"lease_hash"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || (input.ToolName != "knowledge_list_documents" && input.ToolName != "knowledge_read_document" && input.ToolName != "execution_status") {
		reply(w, http.StatusUnprocessableEntity, nil, &workflowtool.ToolError{Code: "INVALID_ARGUMENT", Message: "Invalid paper data request"})
		return
	}
	attemptID := mux.Vars(r)["attempt_id"]
	var row orm.WorkflowSessionStep
	if err := h.DB.WithContext(r.Context()).Where("id = ?", attemptID).First(&row).Error; err != nil ||
		row.Validity != "effective" || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(time.Now()) ||
		(row.Status != "running" && row.Status != "claimed") {
		reply(w, http.StatusConflict, nil, &workflowtool.ToolError{Code: "EXECUTION_FENCED", Message: "Paper execution is no longer active"})
		return
	}
	if input.LeaseHash == "" || input.LeaseHash != fmt.Sprintf("%x", sha256.Sum256([]byte(row.LeaseToken))) {
		reply(w, http.StatusConflict, nil, &workflowtool.ToolError{Code: "EXECUTION_FENCED", Message: "Paper execution was re-claimed"})
		return
	}
	contract, err := (executor.DBContextLoader{DB: h.DB}).LoadAttemptContext(r.Context(), row.ID)
	if err != nil || !executor.UsesPaperAgentContract(contract) {
		reply(w, http.StatusForbidden, nil, &workflowtool.ToolError{Code: "PAPER_CONTRACT_REQUIRED", Message: "A pinned paper Agent contract is required"})
		return
	}
	if input.ToolName == "execution_status" {
		var session orm.WorkflowSession
		if err := h.DB.Where("id = ?", row.SessionID).First(&session).Error; err != nil || session.Status == "stopped" || session.Dismissed {
			reply(w, http.StatusConflict, nil, &workflowtool.ToolError{Code: "EXECUTION_FENCED", Message: "Paper run stopped"})
			return
		}
		reply(w, http.StatusOK, workflowtool.Result{Result: map[string]any{"active": true}}, nil)
		return
	}
	declared := "paper_knowledge_documents"
	if input.ToolName == "knowledge_read_document" {
		declared = "paper_knowledge_read"
	}
	allowed := false
	for _, tool := range contract.Execution.Tools {
		if tool.Name == declared {
			allowed = true
		}
	}
	if !allowed {
		reply(w, http.StatusForbidden, nil, &workflowtool.ToolError{Code: "TOOL_NOT_ALLOWED", Message: "Knowledge operation is not declared for this step"})
		return
	}
	var session orm.WorkflowSession
	if err := h.DB.Where("id = ?", row.SessionID).First(&session).Error; err != nil {
		serviceError(w, err)
		return
	}
	if session.Status == "stopped" || session.Dismissed {
		reply(w, http.StatusConflict, nil, &workflowtool.ToolError{Code: "EXECUTION_FENCED", Message: "Paper run is stopped"})
		return
	}
	result, err := h.ReadKnowledge(r.Context(), session.CreateUserID, contract, input.ToolName, input.Arguments)
	if err != nil {
		serviceError(w, err)
		return
	}
	// No text is delivered after stop/reclaim. The Python receipt is also bound
	// to the input snapshot and final submission rechecks that exact scope.
	var current orm.WorkflowSessionStep
	if err := h.DB.Where("id = ?", row.ID).First(&current).Error; err != nil || current.LeaseToken != row.LeaseToken ||
		current.Status != row.Status || current.Validity != "effective" || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(time.Now()) {
		reply(w, http.StatusConflict, nil, &workflowtool.ToolError{Code: "EXECUTION_FENCED", Message: "Paper execution changed during reading"})
		return
	}
	reply(w, http.StatusOK, result, nil)
}

// Reserve quota and the existing idempotency receipt together. Replays never
// spend another unit; concurrent calls cannot overrun the pinned paper budget.
func (s *Adapter) Reserve(ctx context.Context, contract executor.AttemptContext, input workflowtool.CallInput, row orm.WorkflowCommand) (bool, error) {
	created := false
	err := controlstore.Transaction(ctx, s.DB, row.SessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		created = false
		var existing orm.WorkflowCommand
		err := tx.Where("command_id = ?", row.CommandID).First(&existing).Error
		if err == nil {
			return nil
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		if contract.Execution.Limits == nil || contract.Execution.Limits.Rounds == 0 {
			return controlstore.Reject("PAPER_EXECUTION_LIMITS_REQUIRED", "Pinned paper execution limits are unavailable")
		}
		limit := contract.Execution.Limits.ToolCalls[input.ToolName]
		if limit > 0 {
			group := controlstore.Hash([]byte(contract.AttemptID + ":" + input.ToolName))
			var count int64
			if err := tx.Model(&orm.WorkflowCommand{}).Where("session_id = ? AND owner_user_id = ? AND contract_version = ? AND request_hash = ?", row.SessionID, row.OwnerUserID, "paper.tool-budget.v1", group).Count(&count).Error; err != nil {
				return err
			}
			if count >= int64(limit) {
				return controlstore.Reject("TOOL_BUDGET_EXHAUSTED", fmt.Sprintf("%s reached its pinned limit of %d calls", input.ToolName, limit))
			}
			marker := orm.WorkflowCommand{CommandID: "paper-budget:" + controlstore.Hash([]byte(row.CommandID)), OwnerUserID: row.OwnerUserID, SessionID: row.SessionID,
				ContractVersion: "paper.tool-budget.v1", RequestHash: group, HTTPStatus: http.StatusOK, ResponseJSON: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}
			if err := tx.Create(&marker).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}
