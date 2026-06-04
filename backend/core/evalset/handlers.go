package evalset

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/store"
)

func ListEvalSets(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	query := r.URL.Query()
	page := parsePositiveInt(query.Get("page"), 1)
	pageSize := parsePositiveInt(query.Get("page_size"), 10)
	resp, err := svc.List(r.Context(), userID, acl.ResolveUserGroupIDs(userID), ListFilter{
		Keyword:    query.Get("keyword"),
		DatasetIDs: parseDatasetIDsQuery(query),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		common.ReplyErr(w, "list failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, resp)
}

func CreateEvalSet(w http.ResponseWriter, r *http.Request) {
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

	var req CreateEvalSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := svc.Create(r.Context(), req, userID, userName)
	if err != nil {
		replyServiceError(w, err, "create eval set failed")
		return
	}
	common.ReplyOK(w, resp)
}

func GetEvalSet(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	evalSetID := strings.TrimSpace(common.PathVar(r, "eval_set_id"))
	if evalSetID == "" {
		common.ReplyErr(w, "invalid eval_set_id", http.StatusBadRequest)
		return
	}

	resp, err := svc.Get(r.Context(), evalSetID, userID, acl.ResolveUserGroupIDs(userID))
	if err != nil {
		replyServiceError(w, err, "query eval set failed")
		return
	}
	common.ReplyOK(w, resp)
}

func UpdateEvalSet(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	evalSetID := strings.TrimSpace(common.PathVar(r, "eval_set_id"))
	if evalSetID == "" {
		common.ReplyErr(w, "invalid eval_set_id", http.StatusBadRequest)
		return
	}

	var req UpdateEvalSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := svc.Update(r.Context(), evalSetID, req, userID, acl.ResolveUserGroupIDs(userID))
	if err != nil {
		replyServiceError(w, err, "update eval set failed")
		return
	}
	common.ReplyOK(w, resp)
}

func DeleteEvalSet(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	evalSetID := strings.TrimSpace(common.PathVar(r, "eval_set_id"))
	if evalSetID == "" {
		common.ReplyErr(w, "invalid eval_set_id", http.StatusBadRequest)
		return
	}

	if err := svc.Delete(r.Context(), evalSetID, userID, acl.ResolveUserGroupIDs(userID)); err != nil {
		replyServiceError(w, err, "delete eval set failed")
		return
	}
	common.ReplyOK(w, DeleteEvalSetResponse{Deleted: true})
}

func ListDatasetOptions(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	resp, err := svc.ListDatasetOptions(r.Context(), userID)
	if err != nil {
		common.ReplyErr(w, "list failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, resp)
}

func ListQuestionTypeOptions(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	common.ReplyOK(w, svc.ListQuestionTypeOptions())
}

func serviceForRequest(w http.ResponseWriter) (*Service, bool) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return nil, false
	}
	return NewService(db), true
}

func parsePositiveInt(raw string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < 1 {
		return fallback
	}
	return v
}

func parseDatasetIDsQuery(query map[string][]string) []string {
	values := make([]string, 0)
	for _, key := range []string{"dataset_ids", "dataset_ids[]"} {
		for _, raw := range query[key] {
			values = append(values, strings.Split(raw, ",")...)
		}
	}
	return normalizeDatasetIDs(values)
}

func replyServiceError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, errForbidden):
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, errEvalSetNotFound):
		common.ReplyErr(w, "eval set not found", http.StatusNotFound)
	case errors.Is(err, errEvalSetItemNotFound):
		common.ReplyErr(w, "eval set item not found", http.StatusNotFound)
	case errors.Is(err, errNoItemSelected):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	case strings.Contains(err.Error(), "required"),
		strings.Contains(err.Error(), "too long"),
		strings.Contains(err.Error(), "at least one field"),
		strings.Contains(err.Error(), "unsupported order_by"),
		strings.Contains(err.Error(), "invalid source"):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	default:
		common.ReplyErr(w, fallback, http.StatusInternalServerError)
	}
}
