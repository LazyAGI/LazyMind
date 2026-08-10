package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
	"lazymind/core/workflow/graphengine"
)

type generatedWorkflowSkeleton struct {
	ID    string `yaml:"id"`
	Name  string `yaml:"name"`
	Slots []struct {
		ID string `yaml:"id"`
	} `yaml:"slots"`
	Steps []struct {
		ID string `yaml:"id"`
	} `yaml:"steps"`
}

func repairDiagnosticsPayload(items []repairDiagnostic) []map[string]any {
	payload := make([]map[string]any, 0, len(items))
	for _, item := range items {
		encoded, _ := json.Marshal(item)
		var diagnostic map[string]any
		_ = json.Unmarshal(encoded, &diagnostic)
		payload = append(payload, diagnostic)
	}
	return payload
}

const workflowDraftGenerateJobType = "workflow_draft_generate"
const workflowDraftRepairJobType = "workflow_draft_repair"

const (
	generateStatusGenerating   = "generating"
	generateStatusBriefDone    = "brief_done"
	generateStatusSkeletonDone = "skeleton_done"
	generateStatusStateDone    = "state_done"
	generateStatusDone         = "done"
	generateStatusFailed       = "failed"
	generateStatusRepairing    = "repairing"
	generateStatusAnalyzing    = "analyzing"
	generateStatusNeedsConfirm = "needs_confirmation"
	generateStatusRejected     = "rejected"
)

const (
	generateErrInvalidPayload = "invalid_payload"
	generateErrDraftNotFound  = "draft_not_found"
	generateErrAlgoFailed     = "algo_failed"
	generateErrSaveFailed     = "save_failed"
)

const (
	generatePhaseDesignBrief     = "design_brief"
	generatePhaseSkeleton        = "skeleton"
	generatePhaseStateMachine    = "state_machine"
	generatePhaseScenarioScripts = "scenario_scripts"
)

type workflowDraftGeneratePayload struct {
	DraftID               string            `json:"draft_id"`
	Name                  string            `json:"name"`
	Description           string            `json:"description,omitempty"`
	StartPhase            string            `json:"start_phase,omitempty"`
	SkillContent          string            `json:"skill_content,omitempty"`
	SkillPackage          map[string]any    `json:"skill_package,omitempty"`
	SourceSkillRevisionID string            `json:"source_skill_revision_id,omitempty"`
	SelectedCandidateJSON string            `json:"selected_candidate_json,omitempty"`
	ReusableScripts       map[string]string `json:"reusable_scripts,omitempty"`
	UserID                string            `json:"user_id"`
}

type workflowDraftRepairPayload struct {
	DraftID      string           `json:"draft_id"`
	UserID       string           `json:"user_id"`
	Target       string           `json:"target"`      // 'statemachine' | 'ui' | 'scenario'
	RepairHint   string           `json:"repair_hint"` // optional
	Warnings     []string         `json:"warnings,omitempty"`
	Diagnostics  []map[string]any `json:"diagnostics,omitempty"`
	PrevStatus   string           `json:"prev_status"`
	LLMConfig    map[string]any   `json:"llm_config,omitempty"`
	DraftVersion int              `json:"draft_version"`
	Mode         string           `json:"mode,omitempty"`
	RepairRunID  string           `json:"repair_run_id,omitempty"`
}

// RegisterWorkflowDraftGenerateJob registers the async job handler.
// Call this once at startup (e.g. from main.go).
func RegisterWorkflowDraftGenerateJob() {
	asyncjob.Register(workflowDraftGenerateJobType, handleWorkflowDraftGenerateJob)
	asyncjob.Register(workflowDraftRepairJobType, handleWorkflowDraftRepairJob)
}

