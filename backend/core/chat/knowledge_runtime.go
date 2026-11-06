package chat

import (
	mcpadapter "lazymind/core/capability/mcp"
	"lazymind/core/knowledgeruntime"
	"lazymind/core/vocabulary"
	"slices"
)

func applySystemMCPConfig(userID string, body map[string]any) error {
	// Browser builder starts from trusted configuration, discarding client entries.
	if err := applyBrowserRuntimeConfig(userID, body); err != nil {
		return err
	}
	disabled := stringSliceFromAny(body["disabled_tools"])
	configs, _ := body["system_mcp_config"].([]any)
	if knowledgeruntime.Enabled() && !slices.Contains(disabled, "knowledge_management") {
		configs = append(configs, map[string]any{"id": "lazymind-knowledge", "name": "lazymind-knowledge", "transport": "streamable-http", "allowed_tools": mcpadapter.ChatKnowledgeManagementTools(), "result_mode": "structured"})
	}
	if vocabulary.Enabled() && !slices.Contains(disabled, "vocabulary") {
		configs = append(configs, map[string]any{"id": "lazymind-vocabulary", "name": "LazyMind Vocabulary", "transport": "streamable-http", "allowed_tools": mcpadapter.ChatVocabularyTools(), "result_mode": "structured"})
	}
	if len(configs) > 0 {
		body["system_mcp_config"] = configs
	}
	return nil
}
