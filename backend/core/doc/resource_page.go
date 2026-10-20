package doc

import (
	"errors"
	"lazymind/core/common"
	"lazymind/core/store"
	"net/http"
)

// These page handlers are registered behind document.write middleware. The service
// independently checks tenant, source policy and resource ACL for every mutation.
func resourcePageService(r *http.Request) (*ResourceService, ResourceOperationContext, error) {
	caller := datasetCatalogCallerFromRequest(r)
	caller.UserName = store.UserName(r)
	caller.Permissions = []string{"document.write"}
	service, err := NewResourceService(ResourceServiceDeps{DB: store.DB()})
	return service, ResourceOperationContext{Caller: caller}, err
}
func replyResourceError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	var typed *ResourceError
	if errors.As(err, &typed) {
		switch typed.Code {
		case "NOT_FOUND":
			code = http.StatusNotFound
		case "FORBIDDEN":
			code = http.StatusForbidden
		case "CONFLICT":
			code = http.StatusConflict
		case "INVALID_ARGUMENT":
			code = http.StatusBadRequest
		case "OPERATION_TOO_LARGE":
			code = http.StatusRequestEntityTooLarge
		case "UNAVAILABLE":
			code = http.StatusServiceUnavailable
		}
	}
	common.ReplyErr(w, err.Error(), code)
}