func handleWorkflowDraftGenerateJob(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var payload workflowDraftGeneratePayload
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		return asyncjob.Result{ErrorCode: generateErrInvalidPayload}, fmt.Errorf("decode payload: %w", err)
	}

	db := store.DB()
	if db == nil {
		return asyncjob.Result{ErrorCode: generateErrDraftNotFound}, fmt.Errorf("store not initialised")
	}
	var draft orm.WorkflowDraft
	if err := db.WithContext(ctx).Where("id = ? AND created_by = ?", payload.DraftID, payload.UserID).First(&draft).Error; err != nil {
		return asyncjob.Result{ErrorCode: generateErrDraftNotFound}, fmt.Errorf("draft not found: %w", err)
	}
	startPhase := normalizeGenerateStartPhase(payload.StartPhase)
	if err := validateGenerateResumePoint(draft, startPhase); err != nil {
		_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("resume point invalid: %s", err))
		return asyncjob.Result{ErrorCode: "generation_resume_invalid"}, fmt.Errorf("resume point invalid: %w", err)
	}
	llmConfig, err := modelconfig.LoadLLMConfig(ctx, db, payload.UserID)
	if err != nil {
		llmConfig = map[string]any{}
	}

	if startPhase == generatePhaseDesignBrief && len(payload.SkillPackage) > 0 && payload.SelectedCandidateJSON == "" {
		analysisResp, analysisErr := algo.AnalyzeSkill(ctx, algo.AnalyzeSkillRequest{Name: draft.Name, SkillPackage: payload.SkillPackage, LLMConfig: llmConfig})
		if analysisErr != nil {
			_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("phase-1 analysis: %s", analysisErr))
			return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, analysisErr
		}
		analysisID := uuid.NewString()
		candidatesJSON, _ := json.Marshal(analysisResp.Candidates)
		coverageJSON, _ := json.Marshal(analysisResp.Coverage)
		toolsJSON, _ := json.Marshal(analysisResp.ToolMappings)
		scriptsJSON, _ := json.Marshal(analysisResp.Scripts)
		packageJSON, _ := json.Marshal(manifestOnlySkillPackage(payload.SkillPackage))
		now := time.Now().UTC()
		analysis := orm.WorkflowGenerationAnalysis{ID: analysisID, DraftID: draft.ID, UserID: payload.UserID, SourceType: "skill", SourceSkillID: draft.SourceSkillID, SourceSkillRevisionID: payload.SourceSkillRevisionID, SourceSkillRevisionNo: draft.SourceSkillRevisionNo, SourceSkillTreeHash: draft.SourceSkillTreeHash, Status: analysisResp.Verdict, VerdictCode: analysisResp.VerdictCode, VerdictMessage: analysisResp.Message, CandidatesJSON: string(candidatesJSON), CoverageReportJSON: string(coverageJSON), ToolMappingReportJSON: string(toolsJSON), ScriptReportJSON: string(scriptsJSON), SourcePackageJSON: string(packageJSON), CreatedAt: now, UpdatedAt: now}
		if analysisResp.Verdict == "generatable" && len(analysisResp.Candidates) > 0 {
			selected, _ := json.Marshal(map[string]any{"candidate": analysisResp.Candidates[0], "tool_mappings": analysisResp.ToolMappings, "scripts": analysisResp.Scripts})
			payload.SelectedCandidateJSON = string(selected)
			if id, ok := analysisResp.Candidates[0]["id"].(string); ok {
				analysis.SelectedCandidateID = id
			}
		}
		payload.ReusableScripts = reusableSkillScripts(payload.SkillPackage, analysisResp.Scripts)
		if err := db.WithContext(ctx).Create(&analysis).Error; err != nil {
			return asyncjob.Result{ErrorCode: generateErrSaveFailed}, err
		}
		status := generateStatusAnalyzing
		switch analysisResp.Verdict {
		case "needs_confirmation":
			status = generateStatusNeedsConfirm
		case "rejected":
			status = generateStatusRejected
		default:
			status = generateStatusGenerating
		}
		analysisUpdates := map[string]any{"source_analysis_id": analysisID, "generate_status": status, "generate_error": analysisResp.Message, "updated_at": now}
		if warning := ignoredScriptWarning(analysisResp.Scripts); warning != "" {
			analysisUpdates["generate_warning"] = warning
		}
		if err := db.WithContext(ctx).Model(&draft).Updates(analysisUpdates).Error; err != nil {
			return asyncjob.Result{ErrorCode: generateErrSaveFailed}, err
		}
		if status == generateStatusNeedsConfirm || status == generateStatusRejected {
			return asyncjob.Result{}, nil
		}
	}

	// ── Phase 0: Design Brief ────────────────────────────────────────────────
	// Generate a design brief (Markdown) that describes slots, steps, and flow.
	// Subsequent phases receive this brief as an authoritative reference so that
	// slot IDs remain consistent across phases.
	// On failure we fall back gracefully (brief stays empty) so existing drafts are unaffected.
	designBrief := draft.DesignBriefContent
	if shouldRunGeneratePhase(startPhase, generatePhaseDesignBrief) {
		briefResp, briefErr := algo.DesignBrief(ctx, algo.DesignBriefRequest{
			Name:             draft.Name,
			Description:      payload.Description,
			SkillContent:     payload.SkillContent,
			SkillPackage:     payload.SkillPackage,
			WorkflowAnalysis: payload.SelectedCandidateJSON,
			LLMConfig:        llmConfig,
		})
		if briefErr != nil {
			// Non-fatal: log and continue without a brief.
			_ = db.WithContext(ctx).Model(&orm.WorkflowDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
				"generate_warning": fmt.Sprintf("phase0 design_brief: %s", briefErr),
				"updated_at":       time.Now().UTC(),
			}).Error
		} else {
			designBrief = briefResp.DesignBrief
			if err := db.WithContext(ctx).Model(&orm.WorkflowDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
				"design_brief_content": designBrief,
				"generate_status":      generateStatusBriefDone,
				"updated_at":           time.Now().UTC(),
			}).Error; err != nil {
				return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save design_brief: %w", err)
			}
		}
	}
	// ── Phase 1: Skeleton ────────────────────────────────────────────────────
	skeletonResp := &algo.GenerateSkeletonResponse{WorkflowYAML: draft.WorkflowYAMLContent}
	if shouldRunGeneratePhase(startPhase, generatePhaseSkeleton) {
		var err error
		skeletonResp, err = algo.GenerateSkeleton(ctx, algo.GenerateSkeletonRequest{
			Name:             draft.Name,
			Description:      payload.Description,
			SkillContent:     payload.SkillContent,
			SkillPackage:     payload.SkillPackage,
			WorkflowAnalysis: payload.SelectedCandidateJSON,
			DesignBrief:      designBrief,
			LLMConfig:        llmConfig,
		})
		if err != nil {
			_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("phase1 skeleton: %s", err))
			return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("phase1 skeleton: %w", err)
		}
		if err := validateGeneratedWorkflowSkeleton(skeletonResp.WorkflowYAML); err != nil {
			_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("phase1 skeleton invalid: %s", err))
			return asyncjob.Result{ErrorCode: "generation_skeleton_invalid"}, fmt.Errorf("phase1 skeleton invalid: %w", err)
		}
		skeletonUpdates := map[string]any{
			"generate_status": generateStatusSkeletonDone,
			"updated_at":      time.Now().UTC(),
		}
		setWorkflowYAMLUpdate(skeletonUpdates, skeletonResp.WorkflowYAML)
		if err := db.WithContext(ctx).Model(&orm.WorkflowDraft{}).Where("id = ?", payload.DraftID).Updates(skeletonUpdates).Error; err != nil {
			return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save skeleton: %w", err)
		}
	}

	// ── Phase 2: State Machine ───────────────────────────────────────────────
	stateResp := &algo.GenerateStateMachineResponse{StateYAML: draft.StateYAMLContent}
	if shouldRunGeneratePhase(startPhase, generatePhaseStateMachine) {
		var err error
		stateResp, err = algo.GenerateStateMachine(ctx, algo.GenerateStateMachineRequest{
			Name:             draft.Name,
			WorkflowYAML:     skeletonResp.WorkflowYAML,
			DesignBrief:      designBrief,
			WorkflowAnalysis: payload.SelectedCandidateJSON,
			LLMConfig:        llmConfig,
		})
		if err != nil {
			_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("phase2 state_machine: %s", err))
			return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("phase2 state_machine: %w", err)
		}
	}
	// Use the (possibly slot-repaired) workflow_yaml returned by Phase 2.
	// Falls back to Phase 1 output when Phase 2 did not modify it.
	finalWorkflowYAML := skeletonResp.WorkflowYAML
	if stateResp.WorkflowYAML != "" {
		finalWorkflowYAML = stateResp.WorkflowYAML
	}
	if err := validateGeneratedWorkflowSkeleton(finalWorkflowYAML); err != nil {
		_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("phase2 workflow invalid: %s", err))
		return asyncjob.Result{ErrorCode: "generation_skeleton_invalid"}, fmt.Errorf("phase2 workflow invalid: %w", err)
	}
	stateDiagnostics := diagnoseWorkflowWithProfile(finalWorkflowYAML, stateResp.StateYAML, "", "{}", graphengine.ProfileGenerationPhase)
	if hasDiagnosticErrorsForTarget(stateDiagnostics, "statemachine") {
		repairResp, repairErr := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
			WorkflowYAML: finalWorkflowYAML,
			StateYAML:    stateResp.StateYAML,
			RepairHint:   "Fix the generated workflow.yaml and scenario/state.yml so the graph compiler has no errors. Return complete final YAML.",
			Diagnostics:  repairDiagnosticsPayload(stateDiagnostics),
			Target:       "statemachine",
			LLMConfig:    llmConfig,
		})
		if repairErr == nil {
			if repairResp.WorkflowYAML != "" {
				finalWorkflowYAML = repairResp.WorkflowYAML
			}
			if repairResp.StateYAML != "" {
				stateResp.StateYAML = repairResp.StateYAML
			}
			stateDiagnostics = diagnoseWorkflowWithProfile(finalWorkflowYAML, stateResp.StateYAML, "", "{}", graphengine.ProfileGenerationPhase)
		}
	}
	if hasDiagnosticErrorsForTarget(stateDiagnostics, "statemachine") {
		message := "phase2 state_machine validation failed: " + diagnosticsJSON(stateDiagnostics)
		_ = markGenerateFailed(db, payload.DraftID, message)
		return asyncjob.Result{ErrorCode: "generation_state_invalid"}, fmt.Errorf("%s", message)
	}
	if shouldRunGeneratePhase(startPhase, generatePhaseStateMachine) {
		stateUpdates := map[string]any{
			"state_yaml_content": stateResp.StateYAML,
			"generate_status":    generateStatusStateDone,
			"updated_at":         time.Now().UTC(),
		}
		setWorkflowYAMLUpdate(stateUpdates, finalWorkflowYAML)
		if len(stateResp.Warnings) > 0 {
			stateUpdates["generate_warning"] = strings.Join(stateResp.Warnings, "; ")
		}
		if err := db.WithContext(ctx).Model(&orm.WorkflowDraft{}).Where("id = ?", payload.DraftID).Updates(stateUpdates).Error; err != nil {
			return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save state_machine: %w", err)
		}
	}

	// ── Phase 3: Scenario + Scripts ──────────────────────────────────────────
	scenarioResp, err := algo.GenerateScenarioScripts(ctx, algo.GenerateScenarioScriptsRequest{
		Name:          draft.Name,
		WorkflowYAML:  finalWorkflowYAML,
		StateYAML:     stateResp.StateYAML,
		DesignBrief:   designBrief,
		SourceScripts: payload.ReusableScripts,
		LLMConfig:     llmConfig,
	})
	if err != nil {
		message := fmt.Sprintf("phase3 scenario_scripts failed: %s", err)
		_ = markGenerateFailed(db, payload.DraftID, message)
		return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("%s", message)
	}
	if err := validateGeneratedScenarioContent(scenarioResp.ScenarioMD, stateResp.StateYAML); err != nil {
		message := fmt.Sprintf("phase3 scenario_scripts invalid: %s", err)
		_ = markGenerateFailed(db, payload.DraftID, message)
		return asyncjob.Result{ErrorCode: "generation_scenario_invalid"}, fmt.Errorf("%s", message)
	}

	// Encode scripts map as JSON string for storage.
	scriptsJSON := "{}"
	if len(scenarioResp.Scripts) > 0 {
		if b, jerr := json.Marshal(scenarioResp.Scripts); jerr == nil {
			scriptsJSON = string(b)
		}
	}
	finalDiagnostics := diagnoseWorkflowWithProfile(finalWorkflowYAML, stateResp.StateYAML, scenarioResp.ScenarioMD, scriptsJSON, graphengine.ProfilePublish)
	if hasDiagnosticErrors(finalDiagnostics) {
		var issues []string
		for _, diagnostic := range finalDiagnostics {
			if diagnostic.Severity == "error" {
				issues = append(issues, diagnostic.Path+": "+diagnostic.Message)
			}
		}
		// UI and graph repair use different prompts, but both consume the same
		// authoritative Go diagnostics and are revalidated with publish rules.
		for _, target := range []string{"ui", "statemachine"} {
			if !hasDiagnosticErrorsForTarget(finalDiagnostics, target) {
				continue
			}
			repairResp, repairErr := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
				WorkflowYAML: finalWorkflowYAML,
				StateYAML:    stateResp.StateYAML,
				RepairHint:   "Automatically fix all post-generation validation errors. Preserve intended behavior and return a complete valid result.",
				Warnings:     issues,
				Diagnostics:  repairDiagnosticsPayload(finalDiagnostics),
				Target:       target,
				LLMConfig:    llmConfig,
			})
			if repairErr != nil {
				continue
			}
			if repairResp.StateYAML != "" {
				stateResp.StateYAML = repairResp.StateYAML
			}
			if repairResp.WorkflowYAML != "" {
				finalWorkflowYAML = repairResp.WorkflowYAML
			}
			finalDiagnostics = diagnoseWorkflowWithProfile(finalWorkflowYAML, stateResp.StateYAML, scenarioResp.ScenarioMD, scriptsJSON, graphengine.ProfilePublish)
		}
	}
	if hasDiagnosticErrors(finalDiagnostics) {
		message := "generation validation failed: " + diagnosticsJSON(finalDiagnostics)
		_ = markGenerateFailed(db, payload.DraftID, message)
		return asyncjob.Result{ErrorCode: "generation_coverage_incomplete"}, fmt.Errorf("%s", message)
	}
	var diagnosticWarnings []string
	for _, diagnostic := range finalDiagnostics {
		if diagnostic.Severity == "warning" {
			diagnosticWarnings = append(diagnosticWarnings, diagnostic.Message)
		}
	}

	finalUpdates := map[string]any{
		"state_yaml_content": stateResp.StateYAML,
		"scenario_content":   scenarioResp.ScenarioMD,
		"scripts_content":    scriptsJSON,
		"generate_status":    generateStatusDone,
		"generate_error":     "",
		"generate_warning":   mergeWarnings(mergeWarnings(currentGenerateWarning(db, payload.DraftID), strings.Join(scenarioResp.Warnings, "; ")), strings.Join(diagnosticWarnings, "; ")),
		"version":            gorm.Expr("version + 1"),
		"updated_at":         time.Now().UTC(),
	}
	setWorkflowYAMLUpdate(finalUpdates, finalWorkflowYAML)
	if err := db.WithContext(ctx).Model(&orm.WorkflowDraft{}).Where("id = ?", payload.DraftID).Updates(finalUpdates).Error; err != nil {
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save scenario_scripts: %w", err)
	}

	return asyncjob.Result{}, nil
}

