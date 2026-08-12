package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func GetWorkflowGenerationAnalysis(w http.ResponseWriter, r *http.Request) {
	draftID, userID := common.PathVar(r, "draft_id"), common.UserID(r)
	var row orm.WorkflowGenerationAnalysis
	if err := store.DB().Where("draft_id = ? AND user_id = ?", draftID, userID).Order("created_at DESC").First(&row).Error; err != nil {
		common.ReplyErr(w, "generation analysis not found", http.StatusNotFound)
		return
	}
	var candidates []map[string]any
	var coverage, tools, scripts any
	_ = json.Unmarshal([]byte(row.CandidatesJSON), &candidates)
	candidates = normalizeWorkflowCandidates(candidates)
	_ = json.Unmarshal([]byte(row.CoverageReportJSON), &coverage)
	_ = json.Unmarshal([]byte(row.ToolMappingReportJSON), &tools)
	_ = json.Unmarshal([]byte(row.ScriptReportJSON), &scripts)
	common.ReplyOK(w, map[string]any{"analysis_id": row.ID, "status": row.Status, "verdict_code": row.VerdictCode, "message": row.VerdictMessage, "source_skill_revision_id": row.SourceSkillRevisionID, "source_skill_revision_no": row.SourceSkillRevisionNo, "source_skill_tree_hash": row.SourceSkillTreeHash, "candidates": candidates, "selected_candidate_id": row.SelectedCandidateID, "coverage": coverage, "tool_mappings": tools, "scripts": scripts})
}

func GetWorkflowRepairRun(w http.ResponseWriter, r *http.Request) {
	var row orm.WorkflowRepairRun
	if store.DB().Where("id=? AND draft_id=? AND user_id=?", common.PathVar(r, "repair_id"), common.PathVar(r, "draft_id"), common.UserID(r)).First(&row).Error != nil {
		common.ReplyErr(w, "repair run not found", http.StatusNotFound)
		return
	}
	var before, after, changes any
	_ = json.Unmarshal([]byte(row.DiagnosticsBeforeJSON), &before)
	_ = json.Unmarshal([]byte(row.DiagnosticsAfterJSON), &after)
	_ = json.Unmarshal([]byte(row.ChangesJSON), &changes)
	common.ReplyOK(w, map[string]any{"repair_id": row.ID, "status": row.Status, "target": row.Target, "mode": row.Mode, "draft_version_before": row.DraftVersionBefore, "source_skill_revision_id": row.SourceSkillRevisionID, "diagnostics_before": before, "diagnostics_after": after, "changes": changes, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt})
}

