package algo

import (
	"context"
	"encoding/json"

	"lazymind/core/common"
)

// Staged plugin generation — four sequential phases, each writes to DB independently.

const (
	generateAnalyzeSkillPath = "/api/chat/generate_plugin/analyze_skill"
	generateDesignBriefPath  = "/api/chat/generate_plugin/design_brief"
	generateSkeletonPath     = "/api/chat/generate_plugin/skeleton"
	generateStateMachinePath = "/api/chat/generate_plugin/state_machine"
	generateScenarioPath     = "/api/chat/generate_plugin/scenario_scripts"
)

type AnalyzeSkillRequest struct {
	Name         string         `json:"name"`
	SkillPackage map[string]any `json:"skill_package"`
	LLMConfig    map[string]any `json:"llm_config"`
}

type AnalyzeSkillResponse struct {
	Verdict      string           `json:"verdict"`
	VerdictCode  string           `json:"verdict_code"`
	Message      string           `json:"message"`
	Candidates   []map[string]any `json:"candidates"`
	Coverage     map[string]any   `json:"coverage"`
	ToolMappings map[string]any   `json:"tool_mappings"`
	Scripts      map[string]any   `json:"scripts"`
}

func AnalyzeSkill(ctx context.Context, req AnalyzeSkillRequest) (*AnalyzeSkillResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	var raw map[string]any
	if err := common.ApiPost(ctx, generateURL(generateAnalyzeSkillPath), req, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	b, _ := json.Marshal(raw)
	var resp AnalyzeSkillResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// Phase 0: Design Brief
// ---------------------------------------------------------------------------

// DesignBriefRequest is the request body for Phase 0.
type DesignBriefRequest struct {
	Name             string         `json:"name"`
	Description      string         `json:"description,omitempty"`
	SkillContent     string         `json:"skill_content,omitempty"`
	SkillPackage     map[string]any `json:"skill_package,omitempty"`
	WorkflowAnalysis string         `json:"workflow_analysis,omitempty"`
	LLMConfig        map[string]any `json:"llm_config"`
}

// DesignBriefResponse is the response body from Phase 0.
type DesignBriefResponse struct {
	DesignBrief string `json:"design_brief"`
}

// DesignBrief calls Phase 0: generate the design brief Markdown.
func DesignBrief(ctx context.Context, req DesignBriefRequest) (*DesignBriefResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	url := generateURL(generateDesignBriefPath)
	var raw map[string]any
	if err := common.ApiPost(ctx, url, req, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	return &DesignBriefResponse{
		DesignBrief: extractStringField(raw, "design_brief"),
	}, nil
}

// GenerateSkeletonRequest is the request body for Phase 1.
type GenerateSkeletonRequest struct {
	Name             string         `json:"name"`
	Description      string         `json:"description,omitempty"`
	SkillContent     string         `json:"skill_content,omitempty"`
	SkillPackage     map[string]any `json:"skill_package,omitempty"`
	WorkflowAnalysis string         `json:"workflow_analysis,omitempty"`
	DesignBrief      string         `json:"design_brief,omitempty"`
	LLMConfig        map[string]any `json:"llm_config"`
}

// GenerateSkeletonResponse is the response body from Phase 1.
type GenerateSkeletonResponse struct {
	PluginYAML string `json:"plugin_yaml"`
}

// GenerateStateMachineRequest is the request body for Phase 2.
type GenerateStateMachineRequest struct {
	Name             string         `json:"name"`
	PluginYAML       string         `json:"plugin_yaml"`
	DesignBrief      string         `json:"design_brief,omitempty"`
	WorkflowAnalysis string         `json:"workflow_analysis,omitempty"`
	LLMConfig        map[string]any `json:"llm_config"`
}

// GenerateStateMachineResponse is the response body from Phase 2.
type GenerateStateMachineResponse struct {
	StateYAML  string   `json:"state_yaml"`
	PluginYAML string   `json:"plugin_yaml"` // may be updated by slot repair
	Warnings   []string `json:"warnings"`
}

// GenerateScenarioScriptsRequest is the request body for Phase 3.
type GenerateScenarioScriptsRequest struct {
	Name          string            `json:"name"`
	PluginYAML    string            `json:"plugin_yaml"`
	StateYAML     string            `json:"state_yaml"`
	DesignBrief   string            `json:"design_brief,omitempty"`
	SourceScripts map[string]string `json:"source_scripts,omitempty"`
	LLMConfig     map[string]any    `json:"llm_config"`
}

// GenerateScenarioScriptsResponse is the response body from Phase 3.
type GenerateScenarioScriptsResponse struct {
	ScenarioMD string            `json:"scenario_md"`
	Scripts    map[string]string `json:"scripts"`
	Warnings   []string          `json:"warnings"`
}

func ensureLLMConfig(c map[string]any) map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return c
}

func extractStringField(raw map[string]any, key string) string {
	if v, ok := raw[key].(string); ok {
		return v
	}
	if data, ok := raw["data"].(map[string]any); ok {
		if v, ok := data[key].(string); ok {
			return v
		}
	}
	return ""
}

func extractScripts(raw map[string]any) map[string]string {
	tryFrom := func(m map[string]any) map[string]string {
		v, ok := m["scripts"].(map[string]any)
		if !ok {
			return nil
		}
		result := make(map[string]string, len(v))
		for k, val := range v {
			if s, ok := val.(string); ok {
				result[k] = s
			}
		}
		return result
	}
	if s := tryFrom(raw); s != nil {
		return s
	}
	if data, ok := raw["data"].(map[string]any); ok {
		if s := tryFrom(data); s != nil {
			return s
		}
	}
	return nil
}

// GenerateSkeleton calls Phase 1: generate plugin.yaml skeleton.
func GenerateSkeleton(ctx context.Context, req GenerateSkeletonRequest) (*GenerateSkeletonResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	url := generateURL(generateSkeletonPath)
	var raw map[string]any
	if err := common.ApiPost(ctx, url, req, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	return &GenerateSkeletonResponse{
		PluginYAML: extractStringField(raw, "plugin_yaml"),
	}, nil
}

// GenerateStateMachine calls Phase 2: generate state.yml from the skeleton.
func GenerateStateMachine(ctx context.Context, req GenerateStateMachineRequest) (*GenerateStateMachineResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	url := generateURL(generateStateMachinePath)
	var raw map[string]any
	if err := common.ApiPost(ctx, url, req, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	resp := &GenerateStateMachineResponse{
		StateYAML: extractStringField(raw, "state_yaml"),
	}
	// Phase 2 may return an updated plugin_yaml when slot repair was applied.
	resp.PluginYAML = extractStringField(raw, "plugin_yaml")
	// Extract warnings list from response (may be absent for older Python versions).
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	if warnRaw, ok := raw["warnings"].([]any); ok {
		for _, w := range warnRaw {
			if s, ok := w.(string); ok {
				resp.Warnings = append(resp.Warnings, s)
			}
		}
	}
	return resp, nil
}

// GenerateScenarioScripts calls Phase 3: generate scenario.md and optional scripts.
func GenerateScenarioScripts(ctx context.Context, req GenerateScenarioScriptsRequest) (*GenerateScenarioScriptsResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	url := generateURL(generateScenarioPath)
	var raw map[string]any
	if err := common.ApiPost(ctx, url, req, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	resp := &GenerateScenarioScriptsResponse{
		ScenarioMD: extractStringField(raw, "scenario_md"),
		Scripts:    extractScripts(raw),
	}
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	if values, ok := raw["warnings"].([]any); ok {
		for _, value := range values {
			if warning, ok := value.(string); ok {
				resp.Warnings = append(resp.Warnings, warning)
			}
		}
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// State machine repair
// ---------------------------------------------------------------------------

const repairStateMachinePath = "/api/chat/generate_plugin/repair"

// RepairStateMachineRequest is the request body for the repair endpoint.
type RepairStateMachineRequest struct {
	PluginYAML  string            `json:"plugin_yaml"`
	StateYAML   string            `json:"state_yaml"`
	RepairHint  string            `json:"repair_hint,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	Diagnostics []map[string]any  `json:"diagnostics,omitempty"`
	Target      string            `json:"target,omitempty"` // 'statemachine' | 'ui' | 'scenario'
	ScenarioMD  string            `json:"scenario_md,omitempty"`
	Scripts     map[string]string `json:"scripts,omitempty"`
	LLMConfig   map[string]any    `json:"llm_config"`
}

// RepairStateMachineResponse is the response body from the repair endpoint.
type RepairStateMachineResponse struct {
	StateYAML         string            `json:"state_yaml"`
	PluginYAML        string            `json:"plugin_yaml"` // may be updated when slot repair was applied
	RemainingWarnings []string          `json:"remaining_warnings"`
	ScenarioMD        string            `json:"scenario_md"`
	Scripts           map[string]string `json:"scripts"`
}

// RepairStateMachine calls the repair endpoint to fix an incomplete state.yml.
func RepairStateMachine(ctx context.Context, req RepairStateMachineRequest) (*RepairStateMachineResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	url := generateURL(repairStateMachinePath)
	var raw map[string]any
	if err := common.ApiPost(ctx, url, req, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	resp := &RepairStateMachineResponse{
		StateYAML:  extractStringField(raw, "state_yaml"),
		PluginYAML: extractStringField(raw, "plugin_yaml"),
		ScenarioMD: extractStringField(raw, "scenario_md"),
		Scripts:    extractScripts(raw),
	}
	if warnRaw, ok := raw["remaining_warnings"].([]any); ok {
		for _, w := range warnRaw {
			if s, ok := w.(string); ok {
				resp.RemainingWarnings = append(resp.RemainingWarnings, s)
			}
		}
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Plugin info polish
// ---------------------------------------------------------------------------

const polishPluginInfoPath = "/api/chat/generate_plugin/polish_info"

// PolishPluginInfoRequest matches the Python request body.
type PolishPluginInfoRequest struct {
	Fields       map[string]string `json:"fields"`
	TargetFields []string          `json:"target_fields"`
	LLMConfig    map[string]any    `json:"llm_config"`
}

// PolishPluginInfoResponse holds the polished field values (only target_fields are populated).
type PolishPluginInfoResponse struct {
	Description *string `json:"description,omitempty"`
	WhenToUse   *string `json:"when_to_use,omitempty"`
	Overview    *string `json:"overview,omitempty"`
	Notes       *string `json:"notes,omitempty"`
}

// PolishPluginInfo proxies to the Python polish_info endpoint.
func PolishPluginInfo(ctx context.Context, req PolishPluginInfoRequest) (*PolishPluginInfoResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	url := generateURL(polishPluginInfoPath)
	var raw map[string]any
	if err := common.ApiPost(ctx, url, req, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	// Unwrap data envelope if present
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	resp := &PolishPluginInfoResponse{}
	if v, ok := raw["description"].(string); ok {
		resp.Description = &v
	}
	if v, ok := raw["when_to_use"].(string); ok {
		resp.WhenToUse = &v
	}
	if v, ok := raw["overview"].(string); ok {
		resp.Overview = &v
	}
	if v, ok := raw["notes"].(string); ok {
		resp.Notes = &v
	}
	return resp, nil
}