func manifestOnlySkillPackage(pkg map[string]any) map[string]any {
	b, _ := json.Marshal(pkg)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if files, ok := out["files"].([]any); ok {
		for _, raw := range files {
			if file, ok := raw.(map[string]any); ok {
				delete(file, "content")
			}
		}
	}
	return out
}

func ignoredScriptWarning(report map[string]any) string {
	var ignored []string
	for path, raw := range report {
		item, _ := raw.(map[string]any)
		if item["classification"] == "unsupported" {
			reason, _ := item["reason"].(string)
			ignored = append(ignored, fmt.Sprintf("%s (%s)", path, reason))
		}
	}
	sort.Strings(ignored)
	if len(ignored) == 0 {
		return ""
	}
	return "已忽略不安全脚本: " + strings.Join(ignored, "; ")
}

func mergeWarnings(existing, added string) string {
	existing = strings.TrimSpace(existing)
	added = strings.TrimSpace(added)
	if existing == "" {
		return added
	}
	if added == "" {
		return existing
	}
	return existing + "; " + added
}

func validateGeneratedWorkflowSkeleton(workflowYAML string) error {
	if strings.TrimSpace(workflowYAML) == "" {
		return fmt.Errorf("workflow_yaml is empty")
	}
	var doc generatedWorkflowSkeleton
	if err := yaml.Unmarshal([]byte(workflowYAML), &doc); err != nil {
		return fmt.Errorf("workflow_yaml invalid: %w", err)
	}
	if strings.TrimSpace(doc.ID) == "" {
		return fmt.Errorf("workflow id is required")
	}
	if strings.TrimSpace(doc.Name) == "" {
		return fmt.Errorf("workflow name is required")
	}
	if len(doc.Slots) == 0 {
		return fmt.Errorf("at least one slot is required")
	}
	for index, slot := range doc.Slots {
		if strings.TrimSpace(slot.ID) == "" {
			return fmt.Errorf("slots[%d].id is required", index)
		}
	}
	if len(doc.Steps) == 0 {
		return fmt.Errorf("at least one step is required")
	}
	for index, step := range doc.Steps {
		if strings.TrimSpace(step.ID) == "" {
			return fmt.Errorf("steps[%d].id is required", index)
		}
	}
	return nil
}

