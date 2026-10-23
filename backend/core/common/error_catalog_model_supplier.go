package common

import (
	"net/http"
	"strings"
)

func init() {
	for _, group := range []struct {
		code    int
		status  int
		message string
		sources []string
	}{
		{2005000, http.StatusBadRequest, "Name must contain 1 to 255 bytes", []string{"name must contain 1 to 255 bytes"}},
		{2005001, http.StatusNotFound, "Source model provider not found", []string{"source model provider not found"}},
		{2005002, http.StatusBadRequest, "API address must be an HTTP(S) API root", []string{"base_url must be an http(s) api root"}},
		{2005003, http.StatusInternalServerError, "Failed to create the custom supplier", []string{"create custom provider failed"}},
		{2005004, http.StatusBadRequest, "Only custom model suppliers can be renamed or deleted", []string{"only custom model suppliers can be renamed or deleted"}},
		{2005005, http.StatusInternalServerError, "Failed to rename the custom supplier", []string{"rename custom provider failed"}},
		{2005006, http.StatusInternalServerError, "Failed to delete the custom supplier", []string{"delete custom provider failed"}},
		{2005007, http.StatusInternalServerError, "Failed to initialize the enabled API key", []string{"initialize enabled api key failed"}},
		{2005008, http.StatusBadRequest, "Saved API keys exceed credential limits", []string{"api_key pool exceeds credential limits"}},
		{2005009, http.StatusInternalServerError, "Failed to reload supplier credentials", []string{"reload group failed"}},
		{2005010, http.StatusInternalServerError, "Failed to resolve the enabled API key", []string{"resolve enabled api key failed"}},
		{2005011, http.StatusInternalServerError, "Failed to save the model", []string{"save model failed"}},
		{2005012, http.StatusBadRequest, "Enter one API key of at most 512 bytes", []string{"api_key must be a single key of at most 512 bytes"}},
		{2005013, http.StatusBadGateway, "API key verification failed", []string{"api key verification failed"}},
		{2005014, http.StatusInternalServerError, "Failed to update the API key", []string{"update api key failed"}},
		{2005015, http.StatusBadRequest, "Credential group and key must both be selected or both be empty", []string{"group_id and key_id must both be supplied or both be empty"}},
		{2005016, http.StatusBadRequest, "Only model suppliers support a shared API key selection", []string{"only model suppliers have a supplier-wide api key"}},
		{2005017, http.StatusInternalServerError, "Failed to update the enabled API key", []string{"update enabled api key failed"}},
		{2005018, http.StatusBadRequest, "Only model suppliers support a shared API address", []string{"only model suppliers have a shared endpoint"}},
		{2005019, http.StatusBadRequest, "API address must be an HTTP(S) API root without credentials, query, fragment, or request path", []string{"base_url must be an http(s) api root without credentials, query, fragment, or request path"}},
		{2005020, http.StatusInternalServerError, "Failed to update the supplier API address", []string{"update model provider endpoint failed"}},
		{2005021, http.StatusBadRequest, "Only model suppliers support request configuration", []string{"only model suppliers have request configuration"}},
		{2005022, http.StatusBadRequest, "The current runtime does not support this request protocol", []string{"request protocol is not supported by the current runtime"}},
		{2005023, http.StatusBadRequest, "Request API address must be an HTTP(S) API root without credentials, query, or fragment", []string{"request endpoint must be an http(s) api root without credentials, query or fragment"}},
		{2005024, http.StatusInternalServerError, "Failed to save request configuration", []string{"save request configuration failed"}},
		{2005025, http.StatusConflict, "The model supplier already has credential storage", []string{"model supplier already has credential storage"}},
		{2005026, http.StatusConflict, "This model name already exists in the supplier", []string{"model name already exists in this supplier"}},
		{2005027, http.StatusBadRequest, "The model supplier has no enabled API key", []string{"model supplier has no enabled api key"}},
		{2005028, http.StatusBadRequest, "The model supplier API key requires verification", []string{"model supplier api key requires verification"}},
		{2005029, http.StatusConflict, "The supplier configuration changed; reload and retry", []string{"connection changed; reload and retry"}},
		{2005030, http.StatusBadRequest, "Anthropic supports chat and vision models only; remove incompatible models before changing the default protocol", []string{"anthropic currently supports chat and vision models only; remove incompatible models before changing the default protocol"}},
		{2005031, http.StatusBadRequest, "The default request API address is empty", []string{"default request endpoint is empty"}},
		{2005032, http.StatusInternalServerError, "The upstream model catalog has no providers", []string{"upstream catalog %s has no providers"}},
		{2005033, http.StatusInternalServerError, "Failed to load an upstream model provider", []string{"upstream provider %q"}},
		{2005034, http.StatusBadRequest, "Unsupported model type", []string{"unsupported model_type"}},
		{2005035, http.StatusBadRequest, "The supplier protocol does not support this model type", []string{"model type is not supported by the supplier protocol"}},
		{2005036, http.StatusBadRequest, "Model ID must contain 1 to 512 bytes", []string{"model name must contain 1 to 512 bytes"}},
		{2005037, http.StatusConflict, "Change the incompatible default model selection before editing this model", []string{"model update would invalidate a default selection; change the selection first"}},
		{2005038, http.StatusConflict, "Change the fixed conversation model before editing this model", []string{"model update would invalidate a conversation selection; change the conversation model first"}},
		{2005039, http.StatusConflict, "This embedding model is in use; change default selections and downgrade indexed knowledge bases before editing its identity", []string{"embedding model identity is in use; change the default model and downgrade indexed knowledge bases before editing"}},
		{2005040, http.StatusBadRequest, "The selected model does not support this default role", []string{"selected model does not support this default model role"}},
		{2005050, http.StatusBadRequest, "Invalid advanced request configuration", []string{"invalid advanced request configuration", "invalid request configuration"}},
		{2005051, http.StatusBadRequest, "Authentication, protocol and transport headers are managed by LazyMind", []string{"authentication, protocol and transport headers are managed by lazymind"}},
		{2005052, http.StatusBadRequest, "Model, input, output format and streaming fields are managed by LazyMind", []string{"model, input, output format and streaming fields are managed by lazymind"}},
		{2005053, http.StatusInternalServerError, "Failed to read advanced request configuration", []string{"read advanced request configuration failed"}},
		{2005054, http.StatusBadRequest, "Request configuration exceeds credential limits", []string{"request configuration exceeds credential limits"}},
		{2005060, http.StatusBadRequest, "Model settings exceed the size limit", []string{"model settings exceed size limit"}},
		{2005061, http.StatusBadRequest, "Invalid model settings", []string{"invalid model settings"}},
		{2005062, http.StatusBadRequest, "Model features require a chat model", []string{"model features require a chat model"}},
		{2005063, http.StatusBadRequest, "Cannot enable a feature declared unsupported", []string{"cannot enable a feature declared unsupported"}},
		{2005064, http.StatusBadRequest, "Vision model type requires vision support", []string{"vision model type requires vision support"}},
		{2005065, http.StatusBadRequest, "Embedding options require a text embedding model", []string{"embedding options require a text embedding model"}},
		{2005066, http.StatusBadRequest, "Embedding prefix exceeds 2048 characters", []string{"embedding prefix exceeds 2048 characters"}},
		{2005067, http.StatusBadRequest, "Embedding batch size must be between 1 and 2048", []string{"embedding batch size must be between 1 and 2048"}},
		{2005070, http.StatusBadRequest, "A supplier name or logo is required", []string{"name or logo_data_url is required"}},
		{2005071, http.StatusInternalServerError, "Failed to load model supplier identities", []string{"query model provider identities failed"}},
		{2005072, http.StatusBadRequest, "Add between 1 and 1000 models at a time", []string{"models must contain between 1 and 1000 entries"}},
		{2005073, http.StatusInternalServerError, "Failed to save models", []string{"save models failed"}},
		{2005074, http.StatusBadGateway, "Supplier connection check failed", []string{"provider connection check failed"}},
		{2005075, http.StatusBadRequest, "Token limits require a chat or vision model", []string{"model token limits are only supported for llm or vlm models"}},
		{2005076, http.StatusBadRequest, "The supplier logo must be a PNG of at most 128 × 128 pixels and 96 KiB", []string{"logo_data_url must be a valid png of at most 128x128 pixels and 96 kib"}},
		{2005077, http.StatusConflict, "The supplier configuration changed during synchronization; reload and retry", []string{"provider configuration changed during service synchronization"}},
		{2005078, http.StatusBadRequest, "Token limits must be positive integers and may use a K or M suffix", []string{"model token limits must be positive integers or use a k or m suffix"}},
		{2005079, http.StatusBadRequest, "Input and output token limits must not exceed the context window", []string{"model input and output token limits must not exceed the context window"}},
	} {
		for _, source := range group.sources {
			if strings.Contains(source, "%") {
				registerAdditionalErrorPattern(source, group.message, group.status, group.code)
			} else {
				registerAdditionalErrorAlias(source, group.message, group.status, group.code)
			}
		}
	}
}
