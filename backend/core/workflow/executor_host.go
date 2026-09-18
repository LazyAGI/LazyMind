package workflow

import "lazymind/core/workflow/graphengine"

// Steps with declared tools or post-step checks run entirely inside LazyMind.
func executorHostForStep(controllerHost string, node graphengine.CompiledNode, runtime graphengine.RuntimePolicy) string {
	if controllerHost != "external-agent" {
		return controllerHost
	}
	if len(node.LegacyTools) > 0 || len(node.TerminalTools) > 0 {
		return "lazymind"
	}
	for _, check := range runtime.PostStepChecks {
		if check.StepID == node.ID {
			return "lazymind"
		}
	}
	return controllerHost
}
