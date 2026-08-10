package attempt

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
)

const ContractVersion = "workflow.v1"

type toolError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type envelope struct {
	ContractVersion string     `json:"contract_version"`
	RequestID       string     `json:"request_id"`
	OK              bool       `json:"ok"`
	Data            any        `json:"data,omitempty"`
	Error           *toolError `json:"error,omitempty"`
}

type Handler struct{ Service *Service }

func respond(w http.ResponseWriter, status int, data any, err *toolError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{ContractVersion: ContractVersion,
		RequestID: "server-generated", OK: err == nil, Data: data, Error: err})
}

func executorID(w http.ResponseWriter, r *http.Request) (string, bool) {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN"))
	if expected != "" {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			respond(w, http.StatusUnauthorized, nil, &toolError{Code: "EXECUTOR_UNAUTHORIZED", Message: "invalid Executor credential"})
			return "", false
		}
	}
	id := strings.TrimSpace(r.Header.Get("X-Workflow-Executor-Id"))
	if id == "" {
		respond(w, http.StatusUnauthorized, nil, &toolError{Code: "EXECUTOR_IDENTITY_REQUIRED", Message: "executor identity is required"})
		return "", false
	}
	version := strings.TrimSpace(r.Header.Get("Workflow-Contract-Version"))
	if version != "" && version != ContractVersion {
		respond(w, http.StatusUnprocessableEntity, nil, &toolError{Code: "CONTRACT_VERSION_UNSUPPORTED", Message: "supported version is workflow.v1"})
		return "", false
	}
	return id, true
}

func protocolToolError(err error) (int, *toolError) {
	switch {
	case errors.Is(err, ErrLeaseLost):
		return http.StatusConflict, &toolError{Code: CodeLeaseLost, Message: "attempt lease is no longer valid", Retryable: true}
	case errors.Is(err, ErrAlreadyTerminal):
		return http.StatusConflict, &toolError{Code: CodeAlreadyTerminal, Message: "another terminal result already won"}
	case errors.Is(err, ErrNotClaimable):
		return http.StatusNotFound, &toolError{Code: CodeNotClaimable, Message: "no attempt is claimable", Retryable: true}
	case errors.Is(err, ErrSchemaUnavailable):
		return http.StatusServiceUnavailable, &toolError{Code: CodeSchemaUnavailable, Message: "attempt schema capability is unavailable", Retryable: true}
	default:
		return http.StatusServiceUnavailable, &toolError{Code: "ATTEMPT_PROTOCOL_FAILED", Message: err.Error(), Retryable: true}
	}
}

func (h Handler) Claim(w http.ResponseWriter, r *http.Request) {
	id, ok := executorID(w, r)
	if !ok {
		return
	}
	claim, err := h.Service.ClaimForHost(r.Context(), id, r.Header.Get("X-Workflow-Host"))
	if err != nil {
		status, body := protocolToolError(err)
		respond(w, status, nil, body)
		return
	}
	respond(w, http.StatusOK, claim, nil)
}

type leaseRequest struct {
	LeaseToken string          `json:"lease_token"`
	Progress   json.RawMessage `json:"progress"`
	Result     json.RawMessage `json:"result"`
	ErrorCode  string          `json:"error_code"`
}

func decodeLease(w http.ResponseWriter, r *http.Request) (leaseRequest, bool) {
	var body leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LeaseToken == "" {
		respond(w, http.StatusUnprocessableEntity, nil, &toolError{Code: "INVALID_REQUEST", Message: "lease_token is required"})
		return leaseRequest{}, false
	}
	return body, true
}

func (h Handler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	if _, ok := executorID(w, r); !ok {
		return
	}
	body, ok := decodeLease(w, r)
	if !ok {
		return
	}
	expires, err := h.Service.Heartbeat(r.Context(), mux.Vars(r)["attempt_id"], body.LeaseToken)
	if err != nil {
		status, failure := protocolToolError(err)
		respond(w, status, nil, failure)
		return
	}
	respond(w, http.StatusOK, map[string]any{"lease_expires_at": expires}, nil)
}

func (h Handler) Progress(w http.ResponseWriter, r *http.Request) {
	if _, ok := executorID(w, r); !ok {
		return
	}
	body, ok := decodeLease(w, r)
	if !ok {
		return
	}
	if err := h.Service.Progress(r.Context(), mux.Vars(r)["attempt_id"], body.LeaseToken, body.Progress); err != nil {
		status, failure := protocolToolError(err)
		respond(w, status, nil, failure)
		return
	}
	respond(w, http.StatusOK, map[string]any{"reported": true}, nil)
}

func (h Handler) terminal(w http.ResponseWriter, r *http.Request, status string) {
	if _, ok := executorID(w, r); !ok {
		return
	}
	body, ok := decodeLease(w, r)
	if !ok {
		return
	}
	err := h.Service.Terminal(r.Context(), mux.Vars(r)["attempt_id"], body.LeaseToken, status, body.ErrorCode, body.Result)
	if err != nil {
		httpStatus, failure := protocolToolError(err)
		respond(w, httpStatus, nil, failure)
		return
	}
	respond(w, http.StatusOK, map[string]any{"attempt_status": status}, nil)
}

func (h Handler) Complete(w http.ResponseWriter, r *http.Request) { h.terminal(w, r, "succeeded") }
func (h Handler) Fail(w http.ResponseWriter, r *http.Request)     { h.terminal(w, r, "failed") }
func (h Handler) Cancel(w http.ResponseWriter, r *http.Request)   { h.terminal(w, r, "cancelled") }
