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
		{2005084, http.StatusForbidden, "Only an administrator can change the global embedding model", []string{"admin role required for global embedding selection"}},
		{2005085, http.StatusInternalServerError, "Failed to check embedding model compatibility", []string{"embedding preview failed"}},
		{2005086, http.StatusBadRequest, "This embedding source cannot be fingerprinted", []string{"embedding_signature_unsupported"}},
		{2005087, http.StatusBadGateway, "The embedding signature check failed", []string{"embedding signature failed", "embedding_signature_failed"}},
		{2005088, http.StatusConflict, "The embedding selection is global and cannot be shared separately", []string{"embedding sharing follows the global selection"}},
		{2005089, http.StatusConflict, "Clear the old text vectors before changing the embedding model; source documents and chunks remain", []string{"embedding_switch_requires_index_clear", "embedding model is in use; its knowledge-base vectors must be cleared before removal", "embedding model is encoding knowledge bases; clear its vectors before changing this runtime configuration"}},
		{2005090, http.StatusBadGateway, "Failed to load the embedding configuration for indexing", []string{"cannot load model configuration for indexed processing", "cannot resolve global embedding model", "cannot check embedding model for indexed processing"}},
		{2005091, http.StatusBadRequest, "Configure a text embedding model before indexing this knowledge base", []string{"text embedding model is required for indexed processing"}},
		{2005092, http.StatusBadRequest, "Model catalog entries must be objects", []string{"model entry must be an object"}},
	} {
		for _, source := range item.sources {
			registerAdditionalErrorAlias(source, item.message, item.status, item.code)
		}
	}
}

func init() {
	registerAdditionalErrorAlias("query processing details failed", "Failed to read document processing details", http.StatusInternalServerError, 2004970)
	registerAdditionalErrorAlias("select one to twenty documents", "Select between one and twenty documents", http.StatusBadRequest, 2004971)
	registerAdditionalErrorAlias("rag component is required for indexing", "Enable the RAG component before indexing", http.StatusConflict, 2004972)
	registerAdditionalErrorAlias("document not found or duplicate selection", "A selected document is missing or selected more than once", http.StatusBadRequest, 2004973)
	registerAdditionalErrorAlias("admin role required", "Administrator permission is required", http.StatusForbidden, 2004974)
	registerAdditionalErrorAlias("explicit chunk clearing confirmation is required", "Explicit confirmation is required to clear text vectors", http.StatusBadRequest, 2004975)
	registerAdditionalErrorAlias("embedding state unavailable", "Embedding state is unavailable; retry shortly", http.StatusServiceUnavailable, 2004976)
	registerAdditionalErrorAlias("document changed during parsing; retry manually", "The document changed during parsing; retry manually", http.StatusConflict, 2004977)
	registerAdditionalErrorAlias("knowledge index changed; retry after model switching finishes", "The index changed; retry after the model switch completes", http.StatusConflict, 2004978)
	registerAdditionalErrorAlias("embedding_index_busy", "Text vector indexing or cleanup is in progress; retry after it finishes", http.StatusConflict, 2004979)
	registerAdditionalErrorAlias("embedding_cleanup_failed", "Text vector cleanup failed; retry in Index management", http.StatusConflict, 2004980)
}
