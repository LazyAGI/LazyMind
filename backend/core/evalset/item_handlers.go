package evalset

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/store"
)

func ListEvalSetItems(w http.ResponseWriter, r *http.Request) {
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

	groupIDs := acl.ResolveUserGroupIDs(userID)
	evalSet, err := svc.requireEvalSetPermission(r.Context(), evalSetID, userID, groupIDs, acl.PermissionEvalSetRead)
	if err != nil {
		replyServiceError(w, err, "query eval set failed")
		return
	}

	query := r.URL.Query()
	resp, err := svc.ListItems(r.Context(), evalSet, ListEvalSetItemsFilter{
		Keyword:      query.Get("keyword"),
		QuestionType: query.Get("question_type"),
		Source:       query.Get("source"),
		Page:         parsePositiveInt(query.Get("page"), 1),
		PageSize:     parsePositiveInt(query.Get("page_size"), 20),
		OrderBy:      query.Get("order_by"),
	})
	if err != nil {
		replyServiceError(w, err, "list eval set items failed")
		return
	}
	common.ReplyOK(w, resp)
}

func CreateEvalSetItem(w http.ResponseWriter, r *http.Request) {
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

	if _, err := svc.requireEvalSetPermission(r.Context(), evalSetID, userID, acl.ResolveUserGroupIDs(userID), acl.PermissionEvalSetWrite); err != nil {
		replyServiceError(w, err, "query eval set failed")
		return
	}

	var req CreateEvalSetItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := svc.CreateItem(r.Context(), evalSetID, req, userID, userName)
	if err != nil {
		replyServiceError(w, err, "create eval set item failed")
		return
	}
	common.ReplyOK(w, resp)
}

func UpdateEvalSetItem(w http.ResponseWriter, r *http.Request) {
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
	itemID := strings.TrimSpace(common.PathVar(r, "item_id"))
	if evalSetID == "" {
		common.ReplyErr(w, "invalid eval_set_id", http.StatusBadRequest)
		return
	}
	if itemID == "" {
		common.ReplyErr(w, "item_id required", http.StatusBadRequest)
		return
	}

	if _, err := svc.requireEvalSetPermission(r.Context(), evalSetID, userID, acl.ResolveUserGroupIDs(userID), acl.PermissionEvalSetWrite); err != nil {
		replyServiceError(w, err, "query eval set failed")
		return
	}

	var req UpdateEvalSetItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := svc.UpdateItem(r.Context(), evalSetID, itemID, req)
	if err != nil {
		replyServiceError(w, err, "update eval set item failed")
		return
	}
	common.ReplyOK(w, resp)
}

func DeleteEvalSetItem(w http.ResponseWriter, r *http.Request) {
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
	itemID := strings.TrimSpace(common.PathVar(r, "item_id"))
	if evalSetID == "" {
		common.ReplyErr(w, "invalid eval_set_id", http.StatusBadRequest)
		return
	}
	if itemID == "" {
		common.ReplyErr(w, "item_id required", http.StatusBadRequest)
		return
	}

	if _, err := svc.requireEvalSetPermission(r.Context(), evalSetID, userID, acl.ResolveUserGroupIDs(userID), acl.PermissionEvalSetWrite); err != nil {
		replyServiceError(w, err, "query eval set failed")
		return
	}
	if err := svc.DeleteItem(r.Context(), evalSetID, itemID); err != nil {
		replyServiceError(w, err, "delete eval set item failed")
		return
	}
	common.ReplyOK(w, DeleteEvalSetItemResponse{Deleted: true})
}

func BatchDeleteEvalSetItems(w http.ResponseWriter, r *http.Request) {
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

	var req BatchDeleteEvalSetItemsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if len(normalizeItemIDs(req.ItemIDs)) == 0 {
		common.ReplyErr(w, errNoItemSelected.Error(), http.StatusBadRequest)
		return
	}
	if _, err := svc.requireEvalSetPermission(r.Context(), evalSetID, userID, acl.ResolveUserGroupIDs(userID), acl.PermissionEvalSetWrite); err != nil {
		replyServiceError(w, err, "query eval set failed")
		return
	}
	deletedCount, err := svc.BatchDeleteItems(r.Context(), evalSetID, req)
	if err != nil {
		replyServiceError(w, err, "batch delete eval set items failed")
		return
	}
	common.ReplyOK(w, BatchDeleteEvalSetItemsResponse{DeletedCount: deletedCount})
}
