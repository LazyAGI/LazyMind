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
