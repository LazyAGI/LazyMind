package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"lazymind/core/common"
	"lazymind/core/store"
)

func List(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	userID := strings.TrimSpace(store.UserID(r))
	resp, err := ListServers(r.Context(), db, userID, parseListServersRequest(r))
	if err != nil {
		common.ReplyErr(w, "list mcp servers failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, resp)
}

func parseListServersRequest(r *http.Request) ListServersRequest {
	q := r.URL.Query()
	return ListServersRequest{
		Keyword:  strings.TrimSpace(q.Get("keyword")),
		Page:     parsePositiveListServersInt(q.Get("page"), 1),
		PageSize: parsePositiveListServersInt(q.Get("page_size"), 0),
	}
}

func parsePositiveListServersInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func Create(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var req CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := CreateServer(r.Context(), db, req, userID, store.UserName(r))
	if err != nil {
		replyError(w, err, "create mcp server failed")
		return
	}
	common.ReplyOK(w, resp)
}

func Get(w http.ResponseWriter, r *http.Request) {
	resp, err := GetServer(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "id"))
	if err != nil {
		replyError(w, err, "get mcp server failed")
		return
	}
	common.ReplyOK(w, resp)
}

func Update(w http.ResponseWriter, r *http.Request) {
	var req UpdateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := UpdateServer(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "id"), req)
	if err != nil {
		replyError(w, err, "update mcp server failed")
		return
	}
	common.ReplyOK(w, resp)
}

func Delete(w http.ResponseWriter, r *http.Request) {
	if err := DeleteServer(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "id")); err != nil {
		replyError(w, err, "delete mcp server failed")
		return
	}
	common.ReplyOK(w, map[string]any{"id": strings.TrimSpace(common.PathVar(r, "id"))})
}

func Check(w http.ResponseWriter, r *http.Request) {
	resp, err := CheckServer(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "id"))
	if err != nil {
		replyError(w, err, "check mcp server failed")
		return
	}
	common.ReplyOK(w, resp)
}

func Discover(w http.ResponseWriter, r *http.Request) {
	resp, err := DiscoverServer(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "id"))
	if err != nil {
		replyError(w, err, "discover mcp tools failed")
		return
	}
	common.ReplyOK(w, resp)
}

func UpdateTools(w http.ResponseWriter, r *http.Request) {
	var req UpdateToolsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	resp, err := UpdateServerTools(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "id"), req)
	if err != nil {
		replyError(w, err, "update mcp tools failed")
		return
	}
	common.ReplyOK(w, resp)
}

func replyError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, errBadRequest):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, errForbidden):
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, errNotFound):
		common.ReplyErr(w, "mcp server not found", http.StatusNotFound)
	default:
		common.ReplyErr(w, fallback, http.StatusInternalServerError)
	}
}
