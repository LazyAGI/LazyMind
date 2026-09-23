package externalcapability

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"lazymind/core/common"
	"lazymind/core/store"
)

func List(w http.ResponseWriter, r *http.Request) {
	service := New(store.DB(), nil)
	result, err := service.Inventory(r.Context(), store.UserID(r), r.URL.Query().Get("agent"))
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.ReplyOK(w, result)
}

func Update(w http.ResponseWriter, r *http.Request) {
	var request GrantUpdate
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := New(store.DB(), nil).SetGrant(r.Context(), store.UserID(r), request); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.ReplyOK(w, request)
}

func ListInvocations(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if value, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && value > 0 && value <= 100 {
		limit = value
	}
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agent != "" {
		var err error
		agent, err = NormalizeAgent(agent)
		if err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	result, err := New(store.DB(), nil).InvocationHistory(
		r.Context(), strings.TrimSpace(store.UserID(r)), agent, limit,
	)
	if err != nil {
		common.ReplyErr(w, "list invocation records failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, result)
}
