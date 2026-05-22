package chat

import "lazymind/core/modelconfig"

type selectedRuntimeModel = modelconfig.SelectedRuntimeModel

func buildLLMConfig(rows []selectedRuntimeModel) map[string]any {
	return modelconfig.BuildLLMConfig(rows)
}
