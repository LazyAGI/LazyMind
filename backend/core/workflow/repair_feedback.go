package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

type workflowRepairFeedback struct {
	BaseHash    string                 `json:"base_hash"`
	Candidate   workflowRepairSnapshot `json:"candidate"`
	Diagnostics []repairDiagnostic     `json:"diagnostics"`
}

type workflowRepairFileChange struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type workflowRepairAttempt struct {
	Diagnostics []repairDiagnostic         `json:"diagnostics"`
	Changes     []workflowRepairFileChange `json:"changes"`
}

type workflowRepairChanges struct {
	Files    []string                `json:"files"`
	Attempts []workflowRepairAttempt `json:"attempts,omitempty"`
	Feedback *workflowRepairFeedback `json:"repair_feedback,omitempty"`
}

func workflowRepairHash(snapshot workflowRepairSnapshot) string {
	body, _ := json.Marshal(snapshot)
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func workflowRepairDiff(base, candidate workflowRepairSnapshot) []workflowRepairFileChange {
	before := map[string]string{"workflow.yaml": base.WorkflowYAML, "scenario/state.yml": base.StateYAML, "scenario/scenario.md": base.ScenarioMD}
	after := map[string]string{"workflow.yaml": candidate.WorkflowYAML, "scenario/state.yml": candidate.StateYAML, "scenario/scenario.md": candidate.ScenarioMD}
	for path, content := range base.Scripts {
		before[path] = content
	}
	for path, content := range candidate.Scripts {
		after[path] = content
	}
	paths := make([]string, 0, len(after))
	for path := range after {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	changes := []workflowRepairFileChange{}
	for _, path := range paths {
		if before[path] != after[path] {
			changes = append(changes, workflowRepairFileChange{path, before[path], after[path]})
		}
	}
	return changes
}

// Failed candidates are context only. The caller always starts from the saved
// draft, and all guards compare against that same immutable original snapshot.
func previousWorkflowRepairFeedback(db *gorm.DB, payload workflowDraftRepairPayload, base workflowRepairSnapshot) (*workflowRepairFeedback, error) {
	var rows []orm.WorkflowRepairRun
	if err := db.Where("draft_id = ? AND user_id = ? AND draft_version_before = ? AND status = ? AND id <> ? AND target IN ?",
		payload.DraftID, payload.UserID, payload.DraftVersion, "failed", payload.RepairRunID, []string{payload.Target, "node_integration"}).Order("created_at DESC").Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	var changes workflowRepairChanges
	if err := json.Unmarshal([]byte(rows[0].ChangesJSON), &changes); err != nil {
		return nil, fmt.Errorf("decode previous repair changes: %w", err)
	}
	if changes.Feedback == nil || changes.Feedback.BaseHash != workflowRepairHash(base) {
		return nil, nil
	}
	return changes.Feedback, nil
}

func currentWorkflowRepairContext(base workflowRepairSnapshot, edit *algo.WorkflowEditContext) map[string]any {
	graph := graphengine.Compile(base.WorkflowYAML, base.StateYAML, base.ScenarioMD, graphengine.ProfileEditor).Graph
	context := map[string]any{"original_files": base, "input_corrections": []algo.WorkflowInputCorrection{}}
	if edit != nil {
		context["input_corrections"] = edit.InputCorrections
	}
	if graph != nil {
		context["protected_transitions"] = graph.ControlEdges
	}
	return context
}

func generateCurrentWorkflowCandidate(ctx context.Context, db *gorm.DB, base workflowRepairSnapshot, payload workflowDraftRepairPayload, llmConfig map[string]any) (workflowRepairSnapshot, *algo.RepairStateMachineResponse, []repairDiagnostic, []workflowRepairAttempt, error) {
	candidate := cloneWorkflowRepairSnapshot(base)
	feedback, err := previousWorkflowRepairFeedback(db.WithContext(ctx), payload, base)
	if err != nil {
		return candidate, nil, nil, nil, err
	}
	repairContext := currentWorkflowRepairContext(base, payload.EditContext)
	if feedback != nil {
		repairContext["previous_failure"] = feedback
	}
	diagnosticsPayload := payload.Diagnostics
	attempts := []workflowRepairAttempt{}
	var response *algo.RepairStateMachineResponse
	var diagnostics []repairDiagnostic
	for attempt := 0; attempt < 2; attempt++ {
		// Cancellation, supersession and concurrent edits must stop before another
		// paid request, not just reject its eventual result at commit time.
		if err := ctx.Err(); err != nil {
			return candidate, response, diagnostics, attempts, err
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			draft, owns, err := lockWorkflowRepairOwner(tx, payload)
			if err != nil {
				return err
			}
			if !owns || draft.Version != payload.DraftVersion {
				return errRepairStaleDraft
			}
			return nil
		}); err != nil {
			return candidate, response, diagnostics, attempts, err
		}
		input := cloneWorkflowRepairSnapshot(candidate)
		maxCalls := 2
		if attempt > 0 {
			maxCalls = 1
		}
		response, err = algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
			WorkflowYAML: input.WorkflowYAML, StateYAML: input.StateYAML, ScenarioMD: input.ScenarioMD, Scripts: input.Scripts,
			RepairHint: payload.RepairHint, Diagnostics: diagnosticsPayload, Target: payload.Target, EditContext: payload.EditContext,
			LLMConfig: llmConfig, PreserveCurrentWorkflow: true, RepairContext: repairContext, MaxModelCalls: maxCalls,
		})
		if err != nil {
			return candidate, response, diagnostics, attempts, err
		}
		if strings.TrimSpace(response.NeedsInput) != "" {
			diagnostics = []repairDiagnostic{{Code: "E_REPAIR_NEEDS_INPUT", Severity: "error", Message: response.NeedsInput}}
			return candidate, response, diagnostics, attempts, nil
		}
		candidate = mergeWorkflowRepairResponse(input, response)
		diagnostics = diagnoseAuthorizedNodeIntegration(base, candidate, payload.EditContext)
		for _, issue := range response.ScriptErrors {
			diagnostics = append(diagnostics, repairDiagnostic{Code: "E_REPAIR_SCRIPT_INVALID", Path: issue.Path, Message: issue.Message, Severity: "error", Fixable: true})
		}
		attempts = append(attempts, workflowRepairAttempt{Diagnostics: diagnostics, Changes: workflowRepairDiff(base, candidate)})
		if !hasDiagnosticErrors(diagnostics) || reflect.DeepEqual(input, candidate) {
			break
		}
		// The supplied files are now the rejected candidate, but original_files
		// and protected_transitions remain unchanged for every correction.
		repairContext["previous_failure"] = workflowRepairFeedback{workflowRepairHash(base), candidate, diagnostics}
		diagnosticsPayload = repairDiagnosticsPayload(diagnostics)
	}
	return candidate, response, diagnostics, attempts, nil
}

func repairFeedbackError(err error) []repairDiagnostic {
	if errors.Is(err, errRepairStaleDraft) {
		return []repairDiagnostic{{Code: "E_REPAIR_STALE_DRAFT", Severity: "error", Message: "草稿或修复任务已更新，本次修复未应用。"}}
	}
	return []repairDiagnostic{workflowRepairCallDiagnostic(err)}
}
