package hosted

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"lazymind/core/workflow/attempt"
	workflowstore "lazymind/core/workflow/store"
)

const ContractVersion = "workflow.v1"

type Handler struct{ Service *Service }

type responseEnvelope struct {
	ContractVersion string         `json:"contract_version"`
	OK              bool           `json:"ok"`
	Result          any            `json:"result,omitempty"`
	Error           *responseError `json:"error,omitempty"`
}

type responseError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func reply(w http.ResponseWriter, status int, result any, protocolErr *responseError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(responseEnvelope{
		ContractVersion: ContractVersion, OK: protocolErr == nil, Result: result, Error: protocolErr,
	})
}

func owner(w http.ResponseWriter, r *http.Request) (string, bool) {
	value := strings.TrimSpace(r.Header.Get("X-User-Id"))
	if value == "" {
		reply(w, http.StatusBadRequest, nil, &responseError{Code: "IDENTITY_REQUIRED", Message: "X-User-Id is required"})
		return "", false
	}
	version := strings.TrimSpace(r.Header.Get("Workflow-Contract-Version"))
	if version != "" && version != ContractVersion {
		reply(w, http.StatusUnprocessableEntity, nil, &responseError{Code: "CONTRACT_VERSION_UNSUPPORTED", Message: "supported version is workflow.v1"})
		return "", false
	}
	return value, true
}

func writeServiceError(w http.ResponseWriter, err error) {
	var protocol *ProtocolError
	switch {
	case errors.As(err, &protocol):
		status := http.StatusConflict
		if protocol.Code == "INVALID_EXECUTION" || protocol.Code == "INVALID_OUTCOME" || protocol.Code == "INVALID_ARTIFACT" ||
			protocol.Code == "OUTPUT_SLOT_UNDECLARED" || protocol.Code == "DUPLICATE_ARTIFACT" || protocol.Code == "TOO_MANY_ARTIFACTS" ||
			protocol.Code == "REQUIRED_OUTPUT_MISSING" {
			status = http.StatusUnprocessableEntity
		} else if protocol.Code == "EXECUTION_NOT_FOUND" {
			status = http.StatusNotFound
		}
		retryable := protocol.Code == attempt.CodeLeaseLost || protocol.Code == "EXECUTION_NOT_CLAIMABLE" || protocol.Code == "WORKFLOW_PROJECTION_FAILED"
		reply(w, status, nil, &responseError{Code: protocol.Code, Message: protocol.Message, Retryable: retryable})
	case errors.Is(err, workflowstore.ErrNotFound):
		reply(w, http.StatusNotFound, nil, &responseError{Code: "WORKFLOW_SESSION_NOT_FOUND", Message: "workflow session was not found"})
	case errors.Is(err, workflowstore.ErrPermissionDenied):
		reply(w, http.StatusForbidden, nil, &responseError{Code: "PERMISSION_DENIED", Message: "workflow session belongs to another owner"})
	default:
		reply(w, http.StatusServiceUnavailable, nil, &responseError{Code: "HOSTED_EXECUTION_FAILED", Message: err.Error(), Retryable: true})
	}
}

func (h Handler) begin(w http.ResponseWriter, r *http.Request, resume bool) {
	ownerID, ok := owner(w, r)
	if !ok {
		return
	}
	vars := mux.Vars(r)
	var (
		value Execution
		err   error
	)
	if resume {
		value, err = h.Service.Resume(r.Context(), ownerID, vars["session_id"], vars["attempt_id"])
	} else {
		value, err = h.Service.Begin(r.Context(), ownerID, vars["session_id"], vars["attempt_id"])
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	reply(w, http.StatusOK, value, nil)
}

func (h Handler) Begin(w http.ResponseWriter, r *http.Request)  { h.begin(w, r, false) }
func (h Handler) Resume(w http.ResponseWriter, r *http.Request) { h.begin(w, r, true) }

func (h Handler) Submit(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := owner(w, r)
	if !ok {
		return
	}
	var submission Submission
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&submission); err != nil {
		reply(w, http.StatusUnprocessableEntity, nil, &responseError{Code: "INVALID_SUBMISSION", Message: "invalid hosted execution submission"})
		return
	}
	vars := mux.Vars(r)
	value, err := h.Service.Submit(r.Context(), ownerID, vars["session_id"], vars["attempt_id"], submission)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	reply(w, http.StatusOK, value, nil)
}
