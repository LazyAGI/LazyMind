package algo

import (
	"context"

	"lazymind/core/common"
)

// Staged plugin generation — three sequential phases, each writes to DB independently.

const (
	generateSkeletonPath     = "/api/chat/generate_plugin/skeleton"
	generateStateMachinePath = "/api/chat/generate_plugin/state_machine"
	generateScenarioPath     = "/api/chat/generate_plugin/scenario_scripts"
)

// GenerateSkeletonRequest is the request body for Phase 1.
type GenerateSkeletonRequest struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	SkillContent string         `json:"skill_content,omitempty"`
	LLMConfig    map[string]any `json:"llm_config"`
}

// GenerateSkeletonResponse is the response body from Phase 1.
type GenerateSkeletonResponse struct {
	PluginYAML string `json:"plugin_yaml"`
}

// GenerateStateMachineRequest is the request body for Phase 2.
type GenerateStateMachineRequest struct {
	Name       string         `json:"name"`
	PluginYAML string         `json:"plugin_yaml"`
	LLMConfig  map[string]any `json:"llm_config"`
}

// GenerateStateMachineResponse is the response body from Phase 2.
type GenerateStateMachineResponse struct {
	StateYAML string `json:"state_yaml"`
}

// GenerateScenarioScriptsRequest is the request body for Phase 3.
type GenerateScenarioScriptsRequest struct {
	Name       string         `json:"name"`
	PluginYAML string         `json:"plugin_yaml"`
	StateYAML  string         `json:"state_yaml"`
	LLMConfig  map[string]any `json:"llm_config"`
}

// GenerateScenarioScriptsResponse is the response body from Phase 3.
type GenerateScenarioScriptsResponse struct {
	ScenarioMD string            `json:"scenario_md"`
	Scripts    map[string]string `json:"scripts"`
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
	return &GenerateStateMachineResponse{
		StateYAML: extractStringField(raw, "state_yaml"),
	}, nil
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
	return resp, nil
}
