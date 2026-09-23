package workflow

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
)

type workflowModelError struct {
	Code    string
	Message string
}

func (e *workflowModelError) Error() string { return e.Code + ": " + e.Message }

// Resolve at execution time using the same user selections as Models and Services.
// Config contains credentials; only Public may cross the external API boundary.
type workflowModelResolution struct {
	Config map[string]any
	Public map[string]any
}

func resolveWorkflowModel(ctx context.Context, db *gorm.DB, userID string) (workflowModelResolution, *workflowModelError) {
	config, err := modelconfig.LoadLLMConfig(ctx, db, userID)
	result := workflowModelResolution{Config: config, Public: workflowModelConfiguration(userID, config, err)}
	if err != nil {
		return result, &workflowModelError{"MODEL_CONFIG_LOAD_FAILED", "Could not load the task owner's system model settings."}
	}
	if workflowModelReady(config) {
		return result, nil
	}
	dynamic, err := modelprovider.FetchRoleIsDynamic(ctx, "llm")
	if err != nil {
		result.Public["error_code"] = "MODEL_CONFIG_CHECK_FAILED"
		return result, &workflowModelError{"MODEL_CONFIG_CHECK_FAILED", "Could not check the algorithm service model configuration."}
	}
	if dynamic {
		return result, &workflowModelError{"MODEL_NOT_CONFIGURED", fmt.Sprintf("No usable default chat model for user %s. Check Models and Services in this LazyMind instance.", userID)}
	}
	result.Public["ready"], result.Public["source"] = true, "runtime_config"
	delete(result.Public, "error_code")
	return result, nil
}

func workflowModelReady(config map[string]any) bool {
	llm, _ := config["llm"].(map[string]any)
	return stringFromMap(llm, "source") != "" && stringFromMap(llm, "model") != ""
}

func workflowModelConfiguration(userID string, config map[string]any, err error) map[string]any {
	result := map[string]any{
		"user_id": userID, "source": "system_settings", "ready": false,
		"settings_path": "/settings?section=models",
	}
	if err != nil {
		result["error_code"] = "MODEL_CONFIG_LOAD_FAILED"
		return result
	}
	if !workflowModelReady(config) {
		result["error_code"] = "MODEL_NOT_CONFIGURED"
		return result
	}
	llm := config["llm"].(map[string]any)
	result["ready"] = true
	result["provider"] = llm["source"]
	result["model"] = llm["model"]
	return result
}