func validateGeneratedScenarioContent(scenarioMD, stateYAML string) error {
	content := strings.TrimSpace(scenarioMD)
	if content == "" {
		return fmt.Errorf("scenario.md is empty")
	}
	placeholderPatterns := []string{
		"暂无描述",
		"待补充",
		"todo",
		"tbd",
		"no description",
		"placeholder",
	}
	lowerContent := strings.ToLower(content)
	for _, pattern := range placeholderPatterns {
		if strings.Contains(lowerContent, strings.ToLower(pattern)) {
			return fmt.Errorf("scenario.md contains placeholder text: %s", pattern)
		}
	}
	var stateDoc struct {
		Steps map[string]any `yaml:"steps"`
	}
	if err := yaml.Unmarshal([]byte(stateYAML), &stateDoc); err != nil {
		return fmt.Errorf("state_yaml invalid while checking scenario.md: %w", err)
	}
	for stepID := range stateDoc.Steps {
		if strings.TrimSpace(stepID) == "" {
			continue
		}
		index := strings.Index(content, stepID)
		if index < 0 {
			return fmt.Errorf("scenario.md does not document step %s", stepID)
		}
		after := strings.TrimSpace(content[index+len(stepID):])
		if after == "" {
			return fmt.Errorf("scenario.md has no description after step %s", stepID)
		}
		nextHeading := strings.Index(after, "\n### ")
		section := after
		if nextHeading >= 0 {
			section = after[:nextHeading]
		}
		section = strings.TrimSpace(strings.Trim(section, "()[]#- \t\r\n"))
		if len([]rune(section)) < 24 {
			return fmt.Errorf("scenario.md description for step %s is too short", stepID)
		}
	}
	return nil
}

