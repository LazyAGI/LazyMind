package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/access"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
)

type providerTokenContextRequest struct {
	UserID             string `json:"user_id"`
	TenantID           string `json:"tenant_id"`
	SourceID           string `json:"source_id"`
	BindingID          string `json:"binding_id"`
	AuthConnectionID   string `json:"auth_connection_id"`
	ContextMode        string `json:"context_mode"`
	Consumer           string `json:"consumer"`
	RequiredCapability string `json:"required_capability"`
}

func (h *Handler) authorizeProviderTokenContext(response http.ResponseWriter, request *http.Request) {
	provided := strings.TrimSpace(request.Header.Get("X-LazyMind-Internal-Token"))
	expected := strings.TrimSpace(h.internalToken)
	if expected == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		http.Error(response, "access denied", http.StatusForbidden)
		return
	}
	var body providerTokenContextRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || strings.TrimSpace(body.UserID) == "" ||
		strings.TrimSpace(body.AuthConnectionID) == "" || strings.TrimSpace(body.ContextMode) == "" {
		http.Error(response, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	actor := access.Actor{UserID: body.UserID, TenantID: body.TenantID, InternalToken: provided}
	if body.ContextMode == "pre_binding_browse" {
		if strings.TrimSpace(body.SourceID) != "" || strings.TrimSpace(body.BindingID) != "" ||
			body.Consumer != "datasource" || body.RequiredCapability != "datasource.browse" {
			http.Error(response, "invalid request", http.StatusUnprocessableEntity)
			return
		}
		if err := h.access.CanUseAuthConnection(request.Context(), actor, body.AuthConnectionID); err != nil {
			http.Error(response, "access denied", http.StatusForbidden)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if body.ContextMode != "source_binding" || strings.TrimSpace(body.SourceID) == "" || strings.TrimSpace(body.BindingID) == "" {
		http.Error(response, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	if err := h.access.CanAccessBindingTarget(request.Context(), actor, access.BindingTargetRequest{
		SourceID: body.SourceID, BindingID: body.BindingID, AuthConnectionID: body.AuthConnectionID,
	}); err != nil {
		http.Error(response, "access denied", http.StatusForbidden)
		return
	}
	if h.sources == nil {
		http.Error(response, "source engine is not configured", http.StatusInternalServerError)
		return
	}
	source, err := h.sources.GetSource(request.Context(), sourceengine.GetSourceRequest{
		CallerID: body.UserID, TenantID: body.TenantID, SourceID: body.SourceID, IncludeBindings: true,
	})
	if err != nil || !providerBindingMatches(source.Bindings, body.BindingID, body.AuthConnectionID) {
		http.Error(response, "access denied", http.StatusForbidden)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func providerBindingMatches(bindings []sourceengine.SourceBindingResponse, bindingID, authConnectionID string) bool {
	for _, binding := range bindings {
		if strings.TrimSpace(binding.BindingID) == strings.TrimSpace(bindingID) &&
			strings.TrimSpace(binding.AuthConnectionID) == strings.TrimSpace(authConnectionID) &&
			!strings.EqualFold(strings.TrimSpace(binding.Status), sourceengine.BindingStatusDeleting) {
			return true
		}
	}
	return false
}
