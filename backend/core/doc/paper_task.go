package doc

import (
	"context"
	"encoding/json"
	"lazymind/core/algo"
)

// Keep the injected generator boundary used by the source/ACL handlers while
// transporting structured objects directly instead of rewrite content wrappers.
func generateStructuredPaperResult(ctx context.Context, content, task, version string, config map[string]any) (string, error) {
	var input map[string]any
	if err := json.Unmarshal([]byte(content), &input); err != nil {
		return "", err
	}
	result, err := algo.RunStructuredTask(ctx, algo.StructuredTaskRequest{Task: task, Version: version, Input: input, LLMConfig: config})
	if err != nil {
		return "", err
	}
	return string(result.Output), nil
}
