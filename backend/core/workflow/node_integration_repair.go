package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

var errRepairStaleDraft = errors.New("repair stale draft")

// Scope and preservation are independent: both flow and full repairs keep the
// user's current graph after conversion, including after a page reload.
func shouldPreserveCurrentWorkflow(draft orm.WorkflowDraft, target string, edit *algo.WorkflowEditContext) bool {
	if target == "node_integration" { // Persisted jobs and older clients.
		return true
	}
	return (target == "statemachine" || target == "full") &&
		((draft.SourceType == "skill" && draft.GenerateStatus == generateStatusDone) || (edit != nil && len(edit.NodeIDs) > 0))
}

func isCurrentWorkflowRepair(payload workflowDraftRepairPayload) bool {
	return payload.PreserveCurrentWorkflow || payload.Target == "node_integration"
}

// Call inside a transaction. Admission holds the same draft lock and retires
// previous active runs before creating the new owner. Terminal runs cannot own
// the draft again, even when another repair uses the same content version.
func lockWorkflowRepairOwner(tx *gorm.DB, payload workflowDraftRepairPayload) (orm.WorkflowDraft, bool, error) {
	var draft orm.WorkflowDraft
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleted_at IS NULL", payload.DraftID).First(&draft).Error; err != nil {
		return draft, false, err
	}
	var run orm.WorkflowRepairRun
	if err := tx.Where("id = ? AND draft_id = ?", payload.RepairRunID, payload.DraftID).First(&run).Error; err != nil {
		return draft, false, err
	}
	return draft, draft.GenerateStatus == generateStatusRepairing && (run.Status == "queued" || run.Status == "repairing"), nil
}

func validateNodeIntegrationContext(draft orm.WorkflowDraft, edit *algo.WorkflowEditContext, hint string) error {
	compiled := graphengine.Compile(draft.WorkflowYAMLContent, draft.StateYAMLContent, draft.ScenarioContent, graphengine.ProfileEditor)
	if compiled.Graph == nil {
		return fmt.Errorf("请先修正 YAML 格式，再接入新增节点")
	}
	if edit == nil {
		return nil
	}
	for _, id := range edit.NodeIDs {
		node, ok := compiled.Graph.Nodes[id]
		if !ok {
			return fmt.Errorf("节点 %s 不存在，请刷新草稿后重试", id)
		}
		if strings.TrimSpace(hint) != "" {
			continue
		}
		linked := false
		for _, edge := range compiled.Graph.ControlEdges {
			if edge.From == id || edge.To == id {
				linked = true
				break
			}
		}
		if !linked && node.Prompt == "" && (node.Label == "" || node.Label == "新步骤" || node.Label == "New Step" || node.Label == "New step") {
			return fmt.Errorf("请说明节点 %s 的用途和接入位置，或先配置节点并连接前后步骤", id)
		}
	}
	return nil
}

// Validate the complete candidate and the user-owned contracts separately.
// A structurally valid graph that drops the requested node is not a repair.
func diagnoseNodeIntegration(base, candidate workflowRepairSnapshot) []repairDiagnostic {
	scripts, _ := json.Marshal(candidate.Scripts)
	out := diagnoseWorkflowWithProfile(candidate.WorkflowYAML, candidate.StateYAML, candidate.ScenarioMD, string(scripts), graphengine.ProfilePublish)
	before := graphengine.Compile(base.WorkflowYAML, base.StateYAML, base.ScenarioMD, graphengine.ProfileEditor).Graph
	after := graphengine.Compile(candidate.WorkflowYAML, candidate.StateYAML, candidate.ScenarioMD, graphengine.ProfilePublish).Graph
	if before == nil || after == nil {
		return out
	}
	add := func(code, id, message string) {
		out = append(out, repairDiagnostic{Code: code, Path: "scenario/state.yml.steps." + id, NodeID: id, Message: message, Severity: "error", Fixable: true})
	}
	ids := make([]string, 0, len(before.Nodes))
	for id := range before.Nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		node := before.Nodes[id]
		next, exists := after.Nodes[id]
		if !exists {
			add("E_REPAIR_NODE_REMOVED", id, "修复删除了用户步骤，请保留该节点并接入流程")
			continue
		}
		if node.Mode != next.Mode {
			add("E_REPAIR_MODE_CHANGED", id, "修复改变了节点执行方式，请保留用户设置")
		}
		if !preservesRequiredInputs(node.Input, next.Input) {
			add("E_REPAIR_INPUT_CHANGED", id, "修复改变了必填输入约束，请修复输入来源而不是放宽要求")
		}
		if !reflect.DeepEqual(node.SkipIf, next.SkipIf) {
			add("E_REPAIR_SKIP_CHANGED", id, "修复改变了跳过条件，不能通过跳过步骤完成接入")
		}
		for _, output := range node.Outputs {
			if !slices.Contains(next.Outputs, output) || (slices.Contains(node.RequiredOutputs, output) && !slices.Contains(next.RequiredOutputs, output)) {
				add("E_REPAIR_OUTPUT_REMOVED", id, "修复删除或放宽了已有输出要求："+output)
			}
		}
	}
	branchSources := map[string]bool{}
	for _, edge := range before.ControlEdges {
		if edge.When != "" {
			branchSources[edge.From] = true
		}
	}
	branchTargets := map[string]bool{}
	for _, edge := range before.ControlEdges {
		if branchSources[edge.From] {
			branchTargets[edge.To] = true
		}
	}
	for _, edge := range before.ControlEdges {
		if branchSources[edge.From] && !slices.ContainsFunc(after.ControlEdges, func(next graphengine.CompiledEdge) bool {
			return next.From == edge.From && next.When == edge.When && preservesBranchDestination(next.To, edge.To, after.ControlEdges, branchTargets)
		}) {
			add("E_REPAIR_BRANCH_CHANGED", edge.From, "修复改变了已有分支条件或去向："+edge.When+" → "+edge.To)
		}
	}
	for id := range before.MaterialTypes {
		producer := before.MaterialProducers[id]
		if next, ok := after.MaterialProducers[id]; ok && producer.Kind != "external" && next.Kind == "external" {
			out = append(out, repairDiagnostic{Code: "E_REPAIR_EXTERNALIZED_MATERIAL", Path: "workflow.yaml.slots", MaterialID: id, Severity: "error", Message: "不能把流程内部产物改成外部输入：" + id, Fixable: true})
		}
	}
	if extractWorkflowID(base.WorkflowYAML) != extractWorkflowID(candidate.WorkflowYAML) {
		out = append(out, repairDiagnostic{Code: "E_REPAIR_ID_CHANGED", Path: "workflow.yaml.id", Severity: "error", Message: "修复不能改变工作流标识", Fixable: true})
	}
	if err := validateGeneratedScenarioContent(candidate.ScenarioMD, candidate.StateYAML); err != nil {
		out = append(out, repairDiagnostic{Code: "E_REPAIR_SCENARIO_INCOMPLETE", Path: "scenario/scenario.md", Severity: "error", Message: err.Error(), Fixable: true})
	}
	for i := range out {
		if out[i].Code == "W_TOOL_SCRIPT_MISSING" || out[i].Code == "W_SCENARIO_STEP_MISSING" {
			out[i].Severity = "error"
		}
	}
	return out
}

