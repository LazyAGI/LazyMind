package server

import (
	"net/http"
	"strconv"

	"github.com/lazymind/scan_control_plane/internal/access"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
)

func (h *Handler) createSource(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := h.access.CanCreateSource(r.Context(), actor); err != nil {
		writeError(w, err)
		return
	}
	var req sourceengine.CreateSourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	if err := h.requireLocalSourceAdmin(r, actor, req.Bindings, req.SourceOptions); err != nil {
		writeError(w, err)
		return
	}
	req.CallerID = actor.UserID
	req.TenantID = actor.TenantID
	if err := h.checkBindingTargetInputs(r, actor, "", req.Bindings); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.sources.CreateSource(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) requireLocalSourceAdmin(r *http.Request, actor access.Actor, bindings []sourceengine.BindingInput, sourceOptions map[string]any) error {
	if h.access.ShouldBlockLocalSourceAccess(r.Context(), actor, access.LocalSourceAccessRequest{
		SourceOptions:  sourceOptions,
		BindingTargets: bindingInputsToTargetAccess(bindings),
	}) {
		return access.NewError(access.ErrCodeForbidden, "local data sources can only be accessed by administrators")
	}
	return nil
}

func bindingInputsToTargetAccess(bindings []sourceengine.BindingInput) []access.BindingTargetRequest {
	targets := make([]access.BindingTargetRequest, 0, len(bindings))
	for _, binding := range bindings {
		targets = append(targets, access.BindingTargetRequest{
			BindingID:        binding.BindingID,
			ConnectorType:    binding.ConnectorType,
			TargetType:       binding.TargetType,
			AgentID:          binding.AgentID,
			AuthConnectionID: binding.AuthConnectionID,
		})
	}
	return targets
}

func (h *Handler) listSources(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceIDs, err := h.access.ListReadableSourceIDs(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	req := sourceengine.ListSourcesRequest{
		CallerID:  actor.UserID,
		TenantID:  actor.TenantID,
		SourceIDs: sourceIDs,
		Keyword:   r.URL.Query().Get("keyword"),
		Status:    r.URL.Query().Get("status"),
		Page:      parseIntQuery(r, "page"),
		PageSize:  parseIntQuery(r, "page_size"),
	}
	resp, err := h.sources.ListSources(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getSource(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	if err := h.access.CanReadSource(r.Context(), actor, sourceID); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.sources.GetSource(r.Context(), sourceengine.GetSourceRequest{
		CallerID:        actor.UserID,
		TenantID:        actor.TenantID,
		SourceID:        sourceID,
		IncludeBindings: boolQueryDefault(r, "include_bindings", true),
		IncludeSummary:  boolQueryDefault(r, "include_summary", true),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getSourceByDataset(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	datasetID := r.PathValue("dataset_id")
	resp, err := h.sources.GetSourceByDatasetID(r.Context(), datasetID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
func (h *Handler) batchGetSourcesByDatasetIDs(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	var req struct {
		DatasetIDs []string `json:"dataset_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	result, err := h.sources.BatchGetSourcesByDatasetIDs(r.Context(), req.DatasetIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source_map": result})
}


func (h *Handler) triggerSourceSync(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	if err := h.access.CanWriteSource(r.Context(), actor, sourceID); err != nil {
		writeError(w, err)
		return
	}
	var req sourceengine.TriggerSourceSyncRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	req.CallerID = actor.UserID
	req.TenantID = actor.TenantID
	req.SourceID = sourceID
	resp, err := h.sources.TriggerSourceSync(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getSourceSummary(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	if err := h.access.CanReadSource(r.Context(), actor, sourceID); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.sources.GetSourceSummary(r.Context(), sourceengine.SourceSummaryRequest{
		CallerID:  actor.UserID,
		TenantID:  actor.TenantID,
		SourceID:  sourceID,
		BindingID: r.URL.Query().Get("binding_id"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) updateSource(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	if err := h.access.CanWriteSource(r.Context(), actor, sourceID); err != nil {
		writeError(w, err)
		return
	}
	req, err := decodeUpdateSourceRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := h.requireLocalSourceAdmin(r, actor, req.Bindings, req.SourceOptions); err != nil {
		writeError(w, err)
		return
	}
	if err := h.checkBindingTargetInputs(r, actor, sourceID, req.Bindings); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.sources.UpdateSource(r.Context(), actor.UserID, sourceID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) deleteSource(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	if err := h.access.CanDeleteSource(r.Context(), actor, sourceID); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.sources.DeleteSource(r.Context(), sourceID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) deleteSourceByDataset(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	datasetID := r.PathValue("dataset_id")
	resp, err := h.sources.DeleteSourceByDatasetID(r.Context(), datasetID, sourceengine.DeleteSourceOptions{
		SkipCoreDatasetDelete: true,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) appendSource(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	if err := h.access.CanWriteSource(r.Context(), actor, sourceID); err != nil {
		writeError(w, err)
		return
	}
	var req sourceengine.AppendSourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	req.CallerID = actor.UserID
	req.TenantID = actor.TenantID
	req.SourceID = sourceID

	if err := h.checkBindingTargetInputs(r, actor, sourceID, req.Bindings); err != nil {
		writeError(w, err)
		return
	}
	if err := h.requireLocalSourceAdmin(r, actor, req.Bindings, nil); err != nil {
		writeError(w, err)
		return
	}

	resp, err := h.sources.AppendSource(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) createSourceBinding(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	if err := h.access.CanWriteSource(r.Context(), actor, sourceID); err != nil {
		writeError(w, err)
		return
	}
	var req sourceengine.BindingInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	if err := h.requireLocalSourceAdmin(r, actor, []sourceengine.BindingInput{req}, nil); err != nil {
		writeError(w, err)
		return
	}
	if err := h.checkBindingTargetInputs(r, actor, sourceID, []sourceengine.BindingInput{req}); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.sources.AddBinding(r.Context(), actor.UserID, sourceID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) updateSourceBinding(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	bindingID := r.PathValue("binding_id")
	if err := h.access.CanWriteBinding(r.Context(), actor, sourceID, bindingID); err != nil {
		writeError(w, err)
		return
	}
	var req sourceengine.BindingInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	if err := h.requireLocalSourceAdmin(r, actor, []sourceengine.BindingInput{req}, nil); err != nil {
		writeError(w, err)
		return
	}
	if err := h.checkBindingTargetInputs(r, actor, sourceID, []sourceengine.BindingInput{req}); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.sources.UpdateBinding(r.Context(), actor.UserID, sourceID, bindingID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) deleteSourceBinding(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceID := r.PathValue("source_id")
	bindingID := r.PathValue("binding_id")
	if err := h.access.CanDeleteBinding(r.Context(), actor, sourceID, bindingID); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.sources.DeleteBinding(r.Context(), sourceID, bindingID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func decodeUpdateSourceRequest(r *http.Request) (sourceengine.UpdateSourceRequest, error) {
	type updateSourceBody struct {
		ConfigVersion     int64                       `json:"config_version"`
		Name              *string                     `json:"name,omitempty"`
		Bindings          []sourceengine.BindingInput `json:"bindings,omitempty"`
		IncludeExtensions []string                    `json:"include_extensions,omitempty"`
		ExcludeExtensions []string                    `json:"exclude_extensions,omitempty"`
		SourceOptions     map[string]any              `json:"source_options,omitempty"`
	}
	var body updateSourceBody
	if err := decodeJSON(r, &body); err != nil {
		return sourceengine.UpdateSourceRequest{}, invalidJSON(err)
	}
	req := sourceengine.UpdateSourceRequest{
		ConfigVersion:     body.ConfigVersion,
		Name:              body.Name,
		Bindings:          body.Bindings,
		IncludeExtensions: body.IncludeExtensions,
		ExcludeExtensions: body.ExcludeExtensions,
		SourceOptions:     body.SourceOptions,
	}
	if body.Bindings != nil {
		req.BindingsProvided = true
	}
	return req, nil
}

func (h *Handler) checkBindingTargetInputs(r *http.Request, actor access.Actor, sourceID string, bindings []sourceengine.BindingInput) error {
	for _, binding := range bindings {
		if binding.AgentID == "" && binding.AuthConnectionID == "" {
			continue
		}
		req := access.BindingTargetRequest{
			SourceID:         sourceID,
			BindingID:        binding.BindingID,
			ConnectorType:    binding.ConnectorType,
			TargetType:       binding.TargetType,
			AgentID:          binding.AgentID,
			AuthConnectionID: binding.AuthConnectionID,
		}
		if err := h.access.CanAccessBindingTarget(r.Context(), actor, req); err != nil {
			return err
		}
	}
	return nil
}

func parseIntQuery(r *http.Request, key string) int {
	value := r.URL.Query().Get(key)
	if value == "" {
		return 0
	}
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func boolQueryDefault(r *http.Request, key string, fallback bool) bool {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

