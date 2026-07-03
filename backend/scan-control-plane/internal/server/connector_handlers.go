package server

import (
	"net/http"

	"github.com/lazymind/scan_control_plane/internal/access"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type connectorSpecResponse struct {
	ConnectorType          connector.ConnectorType  `json:"connector_type"`
	TargetTypes            []connector.TargetType   `json:"target_types"`
	SupportsSearch         bool                     `json:"supports_search"`
	SupportsDelta          bool                     `json:"supports_delta"`
	SupportsDualRoleObject bool                     `json:"supports_dual_role_object"`
	SupportsExportFormats  []connector.ExportFormat `json:"supports_export_formats"`
	MaxPageSize            int                      `json:"max_page_size"`
}

func (h *Handler) listConnectors(w http.ResponseWriter, r *http.Request) {
	if h.registry == nil {
		writeError(w, missingDependency("connector registry"))
		return
	}
	specs := h.registry.Specs()
	items := make([]connectorSpecResponse, 0, len(specs))
	for _, spec := range specs {
		items = append(items, connectorSpecResponse{
			ConnectorType:          spec.ConnectorType,
			TargetTypes:            spec.TargetTypes,
			SupportsSearch:         spec.SupportsSearch,
			SupportsDelta:          spec.SupportsDelta,
			SupportsDualRoleObject: spec.SupportsDualRoleObject,
			SupportsExportFormats:  spec.SupportsExportFormats,
			MaxPageSize:            spec.MaxPageSize,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) validateBindingTarget(w http.ResponseWriter, r *http.Request) {
	if h.registry == nil {
		writeError(w, missingDependency("connector registry"))
		return
	}
	var req connector.ValidateTargetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := h.access.CanAccessBindingTarget(r.Context(), actor, validateTargetAccess(req)); err != nil {
		writeError(w, err)
		return
	}
	req.UserID = actor.UserID
	conn, err := h.registry.Get(req.ConnectorType)
	if err != nil {
		writeError(w, err)
		return
	}
	target, err := conn.ValidateTarget(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func validateTargetAccess(req connector.ValidateTargetRequest) access.BindingTargetRequest {
	return access.BindingTargetRequest{
		SourceID:         req.ProviderOptions.String("source_id"),
		BindingID:        req.ProviderOptions.String("binding_id"),
		ConnectorType:    req.ConnectorType,
		TargetType:       req.TargetType,
		AgentID:          req.AgentID,
		AuthConnectionID: req.AuthConnectionID,
	}
}
