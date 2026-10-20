package subagent

import (
	"context"
	"encoding/json"
	mcpadapter "lazymind/core/capability/mcp"
	"lazymind/core/knowledgeruntime"
	"lazymind/core/state"
	"slices"
)

func KnowledgeToolsForSelection(selection []string) []string {
	if selection == nil {
		selection = []string{"knowledge_management"}
	}
	result := []string{}
	for _, wire := range mcpadapter.KnowledgeManagementTools {
		if slices.Contains(selection, "knowledge_management") || slices.Contains(selection, wire) ||
			(slices.Contains(selection, "kb") && (wire == "knowledge.list" || wire == "knowledge.document.list" || wire == "knowledge.document.aggregate")) {
			result = append(result, wire)
		}
	}
	return result
}
func KnowledgeSystemConfig(selection []string) []any {
	if !knowledgeruntime.Enabled() {
		return []any{}
	}
	return []any{map[string]any{"id": "lazymind-knowledge", "name": "lazymind-knowledge", "transport": "streamable-http", "result_mode": "structured", "allowed_tools": KnowledgeToolsForSelection(selection)}}
}
func saveKnowledgeRunPolicy(ctx context.Context, st state.Store, req RunRequest, generation string) error {
	if st == nil {
		return nil
	}
	allowed := []string{}
	for _, v := range req.SystemMCPConfig {
		if cfg, ok := v.(map[string]any); ok && cfg["id"] == "lazymind-knowledge" {
			allowed, _ = cfg["allowed_tools"].([]string)
		}
	}
	data, _ := json.Marshal(map[string]any{"allowed_tools": allowed})
	return st.Set(ctx, "knowledge/task/"+req.TaskID+"/"+generation, data, subagentRunTimeout)
}
