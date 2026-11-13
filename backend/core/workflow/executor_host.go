package workflow

import "lazymind/core/workflow/graphengine"

// Shared execution supports the controller's host. Steps with native-only
// tools or checks are handled by the native executor.
func executorHostForStep(controllerHost, provider string, node graphengine.CompiledNode, runtime graphengine.RuntimePolicy) string {
	if controllerHost != "external-agent" {
		return controllerHost
	}
	if provider != "" && runtime.Execution.UsesExternalModels() {
		return "lazymind"
	}
	if node.Execution.Supported() {
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
