package workflow

import "lazymind/core/workflow/graphengine"

// Versioned external steps retain external execution even when the native
// implementation declares tools/checks. Older revisions keep their routing.
func executorHostForStep(controllerHost string, node graphengine.CompiledNode, runtime graphengine.RuntimePolicy) string {
	if controllerHost != "external-agent" {
		return controllerHost
	}
	if node.ExternalExecution.Supported() {
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
