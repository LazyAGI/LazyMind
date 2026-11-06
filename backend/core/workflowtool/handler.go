package workflowtool

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/workflow/controlstore"
	workflowstore "lazymind/core/workflow/store"
)

type Handler struct{ Service *Service }

func reply(w http.ResponseWriter, status int, result any, err *ToolError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		ContractVersion string     `json:"contract_version"`
		OK              bool       `json:"ok"`
		Result          any        `json:"result,omitempty"`
		Error           *ToolError `json:"error,omitempty"`
	}{"workflow.v1", err == nil, result, err})
}

func serviceError(w http.ResponseWriter, err error) {
	var controlled *controlstore.Error
	var tool *ToolError
	switch {
	case errors.As(err, &controlled):
		reply(w, controlled.HTTPStatus(), nil, &ToolError{Code: controlled.Code, Message: controlled.Message})
	case errors.As(err, &tool):
		reply(w, http.StatusConflict, nil, tool)
	case errors.Is(err, workflowstore.ErrPermissionDenied):
		reply(w, http.StatusForbidden, nil, &ToolError{Code: "PERMISSION_DENIED", Message: "Workflow belongs to another owner"})
	case errors.Is(err, workflowstore.ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		reply(w, http.StatusNotFound, nil, &ToolError{Code: "EXECUTION_NOT_FOUND", Message: "Workflow execution is unavailable"})
	default:
		reply(w, http.StatusServiceUnavailable, nil, &ToolError{Code: "WORKFLOW_TOOL_FAILED", Message: "Workflow tool service failed; check the configuration in LazyMind", Retryable: true})
	}
}

func decode(w http.ResponseWriter, r *http.Request, value any) (string, bool) {
	owner := strings.TrimSpace(r.Header.Get("X-User-Id"))
	if owner == "" {
		reply(w, http.StatusUnauthorized, nil, &ToolError{Code: "IDENTITY_REQUIRED", Message: "Authenticated user identity is required"})
		return "", false
	}
	if version := r.Header.Get("Workflow-Contract-Version"); version != "" && version != "workflow.v1" {
		reply(w, http.StatusUnprocessableEntity, nil, &ToolError{Code: "CONTRACT_VERSION_UNSUPPORTED", Message: "Supported version is workflow.v1"})
		return "", false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		reply(w, http.StatusUnprocessableEntity, nil, &ToolError{Code: "INVALID_TOOL_CALL", Message: "Invalid tool request"})
		return "", false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		reply(w, http.StatusUnprocessableEntity, nil, &ToolError{Code: "INVALID_TOOL_CALL", Message: "A single tool request is required"})
		return "", false
	}
	return owner, true
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExecutionHandle string `json:"execution_handle"`
	}
	owner, ok := decode(w, r, &input)
	if !ok {
		return
	}
	vars := mux.Vars(r)
	result, err := h.Service.List(r.Context(), owner, vars["session_id"], vars["attempt_id"], input.ExecutionHandle)
	if err != nil {
		serviceError(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"tools": result}, nil)
}

func (h Handler) Call(w http.ResponseWriter, r *http.Request) {
	var input CallInput
	owner, ok := decode(w, r, &input)
	if !ok {
		return
	}
	vars := mux.Vars(r)
	result, err := h.Service.Call(withDataToolCaller(r.Context(), r.Header), owner, vars["session_id"], vars["attempt_id"], input)
	if err != nil {
		serviceError(w, err)
		return
	}
	// Failed capability calls are persisted as receipts, but remain MCP errors.
	if result.Error != nil {
		reply(w, http.StatusConflict, nil, result.Error)
		return
	}
	reply(w, http.StatusOK, result, nil)
}
