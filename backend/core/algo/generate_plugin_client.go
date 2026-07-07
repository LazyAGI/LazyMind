package algo

import (
	"context"

	"lazymind/core/common"
)

const generatePluginPath = "/api/chat/generate_plugin"

// GeneratePluginRequest is the request body for the /api/chat/generate_plugin endpoint.
type GeneratePluginRequest struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	SkillContent string         `json:"skill_content,omitempty"`
	LLMConfig    map[string]any `json:"llm_config"`
}

// GeneratePluginResponse is the response body from /api/chat/generate_plugin.
type GeneratePluginResponse struct {
	PluginYAML string            `json:"plugin_yaml"`
	StateYAML  string            `json:"state_yaml"`
	ScenarioMD string            `json:"scenario_md"`
	Scripts    map[string]string `json:"scripts"`
}

// GeneratePlugin calls the Python chat service to generate plugin YAML files from a
// natural-language description or an existing skill definition.
func GeneratePlugin(ctx context.Context, req GeneratePluginRequest) (*GeneratePluginResponse, error) {
	if req.LLMConfig == nil {
		req.LLMConfig = map[string]any{}
	}
	url := generateURL(generatePluginPath)
	var raw map[string]any
	if err := common.ApiPost(ctx, url, req, nil, &raw, generateTimeout); err != nil {
		return nil, err
	}
	resp := &GeneratePluginResponse{}
	if data, ok := raw["data"].(map[string]any); ok {
		resp.PluginYAML, _ = data["plugin_yaml"].(string)
		resp.StateYAML, _ = data["state_yaml"].(string)
		resp.ScenarioMD, _ = data["scenario_md"].(string)
		if scripts, ok := data["scripts"].(map[string]any); ok {
			resp.Scripts = make(map[string]string, len(scripts))
			for k, v := range scripts {
				if s, ok := v.(string); ok {
					resp.Scripts[k] = s
				}
			}
		}
	} else {
		resp.PluginYAML, _ = raw["plugin_yaml"].(string)
		resp.StateYAML, _ = raw["state_yaml"].(string)
		resp.ScenarioMD, _ = raw["scenario_md"].(string)
		if scripts, ok := raw["scripts"].(map[string]any); ok {
			resp.Scripts = make(map[string]string, len(scripts))
			for k, v := range scripts {
				if s, ok := v.(string); ok {
					resp.Scripts[k] = s
				}
			}
		}
	}
	return resp, nil
}
