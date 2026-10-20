package chat

import (
	mcpadapter "lazymind/core/capability/mcp"
	"lazymind/core/knowledgeruntime"
)

func applySystemMCPConfig(userID string, body map[string]any) error {
	// Browser builder starts from trusted configuration, discarding client entries.
	if err := applyBrowserRuntimeConfig(userID, body); err != nil {
		return err
	}
	if !knowledgeruntime.Enabled() {
		return nil
	}
	configs, _ := body["system_mcp_config"].([]any)
	body["system_mcp_config"] = append(configs, map[string]any{"id": "lazymind-knowledge", "name": "lazymind-knowledge", "transport": "streamable-http", "allowed_tools": append([]string{}, mcpadapter.KnowledgeManagementTools...), "result_mode": "structured"})
	return nil
}
