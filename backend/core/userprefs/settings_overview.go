package userprefs

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/settings"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
)

type settingsOverviewCounts struct {
	Total      int64 `json:"total"`
	Enabled    int64 `json:"enabled"`
	Verified   int64 `json:"verified"`
	Runnable   int64 `json:"runnable"`
	Configured int64 `json:"configured"`
}

type settingsOverviewSection struct {
	ID               string                 `json:"id"`
	Title            string                 `json:"title"`
	Route            string                 `json:"route"`
	RawEnabled       *bool                  `json:"raw_enabled,omitempty"`
	EffectiveEnabled *bool                  `json:"effective_enabled,omitempty"`
	Counts           settingsOverviewCounts `json:"counts"`
	Status           string                 `json:"status"`
	Detail           string                 `json:"detail"`
}

type settingsOverviewIssue struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Section  string `json:"section"`
}

type settingsOverviewResponse struct {
	Controls  settings.FeatureControls  `json:"controls"`
	Sections  []settingsOverviewSection `json:"sections"`
	Issues    []settingsOverviewIssue   `json:"issues"`
	UpdatedAt string                    `json:"updated_at"`
}

type settingsCheckResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Section string `json:"section"`
}

type settingsChecksResponse struct {
	StartedAt  string                `json:"started_at"`
	FinishedAt string                `json:"finished_at"`
	Results    []settingsCheckResult `json:"results"`
}