func normalizeGenerateStartPhase(phase string) string {
	switch strings.TrimSpace(strings.ToLower(phase)) {
	case "", "brief", "design", "design_brief":
		return generatePhaseDesignBrief
	case "workflow", "workflow_yaml", "skeleton":
		return generatePhaseSkeleton
	case "state", "state_yaml", "state_machine":
		return generatePhaseStateMachine
	case "scenario", "scenario_md", "scripts", "scenario_scripts":
		return generatePhaseScenarioScripts
	default:
		return ""
	}
}

func generatePhaseRank(phase string) int {
	switch phase {
	case generatePhaseDesignBrief:
		return 0
	case generatePhaseSkeleton:
		return 1
	case generatePhaseStateMachine:
		return 2
	case generatePhaseScenarioScripts:
		return 3
	default:
		return -1
	}
}

func shouldRunGeneratePhase(startPhase, phase string) bool {
	return generatePhaseRank(phase) >= generatePhaseRank(startPhase)
}

func generateStatusForStartPhase(phase string) string {
	switch phase {
	case generatePhaseSkeleton:
		return generateStatusBriefDone
	case generatePhaseStateMachine:
		return generateStatusSkeletonDone
	case generatePhaseScenarioScripts:
		return generateStatusStateDone
	default:
		return generateStatusGenerating
	}
}

