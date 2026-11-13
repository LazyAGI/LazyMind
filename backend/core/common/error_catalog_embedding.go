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
		{2005089, http.StatusConflict, "Clear the old text vectors before changing the embedding model; source documents and chunks remain", []string{"embedding_switch_requires_index_clear", "embedding model is in use; its knowledge-base vectors must be cleared before removal", "embedding model is encoding knowledge bases; clear its vectors before changing this runtime configuration"}},
		{2005090, http.StatusBadGateway, "Failed to load the embedding configuration for indexing", []string{"cannot load model configuration for indexed processing", "cannot resolve global embedding model", "cannot check embedding model for indexed processing"}},
		{2005091, http.StatusBadRequest, "Configure a text embedding model before indexing this knowledge base", []string{"text embedding model is required for indexed processing"}},
		{2005092, http.StatusBadRequest, "Model catalog entries must be objects", []string{"model entry must be an object"}},
		{2005093, http.StatusBadRequest, "Explicit confirmation is required to clear text vectors", []string{"explicit chunk clearing confirmation is required"}},
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

func init() {
	registerAdditionalErrorAlias("query processing details failed", "Failed to read document processing details", http.StatusInternalServerError, 2004970)
	registerAdditionalErrorAlias("select one to twenty documents", "Select between one and twenty documents", http.StatusBadRequest, 2004971)
	registerAdditionalErrorAlias("rag component is required for indexing", "Enable the RAG component before indexing", http.StatusConflict, 2004972)
	registerAdditionalErrorAlias("document not found or duplicate selection", "A selected document is missing or selected more than once", http.StatusBadRequest, 2004973)
}

func init() {
	registerAdditionalErrorAlias("account identity unavailable", "forbidden", http.StatusForbidden, ErrCodeForbidden)
	registerAdditionalErrorAlias("query libraries failed", "Failed to query datasets", http.StatusInternalServerError, 2000522)
	registerAdditionalErrorAlias("query processing tasks failed", "Failed to query tasks", http.StatusInternalServerError, 2000505)
	registerAdditionalErrorAlias("query legacy document types failed", "Failed to read document processing details", http.StatusInternalServerError, 2004970)
	registerAdditionalErrorAlias("folder cannot be indexed", "Select documents instead of folders for indexing", http.StatusBadRequest, 2004974)
	registerAdditionalErrorAlias("processing status temporarily unavailable", "Document processing status is temporarily unavailable; refresh and retry", http.StatusServiceUnavailable, 2004975)
	registerAdditionalErrorAlias("index freshness could not be checked", "Unable to verify index freshness; retry later", http.StatusServiceUnavailable, 2004976)
	registerAdditionalErrorAlias("向量索引需要本地知识库组件，请前往设置中的系统工具安装并启用。原文件和解析结果不受影响。", "Enable the RAG component before indexing", http.StatusConflict, 2004972)
	registerAdditionalErrorAlias("mineru tls certificate hostname mismatch; check the endpoint, dns or proxy certificate", "MinerU certificate hostname mismatch; check the endpoint, DNS or proxy certificate", http.StatusBadGateway, 2004977)
	registerAdditionalErrorAlias("mineru tls certificate verification failed; check the certificate trust chain", "MinerU certificate verification failed; check the certificate trust chain", http.StatusBadGateway, 2004978)
	registerAdditionalErrorAlias("mineru request timed out; retry when the service is reachable", "MinerU request timed out; check connectivity and retry", http.StatusGatewayTimeout, 2004979)
	registerAdditionalErrorAlias("mineru network connection failed; check dns, proxy and service connectivity", "MinerU connection failed; check DNS, proxy and service connectivity", http.StatusBadGateway, 2004980)
}