// A branch may be subdivided by inserted steps, but must still reach its
// original destination without passing through another branch destination or
// introducing a new routing decision. Mere downstream reachability is not
// enough: success and failure branches may eventually merge.
func preservesBranchDestination(from, target string, edges []graphengine.CompiledEdge, branchTargets map[string]bool) bool {
	visited := map[string]bool{}
	for from != target {
		if visited[from] || branchTargets[from] {
			return false
		}
		visited[from] = true
		var outgoing []graphengine.CompiledEdge
		for _, edge := range edges {
			if edge.From == from {
				outgoing = append(outgoing, edge)
			}
		}
		if len(outgoing) != 1 || outgoing[0].When != "" || outgoing[0].Condition != nil || outgoing[0].Legacy != "" {
			return false
		}
		from = outgoing[0].To
	}
	return true
}

// Inputs use AND(required groups), where each group is a material or OR of
// alternatives. New dependencies may be added, but every old requirement must
// still be implied by a candidate group (an equal or smaller alternative set).
func preservesRequiredInputs(before, after *graphengine.Expression) bool {
	if before == nil {
		return true
	}
	if after == nil {
		return false
	}
	groups := func(expr *graphengine.Expression) []graphengine.Expression {
		if len(expr.All) > 0 {
			return expr.All
		}
		return []graphengine.Expression{*expr}
	}
	for _, oldGroup := range groups(before) {
		oldMaterials := graphengine.Materials(&oldGroup)
		preserved := false
		for _, nextGroup := range groups(after) {
			materials := graphengine.Materials(&nextGroup)
			if len(materials) > 0 && !slices.ContainsFunc(materials, func(id string) bool { return !slices.Contains(oldMaterials, id) }) {
				preserved = true
				break
			}
		}
		if !preserved {
			return false
		}
	}
	return true
}