func validateGenerateResumePoint(draft orm.WorkflowDraft, startPhase string) error {
	if startPhase == "" {
		return fmt.Errorf("invalid start_phase")
	}
	switch startPhase {
	case generatePhaseDesignBrief:
		return nil
	case generatePhaseSkeleton:
		if strings.TrimSpace(draft.DesignBriefContent) == "" {
			return fmt.Errorf("design brief is not available")
		}
		return nil
	case generatePhaseStateMachine:
		return validateGeneratedWorkflowSkeleton(draft.WorkflowYAMLContent)
	case generatePhaseScenarioScripts:
		if err := validateGeneratedWorkflowSkeleton(draft.WorkflowYAMLContent); err != nil {
			return err
		}
		diagnostics := diagnoseWorkflowWithProfile(
			draft.WorkflowYAMLContent,
			draft.StateYAMLContent,
			"",
			"{}",
			graphengine.ProfileGenerationPhase,
		)
		if hasDiagnosticErrorsForTarget(diagnostics, "statemachine") {
			return fmt.Errorf("state machine is not valid")
		}
		return nil
	default:
		return fmt.Errorf("invalid start_phase")
	}
}

func currentGenerateWarning(db *gorm.DB, draftID string) string {
	var draft orm.WorkflowDraft
	if db.Select("generate_warning").Where("id=?", draftID).First(&draft).Error != nil {
		return ""
	}
	return draft.GenerateWarning
}

func markGenerateFailed(db *gorm.DB, draftID string, errMsg string) error {
	return db.Model(&orm.WorkflowDraft{}).Where("id = ?", draftID).Updates(map[string]any{
		"generate_status": generateStatusFailed,
		"generate_error":  errMsg,
		"updated_at":      time.Now().UTC(),
	}).Error
}

