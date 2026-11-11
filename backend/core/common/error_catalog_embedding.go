package common

import "net/http"

func init() {
	for _, item := range []struct {
		code    int
		status  int
		message string
		sources []string
	}{
		{2005083, http.StatusInternalServerError, "Failed to read the text-vector index state", []string{"query embedding index state failed"}},
		{2005084, http.StatusForbidden, "Only an administrator can change the global embedding model", []string{"admin role required for global embedding selection", "admin role required"}},
		{2005085, http.StatusInternalServerError, "Failed to check embedding model compatibility", []string{"embedding preview failed"}},
		{2005086, http.StatusBadRequest, "This embedding source cannot be fingerprinted", []string{"embedding_signature_unsupported"}},
		{2005087, http.StatusBadGateway, "The embedding signature check failed", []string{"embedding signature failed", "embedding_signature_failed"}},
		{2005088, http.StatusConflict, "The embedding selection is global and cannot be shared separately", []string{"embedding sharing follows the global selection"}},
		{2005089, http.StatusConflict, "Confirm clearing all knowledge-base chunks and vectors before changing the embedding model; original files remain for manual re-parsing", []string{"embedding_switch_requires_index_clear", "embedding model is in use; its knowledge-base vectors must be cleared before removal", "embedding model is encoding knowledge bases; clear its vectors before changing this runtime configuration"}},
		{2005090, http.StatusBadGateway, "Failed to load the embedding configuration for indexing", []string{"cannot load model configuration for indexed processing", "cannot resolve global embedding model", "cannot check embedding model for indexed processing"}},
		{2005091, http.StatusBadRequest, "Configure a text embedding model before indexing this knowledge base", []string{"text embedding model is required for indexed processing"}},
		{2005092, http.StatusBadRequest, "Model catalog entries must be objects", []string{"model entry must be an object"}},
		{2005093, http.StatusBadRequest, "Explicit confirmation is required to clear knowledge-base chunks", []string{"explicit chunk clearing confirmation is required"}},
		{2005094, http.StatusServiceUnavailable, "The embedding model state is unavailable; retry later", []string{"embedding state unavailable"}},
		{2005095, http.StatusConflict, "The document changed during parsing; retry manually", []string{"document changed during parsing; retry manually"}},
		{2005096, http.StatusConflict, "The knowledge index changed; retry after model switching finishes", []string{"knowledge index changed; retry after model switching finishes"}},
		{2005097, http.StatusConflict, "The knowledge index is busy or has changed; refresh and retry", []string{"embedding_index_busy"}},
		{2005098, http.StatusConflict, "Knowledge-base cleanup is incomplete; retry clearing before switching models", []string{"embedding_cleanup_failed"}},
	} {
		for _, source := range item.sources {
			registerAdditionalErrorAlias(source, item.message, item.status, item.code)
		}
	}
}