func GetSettingsOverview(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	overview, err := buildSettingsOverview(r, db, userID)
	if err != nil {
		common.ReplyErr(w, "load settings overview failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, overview)
}

// RunSettingsChecks intentionally performs only verifiable checks. It does not
// invent cross-module impact counts; the dependency graph phase owns that work.
func RunSettingsChecks(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	startedAt := time.Now().UTC()
	overview, err := buildSettingsOverview(r, db, userID)
	if err != nil {
		common.ReplyErr(w, "load settings checks failed", http.StatusInternalServerError)
		return
	}

	results := []settingsCheckResult{}
	model := sectionByID(overview.Sections, "models")
	if model.Counts.Configured > 0 {
		results = append(results, settingsCheckResult{ID: "models", Status: "passed", Message: "已找到已选模型", Section: "models"})
	} else {
		results = append(results, settingsCheckResult{ID: "models", Status: "attention", Message: "尚未选择模型", Section: "models"})
	}
	mcpSection := sectionByID(overview.Sections, "mcp")
	if mcpSection.Counts.Enabled > mcpSection.Counts.Runnable {
		results = append(results, settingsCheckResult{ID: "mcp", Status: "attention", Message: "存在已启用但尚未通过验证或工具授权的 MCP 服务", Section: "mcp"})
	} else {
		results = append(results, settingsCheckResult{ID: "mcp", Status: "passed", Message: "MCP 服务验证状态正常", Section: "mcp"})
	}

	ffmpegStatus, ffmpegErr := detectFFmpegForSettings()
	if ffmpegErr != nil {
		results = append(results, settingsCheckResult{ID: "system_tools", Status: "attention", Message: "无法读取本地依赖状态", Section: "system_tools"})
	} else if ffmpegStatus.Installed {
		results = append(results, settingsCheckResult{ID: "system_tools", Status: "passed", Message: "本地依赖可用", Section: "system_tools"})
	} else {
		results = append(results, settingsCheckResult{ID: "system_tools", Status: "attention", Message: ffmpegStatus.Message, Section: "system_tools"})
	}
	results = append(results, settingsCheckResult{ID: "channels", Status: "not_checked", Message: "渠道连接需要在终端连接中单独验证", Section: "channels"})

	common.ReplyOK(w, settingsChecksResponse{
		StartedAt:  startedAt.Format(time.RFC3339Nano),
		FinishedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Results:    results,
	})
}

func buildSettingsOverview(r *http.Request, db *gorm.DB, userID string) (settingsOverviewResponse, error) {
	controls, err := settings.LoadFeatureControls(r.Context(), db, userID)
	if err != nil {
		return settingsOverviewResponse{}, err
	}
	var selectedModels, schedules, enabledSchedules, totalSkills, enabledSkills, totalWorkflows, enabledWorkflows, mcpServers, enabledMCP, verifiedMCP, runnableMCP int64
	if err := db.WithContext(r.Context()).Model(&orm.UserSelectedModel{}).Where("user_id = ?", userID).Count(&selectedModels).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	if err := db.WithContext(r.Context()).Model(&orm.UserSchedule{}).Where("user_id = ?", userID).Count(&schedules).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	if err := db.WithContext(r.Context()).Model(&orm.UserSchedule{}).Where("user_id = ? AND enabled = ?", userID, true).Count(&enabledSchedules).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	if err := db.WithContext(r.Context()).Model(&orm.SkillV2Skill{}).Where("owner_user_id = ? AND is_enabled = ? AND deleted_at IS NULL", userID, true).Count(&enabledSkills).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	if err := db.WithContext(r.Context()).Model(&orm.SkillV2Skill{}).Where("owner_user_id = ? AND deleted_at IS NULL", userID).Count(&totalSkills).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	workflowBase := db.WithContext(r.Context()).Table("plugins p").Where("p.status = ? AND (p.owner_user_id = ? OR p.owner_user_id = '')", "active", userID)
	if err := workflowBase.Count(&totalWorkflows).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	if err := db.WithContext(r.Context()).Table("plugins p").
		Joins("JOIN user_plugin_settings ups ON ups.plugin_ref = p.plugin_ref AND ups.user_id = ? AND ups.enabled = ?", userID, true).
		Where("p.status = ? AND (p.owner_user_id = ? OR p.owner_user_id = '')", "active", userID).
		Count(&enabledWorkflows).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	mcpQuery := db.WithContext(r.Context()).Model(&orm.MCPServer{}).Where("create_user_id = ? AND deleted_at IS NULL", userID)
	if err := mcpQuery.Count(&mcpServers).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	if err := db.WithContext(r.Context()).Model(&orm.MCPServer{}).Where("create_user_id = ? AND deleted_at IS NULL AND enabled = ?", userID, true).Count(&enabledMCP).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	if err := db.WithContext(r.Context()).Model(&orm.MCPServer{}).Where("create_user_id = ? AND deleted_at IS NULL AND enabled = ? AND is_verified = ?", userID, true, true).Count(&verifiedMCP).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	var verifiedServers []orm.MCPServer
	if err := db.WithContext(r.Context()).Where("create_user_id = ? AND deleted_at IS NULL AND enabled = ? AND is_verified = ?", userID, true, true).Find(&verifiedServers).Error; err != nil {
		return settingsOverviewResponse{}, err
	}
	for _, server := range verifiedServers {
		var allowedTools []string
		if json.Unmarshal(server.AllowedToolsJSON, &allowedTools) == nil && len(allowedTools) > 0 {
			runnableMCP++
		}
	}

	modelReady := selectedModels > 0
	tasksControlEnabled := controls.TaskCenterEnabled || controls.WorkflowsEnabled || controls.SchedulesEnabled
	tasksEffective := controls.TaskCenterEnabled ||
		(controls.WorkflowsEnabled && enabledWorkflows > 0) ||
		(controls.SchedulesEnabled && enabledSchedules > 0)
	enabledSkillResources := enabledSkills + enabledWorkflows
	skillsControlEnabled := controls.SkillsEnabled || controls.WorkflowsEnabled
	skillsEffective := (controls.SkillsEnabled && enabledSkills > 0) || (controls.WorkflowsEnabled && enabledWorkflows > 0)
	mcpEffective := controls.MCPEnabled && runnableMCP > 0
	sections := []settingsOverviewSection{
		{ID: "overview", Title: "设置概览", Route: "/settings?section=overview", Status: "ready", Detail: "统一查看运行状态和待处理配置"},
		{ID: "models", Title: "模型与服务", Route: "/model-providers/default-services", EffectiveEnabled: boolPointer(modelReady), Counts: settingsOverviewCounts{Configured: selectedModels}, Status: statusFor(modelReady), Detail: "已选模型决定对话与工具能力"},
		{ID: "tasks", Title: "对话与任务", Route: "/task-center", RawEnabled: boolPointer(tasksControlEnabled), EffectiveEnabled: boolPointer(tasksEffective), Counts: settingsOverviewCounts{Total: schedules, Enabled: enabledSchedules}, Status: statusFor(tasksControlEnabled), Detail: "子任务、工作流和定时任务分别控制，关闭不会删除已有配置"},
		{ID: "knowledge", Title: "知识与数据", Route: "/settings?section=knowledge", RawEnabled: boolPointer(controls.DocumentParsingEnabled), EffectiveEnabled: boolPointer(controls.DocumentParsingEnabled), Status: statusFor(controls.DocumentParsingEnabled), Detail: "管理检索工具、解析服务、数据源和文件连接"},
		{ID: "memory", Title: "记忆与自进化", Route: "/memory-management", Status: "ready", Detail: "管理记忆、经验和术语"},
		{ID: "skills", Title: "技能与插件", Route: "/memory-management/skills", RawEnabled: boolPointer(skillsControlEnabled), EffectiveEnabled: boolPointer(skillsEffective), Counts: settingsOverviewCounts{Total: totalSkills + totalWorkflows, Enabled: enabledSkillResources}, Status: statusFor(skillsControlEnabled), Detail: "我的技能和我的工作流分别批量启用或停用"},
		{ID: "system_tools", Title: "系统工具", Route: "/model-providers/tools", Status: "ready", Detail: "依赖配置独立于运行开关"},
		{ID: "mcp", Title: "MCP 工具", Route: "/model-providers/tools?view=mcp", RawEnabled: boolPointer(controls.MCPEnabled), EffectiveEnabled: boolPointer(mcpEffective), Counts: settingsOverviewCounts{Total: mcpServers, Enabled: enabledMCP, Verified: verifiedMCP, Runnable: runnableMCP}, Status: statusFor(controls.MCPEnabled), Detail: "需要总开关、服务启用和验证同时满足"},
		{ID: "channels", Title: "终端连接", Route: "/channels", Status: "ready", Detail: "配置终端和外部渠道连接"},
		{ID: "diagnostics", Title: "同步与查验", Route: "/settings?section=diagnostics", Status: "ready", Detail: "检查模型、MCP 和本地依赖"},
	}
	issues := make([]settingsOverviewIssue, 0, 4)
	if !modelReady {
		issues = append(issues, settingsOverviewIssue{ID: "model-not-configured", Severity: "warning", Message: "尚未选择模型", Section: "models"})
	}
	if enabledMCP > runnableMCP {
		issues = append(issues, settingsOverviewIssue{ID: "mcp-needs-verification", Severity: "warning", Message: "存在已启用但尚未通过验证或工具授权的 MCP 服务", Section: "mcp"})
	}
	if !controls.SchedulesEnabled && enabledSchedules > 0 {
		issues = append(issues, settingsOverviewIssue{ID: "scheduled-tasks-paused", Severity: "info", Message: "定时任务已暂停；任务配置和原始开关仍被保留", Section: "tasks"})
	}
	if !controls.DocumentParsingEnabled {
		issues = append(issues, settingsOverviewIssue{ID: "document-parsing-paused", Severity: "info", Message: "文档解析已暂停；已有文档和服务配置仍被保留", Section: "knowledge"})
	}
	return settingsOverviewResponse{Controls: controls, Sections: sections, Issues: issues, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

func detectFFmpegForSettings() (systemdeps.FFmpegStatus, error) {
	if !systemdeps.IsLocalRuntime() {
		return systemdeps.DetectFFmpeg("")
	}
	runtimeRoot, err := systemdeps.RuntimeRootFromEnv()
	if err != nil {
		return systemdeps.FFmpegStatus{}, err
	}
	return systemdeps.DetectFFmpeg(runtimeRoot)
}

func sectionByID(sections []settingsOverviewSection, id string) settingsOverviewSection {
	for _, section := range sections {
		if section.ID == id {
			return section
		}
	}
	return settingsOverviewSection{}
}

func boolPointer(value bool) *bool { return &value }

func statusFor(enabled bool) string {
	if enabled {
		return "ready"
	}
	return "paused"
}
