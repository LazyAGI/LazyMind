package product

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	corestore "lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow/facade"
	workflowstore "lazymind/core/workflow/store"
)

func (h Handler) ProductStageRelay(w http.ResponseWriter, r *http.Request) {
	owner, ok := facade.IdentityAndVersion(w, r)
	if !ok {
		return
	}
	sessionID := mux.Vars(r)["session_id"]
	if r.Method == http.MethodGet {
		result, err := h.Store.ProductRelaySummary(r.Context(), owner, sessionID)
		if err != nil {
			productRelayError(w, err)
			return
		}
		facade.WriteJSON(w, http.StatusOK, facade.Envelope{Data: result})
		return
	}
	var req ProductRelayRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		facade.Fail(w, 422, "INVALID_PRODUCT_RELAY", "invalid product stage request", false)
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = r.Header.Get("Idempotency-Key")
	}
	result, err := h.Store.RelayProductStage(r.Context(), owner, sessionID, req)
	if err != nil {
		productRelayError(w, err)
		return
	}
	var value map[string]any
	_ = json.Unmarshal(result, &value)
	if value["session_id"] != sessionID && subagent.EventHooks != nil {
		// The event only refreshes UI; it never dispatches a stage or creates authorization.
		_ = subagent.EventHooks.CallConversationEventChecked(r.Context(), corestore.State(),
			productRelayConversation(h, r, owner, sessionID), "", "workflow_session_created", value)
	}
	facade.WriteJSON(w, http.StatusOK, facade.Envelope{Data: value})
}

func (h Handler) ProductProjectArtifact(w http.ResponseWriter, r *http.Request) {
	owner, ok := facade.IdentityAndVersion(w, r)
	if !ok {
		return
	}
	vars := mux.Vars(r)
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	result, err := h.Store.ProductProjectArtifact(r.Context(), owner, vars["session_id"], vars["stage"], format)
	if err != nil {
		productRelayError(w, err)
		return
	}
	// HTML is JSON data, never served as an executable same-origin document.
	w.Header().Set("Cache-Control", "private, no-store")
	facade.WriteJSON(w, http.StatusOK, facade.Envelope{Data: result})
}

func (h Handler) ProductDecision(w http.ResponseWriter, r *http.Request) {
	owner, ok := facade.IdentityAndVersion(w, r)
	if !ok {
		return
	}
	var req ProductDecisionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		facade.Fail(w, http.StatusUnprocessableEntity, "INVALID_PRODUCT_DECISION", "invalid product decision request", false)
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = r.Header.Get("Idempotency-Key")
	}
	vars := mux.Vars(r)
	result, err := h.Store.DecideProductDecision(r.Context(), owner, vars["session_id"], vars["decision_id"], req)
	if err != nil {
		productRelayError(w, err)
		return
	}
	var value map[string]any
	_ = json.Unmarshal(result, &value)
	facade.WriteJSON(w, http.StatusOK, facade.Envelope{Data: value})
}

func productRelayConversation(h Handler, r *http.Request, owner, sessionID string) string {
	// Summary is safe to expose; the durable event itself is Session scoped.
	return h.Store.ProductRelayConversation(r.Context(), owner, sessionID)
}

func productRelayError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, workflowstore.ErrPermissionDenied) {
		status = http.StatusForbidden
	}
	if errors.Is(err, workflowstore.ErrNotFound) {
		status = http.StatusNotFound
	}
	if err.Error() == "INVALID_PRODUCT_RELAY" || err.Error() == "INVALID_PRODUCT_STAGE" || err.Error() == "INVALID_PRODUCT_DECISION" || err.Error() == "INVALID_PRODUCT_RESTART" || err.Error() == "INVALID_PRODUCT_MARKDOWN" {
		status = http.StatusUnprocessableEntity
	}
	facade.Fail(w, status, err.Error(), err.Error(), false)
}

type Handler struct{ Store *Repository }
