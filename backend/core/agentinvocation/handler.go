package agentinvocation

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"lazymind/core/common"
)

type Handler struct{ Service *Service }

func (h Handler) Start(w http.ResponseWriter, r *http.Request) {
	owner, ok := requestOwner(w, r)
	if !ok {
		return
	}
	var input StartInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input); err != nil {
		common.ReplyErr(w, ErrInvalidInput.Error(), http.StatusBadRequest)
		return
	}
	input.ID = mux.Vars(r)["invocation_id"]
	input.ExternalRef = r.Header.Get("X-LazyMind-External-Ref")
	record, created, err := h.Service.Start(r.Context(), owner, input)
	if err != nil {
		writeError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"invocation": record, "created": created})
}

func (h Handler) Finish(w http.ResponseWriter, r *http.Request) {
	owner, ok := requestOwner(w, r)
	if !ok {
		return
	}
	var input FinishInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input); err != nil {
		common.ReplyErr(w, ErrInvalidInput.Error(), http.StatusBadRequest)
		return
	}
	record, err := h.Service.Finish(r.Context(), owner, mux.Vars(r)["invocation_id"], input)
	if err != nil {
		writeError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"invocation": record})
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	owner, ok := requestOwner(w, r)
	if !ok {
		return
	}
	pageSize := 0
	if value := strings.TrimSpace(r.URL.Query().Get("page_size")); value != "" {
		var err error
		pageSize, err = strconv.Atoi(value)
		if err != nil {
			common.ReplyErr(w, ErrInvalidInput.Error(), http.StatusBadRequest)
			return
		}
	}
	page, err := h.Service.List(r.Context(), owner, ListQuery{
		ToolName: r.URL.Query().Get("tool_name"), Status: r.URL.Query().Get("status"),
		ClientName: r.URL.Query().Get("client_name"), WorkflowID: r.URL.Query().Get("workflow_id"),
		SessionID: r.URL.Query().Get("session_id"), ExternalRef: r.URL.Query().Get("external_ref"),
		PageToken: r.URL.Query().Get("page_token"), PageSize: pageSize,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	common.ReplyOK(w, page)
}

func requestOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner := strings.TrimSpace(common.UserID(r))
	if owner == "" {
		common.ReplyErr(w, "missing x-user-id", http.StatusUnauthorized)
		return "", false
	}
	return owner, true
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrNotFound):
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrConflict):
		common.ReplyErr(w, err.Error(), http.StatusConflict)
	default:
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
	}
}
