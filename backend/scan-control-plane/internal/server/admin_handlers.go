package server

import (
	"net/http"

	"github.com/lazymind/scan_control_plane/internal/access"
	adminservice "github.com/lazymind/scan_control_plane/internal/admin"
)

func (h *Handler) metricsHandler(w http.ResponseWriter, _ *http.Request) {
	if h.metrics == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_ = h.metricsSnapshot(w)
}

func (h *Handler) metricsSnapshot(w http.ResponseWriter) error {
	return h.metrics.Write(w)
}

func (h *Handler) listDeletingResources(w http.ResponseWriter, r *http.Request) {
	req, _, ok := h.adminListRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.admin.ListDeletingResources(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listCompensations(w http.ResponseWriter, r *http.Request) {
	req, _, ok := h.adminListRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.admin.ListCompensations(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) retryCompensation(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	resp, err := h.admin.RetryCompensation(r.Context(), actor.UserID, r.PathValue("operation_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listDeadLetters(w http.ResponseWriter, r *http.Request) {
	req, _, ok := h.adminListRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.admin.ListDeadLetters(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) retryDeadLetter(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Force bool `json:"force,omitempty"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	resp, err := h.admin.RetryDeadLetter(r.Context(), actor.UserID, r.PathValue("dead_letter_id"), body.Force)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) reconcileBinding(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	sourceID := r.PathValue("source_id")
	bindingID := r.PathValue("binding_id")
	if err := h.access.CanWriteBinding(r.Context(), actor, sourceID, bindingID); err != nil {
		writeError(w, err)
		return
	}
	var body adminservice.ReconcileRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	resp, err := h.admin.ReconcileBinding(r.Context(), actor.UserID, sourceID, bindingID, body.RequestID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) adminListRequest(w http.ResponseWriter, r *http.Request) (adminservice.ListRequest, access.Actor, bool) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return adminservice.ListRequest{}, access.Actor{}, false
	}
	req := adminservice.ListRequest{
		SourceID:  r.URL.Query().Get("source_id"),
		BindingID: r.URL.Query().Get("binding_id"),
		Page:      parseIntQuery(r, "page"),
		PageSize:  parseIntQuery(r, "page_size"),
	}
	if req.SourceID != "" {
		if err := h.access.CanReadSource(r.Context(), actor, req.SourceID); err != nil {
			writeError(w, err)
			return adminservice.ListRequest{}, access.Actor{}, false
		}
		req.SourceIDs = []string{req.SourceID}
		return req, actor, true
	}
	sourceIDs, err := h.access.ListReadableSourceIDs(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return adminservice.ListRequest{}, access.Actor{}, false
	}
	req.SourceIDs = sourceIDs
	return req, actor, true
}

func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (access.Actor, bool) {
	if h.admin == nil {
		writeError(w, missingDependency("admin service"))
		return access.Actor{}, false
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return access.Actor{}, false
	}
	// Admin-only enforcement happens at the Kong gateway layer (rbac-auth plugin requires "user.admin"
	// permission for all /api/scan/admin/* paths). Any request that reaches this point has already
	// been authorized by Kong; we still require a valid actor as a defence-in-depth measure.
	return actor, true
}
