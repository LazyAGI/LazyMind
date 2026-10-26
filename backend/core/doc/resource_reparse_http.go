package doc

import (
	"lazymind/core/common"
	"lazymind/core/store"
	"net/http"
	"strings"
)

// ReparseDocument is the page adapter of the same explicit retry used by Agent tools.
func ReparseDocument(w http.ResponseWriter, r *http.Request) {
	caller, err := resolveDatasetCatalogCallerFromRequest(r)
	if err != nil {
		common.ReplyErr(w, "permission denied", http.StatusForbidden)
		return
	}
	caller.Permissions = []string{"document.write"} // route middleware verified this capability
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 {
		common.ReplyErr(w, "Idempotency-Key required (max 128 characters)", http.StatusBadRequest)
		return
	}
	service, err := NewResourceService(ResourceServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
	if err != nil {
		common.ReplyErr(w, "retry service unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := service.Reparse(r.Context(), ResourceOperationContext{Caller: caller, OperationKey: "page-reparse:" + key}, ReparseDocumentsRequest{KnowledgeBaseID: datasetIDFromPath(r), DocumentIDs: []string{documentIDFromPath(r)}})
	if err != nil {
		replyResourceError(w, err)
		return
	}
	common.ReplyJSON(w, result)
}
