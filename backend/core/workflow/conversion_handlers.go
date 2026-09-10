package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/settings"
	"lazymind/core/store"
)

type skillConversionCheck struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
	Path       string `json:"path,omitempty"`
}

var (
	skillDependencyRefPattern = regexp.MustCompile(`(?:^|[\s"'(（\[])((?:references|assets|scripts)/[A-Za-z0-9._@+~=-][A-Za-z0-9._@+~=/ -]*)`)
	skillTemplateParamPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)
	skillPlaceholderPattern   = regexp.MustCompile(`(?i)\b(TODO|TBD|YOUR_[A-Z0-9_]+|REPLACE_ME)\b`)
)

func PreflightSkillWorkflowConversion(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		SkillID string `json:"skill_id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.SkillID) == "" {
		common.ReplyErr(w, "skill_id required", http.StatusBadRequest)
		return
	}
	snapshot, err := loadWorkflowSourceSkill(r.Context(), store.DB(), userID, strings.TrimSpace(body.SkillID))
	if err != nil {
		status := http.StatusInternalServerError
		if isWorkflowSourceSkillNotFound(err) {
			status = http.StatusNotFound
		}
		common.ReplyErrWithData(w, "skill conversion preflight failed", map[string]any{
			"skill_id": body.SkillID,
			"status":   "blocked",
			"checks": []skillConversionCheck{{
				Code:       "SKILL_NOT_AVAILABLE",
				Severity:   "error",
				Message:    "Skill 不存在、未发布或缺少 SKILL.md。",
				Suggestion: "请先确认 Skill 已发布，且包内包含非空 SKILL.md。",
			}},
		}, status)
		return
	}
	checks := preflightSkillSnapshot(snapshot)
	requirements := detectSkillCapabilityRequirementsFromSnapshot(snapshot)
	checks = append(checks, preflightCapabilityChecks(requirements)...)
	checks = append(checks, preflightCapabilityRuntimeChecks(r.Context(), store.DB(), userID, requirements)...)
	status := "pass"
	for _, check := range checks {
		if check.Severity == "error" {
			status = "blocked"
			break
		}
		if check.Severity == "warning" && status == "pass" {
			status = "warning"
		}
	}
	summary := "Skill 内容完整，可以开始转换。"
	switch status {
	case "blocked":
		summary = "Skill 存在阻断项，建议修复后再转换。"
	case "warning":
		summary = "Skill 可以转换，但部分内容可能被忽略或需要转换后调整。"
	}
	common.ReplyOK(w, map[string]any{
		"skill_id":     snapshot.SkillID,
		"skill_name":   snapshot.Name,
		"revision_id":  snapshot.RevisionID,
		"revision_no":  snapshot.RevisionNo,
		"tree_hash":    snapshot.TreeHash,
		"status":       status,
		"summary":      summary,
		"checks":       checks,
		"file_count":   len(snapshot.Files),
		"skill_md_len": len([]rune(snapshot.skillMD())),
	})
}

func preflightCapabilityChecks(requirements []skillCapabilityRequirement) []skillConversionCheck {
	checks := make([]skillConversionCheck, 0, len(requirements))
	for _, req := range requirements {
		severity := "warning"
		suggestion := "转换时会尝试把该能力声明到 Workflow 步骤中；转换后请确认对应步骤可正常执行。"
		if !req.Supported {
			severity = "error"
			suggestion = "当前 LazyMind Workflow 尚未提供该能力的自动映射，请继续使用原 Skill，或转换后手动改造 Workflow。"
		}
		checks = append(checks, skillConversionCheck{
			Code:       "REQUIRED_WORKFLOW_CAPABILITY",
			Severity:   severity,
			Path:       "SKILL.md",
			Message:    fmt.Sprintf("Skill 依赖%s能力：%s", req.Label, req.Reason),
			Suggestion: suggestion,
		})
	}
	return checks
}

func preflightCapabilityRuntimeChecks(ctx context.Context, db *gorm.DB, userID string, requirements []skillCapabilityRequirement) []skillConversionCheck {
	issues := skillCapabilityRuntimeConfigIssues(ctx, db, userID, requirements)
	checks := make([]skillConversionCheck, 0, len(issues))
	for _, issue := range issues {
		message := fmt.Sprintf("Skill 依赖%s能力，但当前运行配置可能不可用。", issue.Label)
		suggestion := "请先在设置中配置对应工具或模型；未配置时可继续使用原 Skill，或转换后手动调整 Workflow。"
		if issue.Reason == "tool_config_check_failed" || issue.Reason == "model_config_check_failed" {
			message = fmt.Sprintf("Skill 依赖%s能力，但运行配置检查失败。", issue.Label)
			suggestion = "请确认相关服务配置正常后再转换，或转换后手动校验 Workflow 可用性。"
		}
		checks = append(checks, skillConversionCheck{
			Code:       "REQUIRED_CAPABILITY_CONFIG_MISSING",
			Severity:   "warning",
			Path:       "SKILL.md",
			Message:    message,
			Suggestion: suggestion,
		})
	}
	return checks
}

func preflightSkillSnapshot(snapshot workflowSourceSkillSnapshot) []skillConversionCheck {
	var checks []skillConversionCheck
	byPath := map[string]skillPackageFile{}
	for _, file := range snapshot.Files {
		byPath[file.Path] = file
		if file.Path == "SKILL.md" {
			continue
		}
		if file.Binary {
			checks = append(checks, skillConversionCheck{
				Code:       "UNSUPPORTED_BINARY_RESOURCE",
				Severity:   "warning",
				Path:       file.Path,
				Message:    "Skill 包含二进制资源，自动转换只能引用该资源，不能理解其内部内容。",
				Suggestion: "请在 SKILL.md 或 references 中补充该资源的用途、输入输出和使用限制。",
			})
		}
		if file.Size > 1024*1024 {
			checks = append(checks, skillConversionCheck{
				Code:       "LARGE_RESOURCE",
				Severity:   "warning",
				Path:       file.Path,
				Message:    "Skill 资源较大，转换时可能只使用摘要或文件引用。",
				Suggestion: "建议把关键流程、规则和示例整理到 markdown 文档中。",
			})
		}
		if strings.HasPrefix(file.Path, "scripts/") && !strings.HasSuffix(file.Path, ".py") {
			checks = append(checks, skillConversionCheck{
				Code:       "UNSUPPORTED_SCRIPT_TYPE",
				Severity:   "warning",
				Path:       file.Path,
				Message:    "当前转换流程主要支持 Python 脚本复用，其他脚本类型可能被忽略。",
				Suggestion: "如需在 Workflow 中复用，请改写为可导入的 Python 工具，或转换后手动配置工具步骤。",
			})
		}
	}
	skillMD := strings.TrimSpace(snapshot.skillMD())
	if skillMD == "" {
		checks = append(checks, skillConversionCheck{
			Code:       "SKILL_MISSING_SKILL_MD",
			Severity:   "error",
			Message:    "Skill 缺少非空 SKILL.md。",
			Suggestion: "请先补充 Skill 的用途、步骤、输入输出和使用约束。",
			Path:       "SKILL.md",
		})
		return checks
	}
	if len([]rune(skillMD)) < 80 {
		checks = append(checks, skillConversionCheck{
			Code:       "SKILL_INSTRUCTIONS_TOO_SHORT",
			Severity:   "warning",
			Message:    "SKILL.md 内容较短，可能不足以稳定生成可执行 Workflow。",
			Suggestion: "建议补充任务目标、关键步骤、输入输出、失败处理和示例。",
			Path:       "SKILL.md",
		})
	}
	seenMissing := map[string]bool{}
	for _, match := range skillDependencyRefPattern.FindAllStringSubmatch(skillMD, -1) {
		ref := cleanSkillDependencyRef(match[1])
		if ref == "" || seenMissing[ref] {
			continue
		}
		if _, ok := byPath[ref]; !ok {
			seenMissing[ref] = true
			checks = append(checks, skillConversionCheck{
				Code:       "DEPENDENCY_RESOURCE_MISSING",
				Severity:   "error",
				Path:       ref,
				Message:    "SKILL.md 引用了包内不存在的依赖资源。",
				Suggestion: "请补齐该资源，或删除/改正 SKILL.md 中的引用。",
			})
		}
	}
	for _, match := range skillTemplateParamPattern.FindAllStringSubmatch(skillMD, -1) {
		param := strings.TrimSpace(match[1])
		if param == "" {
			continue
		}
		checks = append(checks, skillConversionCheck{
			Code:       "REQUIRED_PARAMETER_PLACEHOLDER",
			Severity:   "warning",
			Message:    fmt.Sprintf("SKILL.md 包含参数占位符 {{%s}}，转换后可能需要配置为 Workflow 输入。", param),
			Suggestion: "请确认该参数的来源、默认值和是否必填。",
			Path:       "SKILL.md",
		})
	}
	for _, match := range skillPlaceholderPattern.FindAllStringSubmatch(skillMD, -1) {
		checks = append(checks, skillConversionCheck{
			Code:       "UNRESOLVED_PLACEHOLDER",
			Severity:   "warning",
			Message:    fmt.Sprintf("SKILL.md 包含未替换占位内容 %q。", match[1]),
			Suggestion: "建议先替换为真实配置或删除示例占位。",
			Path:       "SKILL.md",
		})
	}
	sort.SliceStable(checks, func(i, j int) bool {
		if checks[i].Severity != checks[j].Severity {
			return checks[i].Severity == "error"
		}
		return checks[i].Code < checks[j].Code
	})
	return checks
}

func cleanSkillDependencyRef(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.Trim(value, "`'\".,;:，。；：）)]}>")
	for _, sep := range []string{"\n", "\r", "\t"} {
		if index := strings.Index(value, sep); index >= 0 {
			value = value[:index]
		}
	}
	return strings.TrimSpace(value)
}

