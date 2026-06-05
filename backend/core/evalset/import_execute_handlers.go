package evalset

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/store"
)

func CreateEvalSetByImport(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var req CreateEvalSetByImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := svc.CreateByImport(r.Context(), req, userID, userName)
	if err != nil {
		replyImportServiceError(w, err, "create eval set import failed")
		return
	}
	common.ReplyOK(w, resp)
}

func AppendEvalSetImport(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	evalSetID := strings.TrimSpace(common.PathVar(r, "eval_set_id"))
	if evalSetID == "" {
		common.ReplyErr(w, "invalid eval_set_id", http.StatusBadRequest)
		return
	}

	var req AppendEvalSetImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := svc.AppendImport(r.Context(), evalSetID, req, userID, userName, acl.ResolveUserGroupIDs(userID))
	if err != nil {
		replyImportServiceError(w, err, "append eval set import failed")
		return
	}
	common.ReplyOK(w, resp)
}

func GetEvalSetImportTask(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	taskID := strings.TrimSpace(common.PathVar(r, "task_id"))
	if taskID == "" {
		common.ReplyErr(w, "invalid task_id", http.StatusBadRequest)
		return
	}

	resp, err := svc.GetImportTask(r.Context(), taskID, userID, acl.ResolveUserGroupIDs(userID))
	if err != nil {
		replyImportServiceError(w, err, "query eval set import task failed")
		return
	}
	common.ReplyOK(w, resp)
}

func replyImportServiceError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, errInvalidImportToken):
		common.ReplyErr(w, "invalid import_token", http.StatusBadRequest)
	case errors.Is(err, errImportTaskNotFound):
		common.ReplyErr(w, "import task not found", http.StatusNotFound)
	case errors.Is(err, errForbidden):
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, errEvalSetNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, "eval set not found", http.StatusNotFound)
	case strings.Contains(err.Error(), "required"),
		strings.Contains(err.Error(), "too long"):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	default:
		common.ReplyErr(w, fallback, http.StatusInternalServerError)
	}
}
