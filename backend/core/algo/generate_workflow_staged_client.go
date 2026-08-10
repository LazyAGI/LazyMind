package algo

import (
	"context"
	"encoding/json"

	"lazymind/core/common"
)

// Staged plugin generation — four sequential phases, each writes to DB independently.

const (
	generateAnalyzeSkillPath = "/api/chat/generate_workflow/analyze_skill"
	generateDesignBriefPath  = "/api/chat/generate_workflow/design_brief"
	generateSkeletonPath     = "/api/chat/generate_workflow/skeleton"
	generateStateMachinePath = "/api/chat/generate_workflow/state_machine"
	generateScenarioPath     = "/api/chat/generate_workflow/scenario_scripts"
	llmTaskRunPath           = "/api/chat/llm-task:run"
)

type llmTaskFile struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
}

type llmTaskInput struct {
	Text  string         `json:"text,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
	Files []llmTaskFile  `json:"files,omitempty"`
}

type llmTaskRequest struct {
	Mode           string           `json:"mode"`
	TaskType       string           `json:"task_type"`
	Instruction    string           `json:"instruction,omitempty"`
	Input          llmTaskInput     `json:"input"`
	Skills         []map[string]any `json:"skills,omitempty"`
	Tools          []map[string]any `json:"tools,omitempty"`
	ResponseFormat map[string]any   `json:"response_format,omitempty"`
	LLMConfig      map[string]any   `json:"llm_config,omitempty"`
	Options        map[string]any   `json:"options,omitempty"`
}

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
	raw, err := callWorkflowLLMTask(ctx, "workflow.analyze_skill", "Analyze whether this Skill can be converted into an executable Workflow.", map[string]any{
		"name":          req.Name,
		"skill_package": req.SkillPackage,
	}, nil, req.LLMConfig)
	if err != nil {
		return nil, err
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
	raw, err := callWorkflowLLMTask(ctx, "workflow.design_brief", "Create an implementation brief for the Workflow generation phases.", map[string]any{
		"name":              req.Name,
		"description":       req.Description,
		"skill_content":     req.SkillContent,
		"skill_package":     req.SkillPackage,
		"workflow_analysis": req.WorkflowAnalysis,
	}, nil, req.LLMConfig)
	if err != nil {
		return nil, err
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
	WorkflowYAML string `json:"workflow_yaml"`
}

// GenerateStateMachineRequest is the request body for Phase 2.
type GenerateStateMachineRequest struct {
	Name             string         `json:"name"`
	WorkflowYAML     string         `json:"workflow_yaml"`
	DesignBrief      string         `json:"design_brief,omitempty"`
	WorkflowAnalysis string         `json:"workflow_analysis,omitempty"`
	LLMConfig        map[string]any `json:"llm_config"`
}

// GenerateStateMachineResponse is the response body from Phase 2.
type GenerateStateMachineResponse struct {
	StateYAML    string   `json:"state_yaml"`
	WorkflowYAML string   `json:"workflow_yaml"` // may be updated by slot repair
	Warnings     []string `json:"warnings"`
}

// GenerateScenarioScriptsRequest is the request body for Phase 3.
type GenerateScenarioScriptsRequest struct {
	Name          string            `json:"name"`
	WorkflowYAML  string            `json:"workflow_yaml"`
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

func workflowTaskSkills() []map[string]any {
	return []map[string]any{{"name": "create-workflow", "source": "builtin", "required": true}}
}

func workflowTaskTools(taskType string) []map[string]any {
	if taskType == "workflow.repair" {
		return []map[string]any{{"name": "str_replace", "source": "builtin", "mode": "expanded"}}
	}
	return nil
}

func callWorkflowLLMTask(ctx context.Context, taskType, instruction string, data map[string]any, files []llmTaskFile, llmConfig map[string]any) (map[string]any, error) {
	payload := llmTaskRequest{
		Mode:           "agent",
		TaskType:       taskType,
		Instruction:    instruction,
		Input:          llmTaskInput{Data: data, Files: files},
		Skills:         workflowTaskSkills(),
		Tools:          workflowTaskTools(taskType),
		ResponseFormat: map[string]any{"type": "json_object"},
		LLMConfig:      ensureLLMConfig(llmConfig),
		Options:        map[string]any{"max_retries": 2},
	}
	var raw map[string]any
	if err := common.ApiPost(ctx, generateURL(llmTaskRunPath), payload, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	if output, ok := raw["output"].(map[string]any); ok {
		return output, nil
	}
	return raw, nil
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

func extractStringSlice(raw map[string]any, key string) []string {
	var result []string
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	if values, ok := raw[key].([]any); ok {
		for _, value := range values {
			if item, ok := value.(string); ok {
				result = append(result, item)
			}
		}
	}
	return result
}

// GenerateSkeleton calls Phase 1: generate workflow.yaml skeleton.
func GenerateSkeleton(ctx context.Context, req GenerateSkeletonRequest) (*GenerateSkeletonResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	raw, err := callWorkflowLLMTask(ctx, "workflow.generate_skeleton", "Generate workflow.yaml skeleton. It must be compatible with the LazyMind Workflow compiler.", map[string]any{
		"name":              req.Name,
		"description":       req.Description,
		"skill_content":     req.SkillContent,
		"skill_package":     req.SkillPackage,
		"workflow_analysis": req.WorkflowAnalysis,
		"design_brief":      req.DesignBrief,
	}, nil, req.LLMConfig)
	if err != nil {
		return nil, err
	}
	return &GenerateSkeletonResponse{
		WorkflowYAML: extractStringField(raw, "workflow_yaml"),
	}, nil
}

// GenerateStateMachine calls Phase 2: generate state.yml from the skeleton.
func GenerateStateMachine(ctx context.Context, req GenerateStateMachineRequest) (*GenerateStateMachineResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	raw, err := callWorkflowLLMTask(ctx, "workflow.generate_state_machine", "Generate scenario/state.yml for this workflow.yaml. Return publish-valid graph YAML.", map[string]any{
		"name":              req.Name,
		"workflow_yaml":     req.WorkflowYAML,
		"design_brief":      req.DesignBrief,
		"workflow_analysis": req.WorkflowAnalysis,
	}, []llmTaskFile{{Path: "workflow.yaml", Content: req.WorkflowYAML, ContentType: "text/yaml"}}, req.LLMConfig)
	if err != nil {
		return nil, err
	}
	resp := &GenerateStateMachineResponse{
		StateYAML: extractStringField(raw, "state_yaml"),
	}
	// Phase 2 may return an updated workflow_yaml when slot repair was applied.
	resp.WorkflowYAML = extractStringField(raw, "workflow_yaml")
	// Extract warnings list from response (may be absent for older Python versions).
	resp.Warnings = extractStringSlice(raw, "warnings")
	return resp, nil
}

// GenerateScenarioScripts calls Phase 3: generate scenario.md and optional scripts.
func GenerateScenarioScripts(ctx context.Context, req GenerateScenarioScriptsRequest) (*GenerateScenarioScriptsResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	files := []llmTaskFile{
		{Path: "workflow.yaml", Content: req.WorkflowYAML, ContentType: "text/yaml"},
		{Path: "scenario/state.yml", Content: req.StateYAML, ContentType: "text/yaml"},
	}
	raw, err := callWorkflowLLMTask(ctx, "workflow.generate_scenario_scripts", "Generate complete scenario documentation and safe optional scripts for the Workflow. The scenario markdown must include one substantive section for every state step, explaining purpose, inputs, outputs, and runtime behavior. Do not return placeholder text.", map[string]any{
		"name":           req.Name,
		"workflow_yaml":  req.WorkflowYAML,
		"state_yaml":     req.StateYAML,
		"design_brief":   req.DesignBrief,
		"source_scripts": req.SourceScripts,
	}, files, req.LLMConfig)
	if err != nil {
		return nil, err
	}
	resp := &GenerateScenarioScriptsResponse{
		ScenarioMD: extractStringField(raw, "scenario_md"),
		Scripts:    extractScripts(raw),
	}
	resp.Warnings = extractStringSlice(raw, "warnings")
	return resp, nil
}

// ---------------------------------------------------------------------------
// State machine repair
// ---------------------------------------------------------------------------

const repairStateMachinePath = "/api/chat/generate_workflow/repair"

// RepairStateMachineRequest is the request body for the repair endpoint.
type RepairStateMachineRequest struct {
	WorkflowYAML string            `json:"workflow_yaml"`
	StateYAML    string            `json:"state_yaml"`
	RepairHint   string            `json:"repair_hint,omitempty"`
	Warnings     []string          `json:"warnings,omitempty"`
	Diagnostics  []map[string]any  `json:"diagnostics,omitempty"`
	Target       string            `json:"target,omitempty"` // 'statemachine' | 'ui' | 'scenario'
	ScenarioMD   string            `json:"scenario_md,omitempty"`
	Scripts      map[string]string `json:"scripts,omitempty"`
	LLMConfig    map[string]any    `json:"llm_config"`
}

// RepairStateMachineResponse is the response body from the repair endpoint.
type RepairStateMachineResponse struct {
	StateYAML         string            `json:"state_yaml"`
	WorkflowYAML      string            `json:"workflow_yaml"` // may be updated when slot repair was applied
	RemainingWarnings []string          `json:"remaining_warnings"`
	ScenarioMD        string            `json:"scenario_md"`
	Scripts           map[string]string `json:"scripts"`
}

// RepairStateMachine calls the repair endpoint to fix an incomplete state.yml.
func RepairStateMachine(ctx context.Context, req RepairStateMachineRequest) (*RepairStateMachineResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	files := []llmTaskFile{
		{Path: "workflow.yaml", Content: req.WorkflowYAML, ContentType: "text/yaml"},
		{Path: "scenario/state.yml", Content: req.StateYAML, ContentType: "text/yaml"},
	}
	if req.ScenarioMD != "" {
		files = append(files, llmTaskFile{Path: "scenario/scenario.md", Content: req.ScenarioMD, ContentType: "text/markdown"})
	}
	for path, content := range req.Scripts {
		files = append(files, llmTaskFile{Path: path, Content: content, ContentType: "text/x-python"})
	}
	raw, err := callWorkflowLLMTask(ctx, "workflow.repair", "Repair only the requested Workflow target. Prefer precise edits for local changes and return final full content for changed files.", map[string]any{
		"workflow_yaml": req.WorkflowYAML,
		"state_yaml":    req.StateYAML,
		"repair_hint":   req.RepairHint,
		"warnings":      req.Warnings,
		"diagnostics":   req.Diagnostics,
		"target":        req.Target,
		"scenario_md":   req.ScenarioMD,
		"scripts":       req.Scripts,
	}, files, req.LLMConfig)
	if err != nil {
		return nil, err
	}
	resp := &RepairStateMachineResponse{
		StateYAML:    extractStringField(raw, "state_yaml"),
		WorkflowYAML: extractStringField(raw, "workflow_yaml"),
		ScenarioMD:   extractStringField(raw, "scenario_md"),
		Scripts:      extractScripts(raw),
	}
	if req.Target == "scenario" && resp.StateYAML == "" {
		resp.StateYAML = resp.ScenarioMD
	}
	resp.RemainingWarnings = extractStringSlice(raw, "remaining_warnings")
	return resp, nil
}

// ---------------------------------------------------------------------------
// Workflow info polish
// ---------------------------------------------------------------------------

const polishWorkflowInfoPath = "/api/chat/generate_workflow/polish_info"

// PolishWorkflowInfoRequest matches the Python request body.
type PolishWorkflowInfoRequest struct {
	Fields       map[string]string `json:"fields"`
	TargetFields []string          `json:"target_fields"`
	LLMConfig    map[string]any    `json:"llm_config"`
}

// PolishWorkflowInfoResponse holds the polished field values (only target_fields are populated).
type PolishWorkflowInfoResponse struct {
	Description *string `json:"description,omitempty"`
	WhenToUse   *string `json:"when_to_use,omitempty"`
	Overview    *string `json:"overview,omitempty"`
	Notes       *string `json:"notes,omitempty"`
}

// PolishWorkflowInfo proxies to the Python polish_info endpoint.
func PolishWorkflowInfo(ctx context.Context, req PolishWorkflowInfoRequest) (*PolishWorkflowInfoResponse, error) {
	req.LLMConfig = ensureLLMConfig(req.LLMConfig)
	raw, err := callWorkflowLLMTask(ctx, "workflow.polish_info", "Polish only the requested Workflow metadata fields.", map[string]any{
		"fields":        req.Fields,
		"target_fields": req.TargetFields,
	}, nil, req.LLMConfig)
	if err != nil {
		return nil, err
	}
	resp := &PolishWorkflowInfoResponse{}
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