func handleNodeIntegrationRepair(ctx context.Context, db *gorm.DB, draft orm.WorkflowDraft, payload workflowDraftRepairPayload, llmConfig map[string]any) (asyncjob.Result, error) {
	base := workflowRepairSnapshot{WorkflowYAML: draft.WorkflowYAMLContent, StateYAML: draft.StateYAMLContent, ScenarioMD: draft.ScenarioContent, Scripts: scriptsFromDraft(draft.ScriptsContent)}
	resp, callErr := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
		WorkflowYAML: base.WorkflowYAML, StateYAML: base.StateYAML, ScenarioMD: base.ScenarioMD, Scripts: base.Scripts,
		RepairHint: payload.RepairHint, Diagnostics: payload.Diagnostics, Target: payload.Target, EditContext: payload.EditContext, LLMConfig: llmConfig,
		PreserveCurrentWorkflow: true,
	})
	var diagnostics []repairDiagnostic
	candidate := base
	if callErr != nil {
		diagnostics = []repairDiagnostic{{Code: "E_REPAIR_MODEL_FAILED", Severity: "error", Message: "AI 调用失败，本次没有应用修改。请稍后重新修复。"}}
	} else if strings.TrimSpace(resp.NeedsInput) != "" {
		diagnostics = []repairDiagnostic{{Code: "E_REPAIR_NEEDS_INPUT", Severity: "error", Message: resp.NeedsInput}}
	} else {
		candidate = mergeWorkflowRepairResponse(base, resp)
		diagnostics = diagnoseNodeIntegration(base, candidate)
		for _, issue := range resp.ScriptErrors {
			diagnostics = append(diagnostics, repairDiagnostic{Code: "E_REPAIR_SCRIPT_INVALID", Path: issue.Path, Message: issue.Message, Severity: "error", Fixable: true})
		}
	}
	status, errorCode := "succeeded", ""
	updates := map[string]any{"generate_status": payload.PrevStatus, "updated_at": time.Now().UTC()}
	changes := []string{}
	if hasDiagnosticErrors(diagnostics) {
		status, errorCode = "failed", "repair_validation_failed"
		if callErr != nil {
			errorCode = generateErrAlgoFailed
		}
		messages := []string{}
		for _, item := range diagnostics {
			if item.Severity == "error" {
				messages = append(messages, item.Message)
			}
		}
		updates["generate_warning"] = "[修复失败] " + strings.Join(messages, "; ")
	} else {
		// The entire candidate passed publish-profile validation. Restoring a
		// previous generation failure here would leave a repaired draft unpublishable.
		updates["generate_status"], updates["generate_error"] = generateStatusDone, ""
		scripts, _ := json.Marshal(candidate.Scripts)
		setWorkflowYAMLUpdate(updates, candidate.WorkflowYAML)
		updates["state_yaml_content"], updates["scenario_content"], updates["scripts_content"] = candidate.StateYAML, candidate.ScenarioMD, string(scripts)
		updates["version"], updates["generate_warning"] = draft.Version+1, strings.Join(resp.RemainingWarnings, "; ")
		if candidate.WorkflowYAML != base.WorkflowYAML {
			changes = append(changes, "workflow.yaml")
		}
		if candidate.StateYAML != base.StateYAML {
			changes = append(changes, "scenario/state.yml")
		}
		if candidate.ScenarioMD != base.ScenarioMD {
			changes = append(changes, "scenario/scenario.md")
		}
		for path, content := range candidate.Scripts {
			if old, ok := base.Scripts[path]; !ok || old != content {
				changes = append(changes, path)
			}
		}
		slices.Sort(changes)
	}
	changeJSON, _ := json.Marshal(map[string]any{"files": changes})
	err := db.Transaction(func(tx *gorm.DB) error {
		current, owns, err := lockWorkflowRepairOwner(tx, payload)
		if err != nil {
			return err
		}
		if !owns || current.Version != payload.DraftVersion {
			return errRepairStaleDraft
		}
		result := tx.Model(&orm.WorkflowDraft{}).Where("id = ? AND version = ? AND generate_status = ? AND deleted_at IS NULL", draft.ID, payload.DraftVersion, generateStatusRepairing).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errRepairStaleDraft
		}
		if status == "succeeded" && !reflect.DeepEqual(base.Scripts, candidate.Scripts) {
			if err := updateWorkflowGenerationScriptAudit(tx, draft.ID, draft.SourceAnalysisID, "", candidate.Scripts); err != nil {
				return err
			}
		}
		return tx.Model(&orm.WorkflowRepairRun{}).Where("id = ?", payload.RepairRunID).Updates(map[string]any{"status": status, "diagnostics_after_json": diagnosticsJSON(diagnostics), "changes_json": string(changeJSON), "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		failureStatus, code, message := "failed", "E_REPAIR_COMMIT_FAILED", "保存修复结果失败，本次没有应用修改。请重新修复。"
		if errors.Is(err, errRepairStaleDraft) {
			failureStatus, code, message = "stale", "E_REPAIR_STALE_DRAFT", "草稿已更新，本次修复未应用。请基于最新草稿重新修复。"
		}
		finishErr := finishNodeIntegrationFailure(db, payload, failureStatus, code, message)
		return asyncjob.Result{ErrorCode: code}, errors.Join(err, finishErr)
	}
	if status == "failed" {
		return asyncjob.Result{ErrorCode: errorCode}, fmt.Errorf("node integration repair failed: %s", updates["generate_warning"])
	}
	return asyncjob.Result{}, nil
}

func finishNodeIntegrationFailure(db *gorm.DB, payload workflowDraftRepairPayload, status, code, message string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		current, owns, err := lockWorkflowRepairOwner(tx, payload)
		if err != nil {
			return err
		}
		if owns {
			if err := tx.Model(&current).Updates(map[string]any{"generate_status": payload.PrevStatus, "generate_warning": "[修复失败] " + message, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&orm.WorkflowRepairRun{}).Where("id = ? AND status IN ?", payload.RepairRunID, []string{"queued", "repairing"}).Updates(map[string]any{"status": status, "diagnostics_after_json": diagnosticsJSON([]repairDiagnostic{{Code: code, Severity: "error", Message: message}}), "updated_at": time.Now().UTC()}).Error
	})
}