func PreviewWorkflowRepair(w http.ResponseWriter, r *http.Request) {
	var draft orm.WorkflowDraft
	if store.DB().Where("id=? AND created_by=?", common.PathVar(r, "draft_id"), common.UserID(r)).First(&draft).Error != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	var body struct {
		Target string `json:"target"`
		Mode   string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Target == "" {
		body.Target = "statemachine"
	}
	if body.Mode == "" {
		body.Mode = "workflow_local"
	}
	diagnostics := diagnosticsForTarget(
		diagnoseWorkflow(draft.WorkflowYAMLContent, draft.StateYAMLContent, draft.ScenarioContent, draft.ScriptsContent),
		body.Target,
	)
	if diagnostics == nil {
		diagnostics = []repairDiagnostic{}
	}
	files := map[string][]string{
		"statemachine": {"workflow.yaml.steps", "workflow.yaml.slots", "scenario/state.yml"},
		"ui":           {"workflow.yaml.ui", "workflow.yaml.slots"},
		"scenario":     {"scenario/scenario.md"},
		"scripts":      {"scripts/*", "workflow.yaml.tools", "scenario/state.yml.steps"},
		"full":         {"workflow.yaml", "scenario/state.yml", "scenario/scenario.md", "scripts/*"},
	}
	common.ReplyOK(w, map[string]any{"target": body.Target, "mode": body.Mode, "draft_version": draft.Version, "diagnostics": diagnostics, "planned_files": files[body.Target]})
}

func ConfirmWorkflowWorkflow(w http.ResponseWriter, r *http.Request) {
	draftID, userID := common.PathVar(r, "draft_id"), common.UserID(r)
	var body struct {
		AnalysisID            string `json:"analysis_id"`
		CandidateID           string `json:"candidate_id"`
		SourceSkillRevisionID string `json:"source_skill_revision_id"`
		DraftVersion          int    `json:"draft_version"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.AnalysisID == "" || body.CandidateID == "" {
		common.ReplyErr(w, "analysis_id and candidate_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	var draft orm.WorkflowDraft
	if db.Where("id=? AND created_by=?", draftID, userID).First(&draft).Error != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	if draft.Version != body.DraftVersion {
		common.ReplyErr(w, "workflow confirmation stale", http.StatusConflict)
		return
	}
	var analysis orm.WorkflowGenerationAnalysis
	if db.Where("id=? AND draft_id=? AND user_id=?", body.AnalysisID, draftID, userID).First(&analysis).Error != nil {
		common.ReplyErr(w, "generation analysis not found", http.StatusNotFound)
		return
	}
	if analysis.Status != "needs_confirmation" || analysis.SourceSkillRevisionID != body.SourceSkillRevisionID {
		common.ReplyErr(w, "workflow confirmation stale", http.StatusConflict)
		return
	}
	var candidates []map[string]any
	_ = json.Unmarshal([]byte(analysis.CandidatesJSON), &candidates)
	candidates = normalizeWorkflowCandidates(candidates)
	var selected map[string]any
	for _, candidate := range candidates {
		if id, _ := candidate["id"].(string); id == body.CandidateID {
			selected = candidate
			break
		}
	}
	if selected == nil {
		common.ReplyErr(w, "candidate not found", http.StatusBadRequest)
		return
	}
	var toolMappings, scriptReport any
	_ = json.Unmarshal([]byte(analysis.ToolMappingReportJSON), &toolMappings)
	_ = json.Unmarshal([]byte(analysis.ScriptReportJSON), &scriptReport)
	selectedJSON, _ := json.Marshal(map[string]any{"candidate": selected, "tool_mappings": toolMappings, "scripts": scriptReport})
	var skillPackage map[string]any
	if strings.HasPrefix(analysis.SourceSkillID, builtinSkillIDPrefix) {
		snapshot, loadErr := loadWorkflowBuiltinSkillPackage(analysis.SourceSkillID)
		if loadErr != nil || snapshot.TreeHash != analysis.SourceSkillTreeHash {
			common.ReplyErr(w, "workflow confirmation stale", http.StatusConflict)
			return
		}
		b, _ := json.Marshal(snapshot)
		_ = json.Unmarshal(b, &skillPackage)
	} else {
		snapshot, loadErr := loadWorkflowSourceSkillRevision(r.Context(), db, userID, analysis.SourceSkillID, analysis.SourceSkillRevisionID)
		if loadErr != nil || snapshot.TreeHash != analysis.SourceSkillTreeHash {
			common.ReplyErr(w, "workflow confirmation stale", http.StatusConflict)
			return
		}
		b, _ := json.Marshal(snapshot)
		_ = json.Unmarshal(b, &skillPackage)
	}
	now := time.Now().UTC()
	if err := db.Model(&analysis).Updates(map[string]any{"selected_candidate_id": body.CandidateID, "status": "generatable", "updated_at": now}).Error; err != nil {
		common.ReplyErr(w, "update failed", http.StatusInternalServerError)
		return
	}
	if err := db.Model(&draft).Updates(map[string]any{"generate_status": generateStatusGenerating, "updated_at": now}).Error; err != nil {
		common.ReplyErr(w, "update failed", http.StatusInternalServerError)
		return
	}
	_, err := asyncjob.Enqueue(r.Context(), db, asyncjob.EnqueueRequest{JobType: workflowDraftGenerateJobType, ResourceType: "workflow_draft", ResourceID: draftID, Payload: workflowDraftGeneratePayload{DraftID: draftID, Name: draft.Name, UserID: userID, SkillContent: skillPackageSkillMD(skillPackage), SkillPackage: skillPackage, SourceSkillRevisionID: analysis.SourceSkillRevisionID, SelectedCandidateJSON: string(selectedJSON), ReusableScripts: reusableSkillScriptsJSON(skillPackage, analysis.ScriptReportJSON)}, MaxAttempts: 1, CreateUserID: userID})
	if err != nil {
		common.ReplyErr(w, "enqueue failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"analysis_id": analysis.ID, "candidate_id": body.CandidateID, "generate_status": generateStatusGenerating})
}

func reusableSkillScriptsJSON(pkg map[string]any, reportJSON string) map[string]string {
	var report map[string]any
	_ = json.Unmarshal([]byte(reportJSON), &report)
	return reusableSkillScripts(pkg, report)
}

func cachedAnalysisContext(analysis orm.WorkflowGenerationAnalysis) string {
	var candidates []map[string]any
	var mappings, scripts any
	_ = json.Unmarshal([]byte(analysis.CandidatesJSON), &candidates)
	candidates = normalizeWorkflowCandidates(candidates)
	_ = json.Unmarshal([]byte(analysis.ToolMappingReportJSON), &mappings)
	_ = json.Unmarshal([]byte(analysis.ScriptReportJSON), &scripts)
	var selected map[string]any
	for _, candidate := range candidates {
		if id, _ := candidate["id"].(string); id == analysis.SelectedCandidateID {
			selected = candidate
			break
		}
	}
	if selected == nil && len(candidates) > 0 {
		selected = candidates[0]
	}
	b, _ := json.Marshal(map[string]any{"candidate": selected, "tool_mappings": mappings, "scripts": scripts})
	return string(b)
}

// normalizeWorkflowCandidates keeps the confirmation contract usable when an
// LLM returns a candidate object without the requested id field. The generated
// id is stable for persisted candidate order, so this also repairs analyses
// that were stored before the prompt required candidate ids explicitly.
func normalizeWorkflowCandidates(candidates []map[string]any) []map[string]any {
	used := make(map[string]struct{}, len(candidates))
	for index, candidate := range candidates {
		id, _ := candidate["id"].(string)
		id = strings.TrimSpace(id)
		if _, duplicate := used[id]; id == "" || duplicate {
			id = fmt.Sprintf("candidate-%d", index+1)
			for suffix := 2; ; suffix++ {
				if _, exists := used[id]; !exists {
					break
				}
				id = fmt.Sprintf("candidate-%d-%d", index+1, suffix)
			}
		}
		candidate["id"] = id
		used[id] = struct{}{}
	}
	return candidates
}

func ignoredScriptWarningJSON(reportJSON string) string {
	var report map[string]any
	_ = json.Unmarshal([]byte(reportJSON), &report)
	return ignoredScriptWarning(report)
}

func reusableSkillScripts(pkg map[string]any, report map[string]any) map[string]string {
	out := map[string]string{}
	files, _ := pkg["files"].([]any)
	for _, raw := range files {
		file, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path, _ := file["path"].(string)
		item, _ := report[path].(map[string]any)
		classification, _ := item["classification"].(string)
		if classification != "importable_tool" && classification != "wrappable_command" {
			continue
		}
		if content, ok := file["content"].(string); ok {
			out[path] = content
		}
	}
	return out
}

func skillPackageSkillMD(pkg map[string]any) string {
	files, _ := pkg["files"].([]any)
	for _, raw := range files {
		if f, ok := raw.(map[string]any); ok && f["path"] == "SKILL.md" {
			if s, ok := f["content"].(string); ok {
				return s
			}
		}
	}
	return ""
}