func ListSkillLinkedWorkflows(w http.ResponseWriter, r *http.Request) {
	userID, skillID := common.UserID(r), strings.TrimSpace(common.PathVar(r, "skill_id"))
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if skillID == "" {
		common.ReplyErr(w, "skill_id required", http.StatusBadRequest)
		return
	}
	current, err := loadWorkflowSourceSkill(r.Context(), store.DB(), userID, skillID)
	if err != nil {
		if isWorkflowSourceSkillNotFound(err) {
			common.ReplyErr(w, "skill not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "load skill failed", http.StatusInternalServerError)
		return
	}
	controls, controlsErr := settings.LoadFeatureControls(r.Context(), store.DB(), userID)
	if controlsErr != nil {
		common.ReplyErr(w, "load feature controls failed", http.StatusInternalServerError)
		return
	}
	type row struct {
		orm.WorkflowResource
		Enabled       *bool           `gorm:"column:enabled"`
		CallMode      *string         `gorm:"column:call_mode"`
		TreeHash      string          `gorm:"column:tree_hash"`
		CompiledGraph json.RawMessage `gorm:"column:compiled_graph"`
	}
	var rows []row
	err = store.DB().Table("plugins p").
		Select("p.*, ups.enabled, ups.call_mode, pr.tree_hash, pr.compiled_graph").
		Joins("LEFT JOIN user_plugin_settings ups ON ups.plugin_ref=p.plugin_ref AND ups.user_id=?", userID).
		Joins("LEFT JOIN plugin_revisions pr ON pr.id=p.head_revision_id").
		Where("p.source_type = ? AND p.source_skill_id = ? AND p.status = 'active' AND (p.owner_user_id = ? OR p.owner_user_id = '')", "skill", skillID, userID).
		Order("p.updated_at DESC").
		Scan(&rows).Error
	if err != nil {
		if missingWorkflowTables(err) {
			common.ReplyOK(w, map[string]any{"skill_id": skillID, "workflows": []map[string]any{}})
			return
		}
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		callMode := WorkflowCallModeDisabled
		if v.CallMode != nil {
			callMode = normalizeWorkflowCallMode(*v.CallMode, v.Enabled != nil && *v.Enabled)
		} else if v.Enabled != nil {
			callMode = normalizeWorkflowCallMode("", *v.Enabled)
		}
		available := controls.WorkflowsEnabled && v.HeadRevisionID != "" && workflowCallModeEnabled(callMode)
		reason := ""
		if !controls.WorkflowsEnabled {
			reason = "workflows_paused"
		} else if v.HeadRevisionID == "" {
			reason = "workflow_unpublished"
		} else if !workflowCallModeEnabled(callMode) {
			reason = "workflow_disabled"
		}
		if available {
			mappings := latestRequiredCapabilityMappingsForDraft(store.DB(), v.SourceDraftID)
			ok, capabilityReason := workflowRevisionCapabilityDiagnostics(v.CompiledGraph, mappings)
			if !ok {
				available = false
				reason = capabilityReason
			} else if ok, capabilityReason = workflowRuntimeCapabilityDiagnostics(r.Context(), store.DB(), userID, mappings); !ok {
				available = false
				reason = capabilityReason
			}
		}
		items = append(items, map[string]any{
			"workflow_ref":              v.WorkflowRef,
			"workflow_id":               v.WorkflowID,
			"name":                      v.Name,
			"description":               v.Description,
			"when_to_use":               v.WhenToUse,
			"status":                    v.Status,
			"enabled":                   workflowCallModeEnabled(callMode),
			"call_mode":                 callMode,
			"revision_id":               v.HeadRevisionID,
			"revision_no":               v.Version,
			"tree_hash":                 v.TreeHash,
			"source_skill_id":           v.SourceSkillID,
			"source_skill_name":         v.SourceSkillName,
			"source_skill_revision_id":  v.SourceSkillRevisionID,
			"source_skill_revision_no":  v.SourceSkillRevisionNo,
			"source_skill_tree_hash":    v.SourceSkillTreeHash,
			"current_skill_revision_id": current.RevisionID,
			"current_skill_revision_no": current.RevisionNo,
			"current_skill_tree_hash":   current.TreeHash,
			"source_is_current":         current.TreeHash == "" || v.SourceSkillTreeHash == current.TreeHash,
			"available":                 available,
			"unavailable_reason":        reason,
		})
	}
	common.ReplyOK(w, map[string]any{"skill_id": skillID, "skill_name": current.Name, "workflows": items})
}