func handleWorkflowDraftRepairJob(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var payload workflowDraftRepairPayload
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		return asyncjob.Result{ErrorCode: generateErrInvalidPayload}, fmt.Errorf("decode repair payload: %w", err)
	}

	log.Printf("[repair_job] START draft_id=%s user_id=%s target=%q prev_status=%q warnings=%v hint_len=%d",
		payload.DraftID, payload.UserID, payload.Target, payload.PrevStatus,
		payload.Warnings, len(payload.RepairHint))

	db := store.DB()
	if db == nil {
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("db unavailable")
	}
	if payload.RepairRunID != "" {
		_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Updates(map[string]any{"status": "repairing", "updated_at": time.Now().UTC()}).Error
	}

	var draft orm.WorkflowDraft
	if err := db.Where("id = ?", payload.DraftID).First(&draft).Error; err != nil {
		log.Printf("[repair_job] draft not found draft_id=%s err=%v", payload.DraftID, err)
		return asyncjob.Result{ErrorCode: generateErrDraftNotFound}, fmt.Errorf("draft not found: %w", err)
	}
	if draft.Version != payload.DraftVersion {
		if payload.RepairRunID != "" {
			_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Updates(map[string]any{"status": "stale", "updated_at": time.Now().UTC()}).Error
		}
		return asyncjob.Result{ErrorCode: "repair_stale_draft"}, fmt.Errorf("repair stale draft")
	}
	log.Printf("[repair_job] draft loaded draft_id=%s workflow_yaml_len=%d state_yaml_len=%d version=%d",
		payload.DraftID, len(draft.WorkflowYAMLContent), len(draft.StateYAMLContent), draft.Version)

	llmConfig := payload.LLMConfig
	if llmConfig == nil {
		if loaded, err := modelconfig.LoadLLMConfig(ctx, db, payload.UserID); err == nil {
			llmConfig = loaded
			log.Printf("[repair_job] llm_config loaded from DB for user_id=%s", payload.UserID)
		} else {
			llmConfig = map[string]any{}
			log.Printf("[repair_job] llm_config load failed (using empty), err=%v", err)
		}
	} else {
		log.Printf("[repair_job] llm_config from payload (keys=%d)", len(llmConfig))
	}

	restoreStatus := func(repairErr string) {
		updates := map[string]any{
			"generate_status": payload.PrevStatus,
			"updated_at":      time.Now().UTC(),
		}
		if repairErr != "" {
			updates["generate_warning"] = "[修复失败] " + repairErr
		}
		log.Printf("[repair_job] RESTORE draft_id=%s status=%q warning=%q",
			payload.DraftID, payload.PrevStatus, updates["generate_warning"])
		_ = db.Model(&orm.WorkflowDraft{}).Where("id = ?", payload.DraftID).Updates(updates)
		if payload.RepairRunID != "" {
			// diagnostics_after_json is written by the validation path with the
			// structured report. Do not replace it with a generic error string.
			_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Updates(map[string]any{"status": "failed", "updated_at": time.Now().UTC()}).Error
		}
	}

	if payload.Target == "scripts" || payload.Target == "full" {
		var scripts map[string]string
		if json.Unmarshal([]byte(draft.ScriptsContent), &scripts) != nil {
			scripts = map[string]string{}
		}
		workflowYAML, stateYAML, scenarioMD := draft.WorkflowYAMLContent, draft.StateYAMLContent, draft.ScenarioContent
		var allWarnings []string
		if payload.Target == "full" {
			stateResp, callErr := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{WorkflowYAML: workflowYAML, StateYAML: stateYAML, RepairHint: payload.RepairHint, Warnings: payload.Warnings, Diagnostics: payload.Diagnostics, Target: "statemachine", LLMConfig: llmConfig})
			if callErr != nil {
				restoreStatus(callErr.Error())
				return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, callErr
			}
			stateYAML = stateResp.StateYAML
			if stateResp.WorkflowYAML != "" {
				workflowYAML = stateResp.WorkflowYAML
			}
			allWarnings = append(allWarnings, stateResp.RemainingWarnings...)
			scenarioResp, callErr := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{WorkflowYAML: workflowYAML, StateYAML: stateYAML, RepairHint: payload.RepairHint, Target: "scenario", LLMConfig: llmConfig})
			if callErr != nil {
				restoreStatus(callErr.Error())
				return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, callErr
			}
			scenarioMD = scenarioResp.ScenarioMD
			if scenarioMD == "" {
				scenarioMD = scenarioResp.StateYAML
			}
		}
		scriptResp, callErr := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{WorkflowYAML: workflowYAML, StateYAML: stateYAML, ScenarioMD: scenarioMD, Scripts: scripts, RepairHint: payload.RepairHint, Target: "scripts", LLMConfig: llmConfig})
		if callErr != nil {
			restoreStatus(callErr.Error())
			return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, callErr
		}
		if scriptResp.WorkflowYAML != "" {
			workflowYAML = scriptResp.WorkflowYAML
		}
		if scriptResp.StateYAML != "" {
			stateYAML = scriptResp.StateYAML
		}
		if scriptResp.ScenarioMD != "" {
			scenarioMD = scriptResp.ScenarioMD
		}
		scripts = scriptResp.Scripts
		allWarnings = append(allWarnings, scriptResp.RemainingWarnings...)
		scriptsBytes, _ := json.Marshal(scripts)
		scriptsJSON := string(scriptsBytes)
		afterDiagnostics := diagnoseWorkflowWithProfile(workflowYAML, stateYAML, scenarioMD, scriptsJSON, graphengine.ProfilePublish)
		if payload.RepairRunID != "" {
			_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Update("diagnostics_after_json", diagnosticsJSON(afterDiagnostics)).Error
		}
		if hasDiagnosticErrorsForTarget(afterDiagnostics, "full") {
			restoreStatus("repair validation failed")
			return asyncjob.Result{ErrorCode: "repair_validation_failed"}, fmt.Errorf("repair validation failed")
		}
		updates := map[string]any{"state_yaml_content": stateYAML, "scenario_content": scenarioMD, "scripts_content": scriptsJSON, "generate_status": payload.PrevStatus, "generate_warning": mergeWarnings(currentGenerateWarning(db, draft.ID), strings.Join(allWarnings, "; ")), "version": draft.Version + 1, "updated_at": time.Now().UTC()}
		setWorkflowYAMLUpdate(updates, workflowYAML)
		result := db.Model(&orm.WorkflowDraft{}).Where("id=? AND version=?", draft.ID, payload.DraftVersion).Updates(updates)
		if result.Error != nil || result.RowsAffected != 1 {
			if payload.RepairRunID != "" {
				_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Update("status", "stale").Error
			}
			return asyncjob.Result{ErrorCode: "repair_stale_draft"}, fmt.Errorf("repair stale draft")
		}
		if payload.RepairRunID != "" {
			files := []string{"workflow.yaml", "scenario/state.yml", "scenario/scenario.md", "scripts"}
			changes, _ := json.Marshal(map[string]any{"files": files})
			_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Updates(map[string]any{"status": "succeeded", "changes_json": string(changes), "updated_at": time.Now().UTC()}).Error
		}
		return asyncjob.Result{}, nil
	}

	if payload.Target == "scenario" {
		scenarioHint := payload.RepairHint
		if scenarioHint == "" {
			scenarioHint = "Fix or complete the scenario.md documentation."
		}
		log.Printf("[repair_job/scenario] calling algo.RepairStateMachine with target=scenario hint_len=%d", len(scenarioHint))
		resp, err := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
			WorkflowYAML: draft.WorkflowYAMLContent,
			StateYAML:    draft.StateYAMLContent,
			RepairHint:   scenarioHint,
			Target:       "scenario",
			Warnings:     payload.Warnings,
			LLMConfig:    llmConfig,
		})
		if err != nil {
			log.Printf("[repair_job/scenario] algo error: %v", err)
			restoreStatus(err.Error())
			return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("repair scenario: %w", err)
		}
		log.Printf("[repair_job/scenario] algo returned scenario_md_len=%d (in state_yaml field)", len(resp.StateYAML))
		scenarioMD := resp.ScenarioMD
		if scenarioMD == "" {
			scenarioMD = resp.StateYAML
		}
		if err := validateGeneratedScenarioContent(scenarioMD, draft.StateYAMLContent); err != nil {
			restoreStatus("repair validation failed: " + err.Error())
			return asyncjob.Result{ErrorCode: "repair_validation_failed"}, fmt.Errorf("repair validation failed: %w", err)
		}
		afterDiagnostics := diagnoseWorkflow(draft.WorkflowYAMLContent, draft.StateYAMLContent, scenarioMD, draft.ScriptsContent)
		if payload.RepairRunID != "" {
			_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Update("diagnostics_after_json", diagnosticsJSON(afterDiagnostics)).Error
		}
		if hasDiagnosticErrorsForTarget(afterDiagnostics, "scenario") {
			restoreStatus("repair validation failed")
			return asyncjob.Result{ErrorCode: "repair_validation_failed"}, fmt.Errorf("repair validation failed")
		}
		updates := map[string]any{
			"scenario_content": scenarioMD,
			"generate_status":  payload.PrevStatus,
			"generate_warning": "", // clear any previous warning on success
			"version":          draft.Version + 1,
			"updated_at":       time.Now().UTC(),
		}
		result := db.Model(&orm.WorkflowDraft{}).Where("id=? AND version=?", draft.ID, payload.DraftVersion).Updates(updates)
		if result.Error != nil || result.RowsAffected != 1 {
			if payload.RepairRunID != "" {
				_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Updates(map[string]any{"status": "stale", "updated_at": time.Now().UTC()}).Error
			}
			log.Printf("[repair_job/scenario] DB save failed: %v", err)
			return asyncjob.Result{ErrorCode: "repair_stale_draft"}, fmt.Errorf("save repair: stale draft")
		}
		if payload.RepairRunID != "" {
			_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Updates(map[string]any{"status": "succeeded", "changes_json": `{"files":["scenario/scenario.md"]}`, "updated_at": time.Now().UTC()}).Error
		}
		log.Printf("[repair_job/scenario] SUCCESS draft_id=%s new_version=%d", payload.DraftID, draft.Version+1)
		return asyncjob.Result{}, nil
	}

	// statemachine / ui target
	log.Printf("[repair_job/statemachine] calling algo.RepairStateMachine target=%q hint_len=%d warnings=%v",
		payload.Target, len(payload.RepairHint), payload.Warnings)
	resp, err := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
		WorkflowYAML: draft.WorkflowYAMLContent,
		StateYAML:    draft.StateYAMLContent,
		RepairHint:   payload.RepairHint,
		Target:       payload.Target,
		Warnings:     payload.Warnings,
		Diagnostics:  payload.Diagnostics,
		LLMConfig:    llmConfig,
	})
	if err != nil {
		log.Printf("[repair_job/statemachine] algo error: %v", err)
		restoreStatus(err.Error())
		return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("repair statemachine: %w", err)
	}
	log.Printf("[repair_job/statemachine] algo returned state_yaml_len=%d workflow_yaml_updated=%v remaining_warnings=%v",
		len(resp.StateYAML), resp.WorkflowYAML != "", resp.RemainingWarnings)

	newWarning := strings.Join(resp.RemainingWarnings, "; ")
	finalWorkflowYAML := draft.WorkflowYAMLContent
	if resp.WorkflowYAML != "" {
		finalWorkflowYAML = resp.WorkflowYAML
	}
	profile := graphengine.ProfileEditor
	if payload.Target == "statemachine" || payload.Target == "full" {
		profile = graphengine.ProfilePublish
	}
	afterDiagnostics := diagnoseWorkflowWithProfile(finalWorkflowYAML, resp.StateYAML, draft.ScenarioContent, draft.ScriptsContent, profile)
	if payload.RepairRunID != "" {
		_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Update("diagnostics_after_json", diagnosticsJSON(afterDiagnostics)).Error
	}
	if hasDiagnosticErrorsForTarget(afterDiagnostics, payload.Target) {
		restoreStatus("repair validation failed")
		return asyncjob.Result{ErrorCode: "repair_validation_failed"}, fmt.Errorf("repair validation failed")
	}
	updates := map[string]any{
		"state_yaml_content": resp.StateYAML,
		"generate_warning":   newWarning,
		"generate_status":    payload.PrevStatus,
		"version":            draft.Version + 1,
		"updated_at":         time.Now().UTC(),
	}
	if resp.WorkflowYAML != "" {
		setWorkflowYAMLUpdate(updates, resp.WorkflowYAML)
	}
	result := db.Model(&orm.WorkflowDraft{}).Where("id=? AND version=?", draft.ID, payload.DraftVersion).Updates(updates)
	if result.Error != nil || result.RowsAffected != 1 {
		if payload.RepairRunID != "" {
			_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Updates(map[string]any{"status": "stale", "updated_at": time.Now().UTC()}).Error
		}
		log.Printf("[repair_job/statemachine] DB save failed: %v", err)
		return asyncjob.Result{ErrorCode: "repair_stale_draft"}, fmt.Errorf("save repair: stale draft")
	}
	if payload.RepairRunID != "" {
		files := `["scenario/state.yml"]`
		if resp.WorkflowYAML != "" {
			files = `["workflow.yaml","scenario/state.yml"]`
		}
		_ = db.Model(&orm.WorkflowRepairRun{}).Where("id=?", payload.RepairRunID).Updates(map[string]any{"status": "succeeded", "changes_json": `{"files":` + files + `}`, "updated_at": time.Now().UTC()}).Error
	}
	log.Printf("[repair_job/statemachine] SUCCESS draft_id=%s new_version=%d status=%q warning=%q",
		payload.DraftID, draft.Version+1, payload.PrevStatus, newWarning)
	return asyncjob.Result{}, nil
}
