package server

import (
	"net/http"
	"strconv"
	"strings"

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
	req.CallerName = actor.UserName
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


type sourceAccessByDatasetBatchRequest struct {
	DatasetIDs []string `json:"dataset_ids"`
	Action     string   `json:"action"`
}

type sourceAccessByDatasetBatchResponse struct {
	Items []sourceAccessByDatasetItem `json:"items"`
}

type sourceAccessByDatasetItem struct {
	DatasetID string `json:"dataset_id"`
	SourceID  string `json:"source_id,omitempty"`
	Exists    bool   `json:"exists"`
	Allowed   bool   `json:"allowed"`
}

func (h *Handler) batchSourceAccessByDataset(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req sourceAccessByDatasetBatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	action, err := sourceAccessAction(req.Action)
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]sourceAccessByDatasetItem, 0, len(req.DatasetIDs))
	for _, datasetID := range uniqueSourceAccessDatasetIDs(req.DatasetIDs) {
		item := sourceAccessByDatasetItem{DatasetID: datasetID, Allowed: true}
		sourceResp, err := h.sources.GetSourceByDatasetID(r.Context(), datasetID)
		if err != nil {
			if sourceengine.ErrorCodeOf(err) == sourceengine.ErrCodeSourceNotFound {
				items = append(items, item)
				continue
			}
			writeError(w, err)
			return
		}
		item.Exists = true
		item.SourceID = sourceResp.Source.SourceID
		item.Allowed = h.sourceAccessAllowed(r, actor, sourceResp.Source.SourceID, action)
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, sourceAccessByDatasetBatchResponse{Items: items})
}

func (h *Handler) sourceAccessAllowed(r *http.Request, actor access.Actor, sourceID string, action access.SourceAction) bool {
	switch action {
	case access.SourceActionWrite:
		return h.access.CanWriteSource(r.Context(), actor, sourceID) == nil
	case access.SourceActionDelete:
		return h.access.CanDeleteSource(r.Context(), actor, sourceID) == nil
	default:
		return h.access.CanReadSource(r.Context(), actor, sourceID) == nil
	}
}

func sourceAccessAction(action string) (access.SourceAction, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", string(access.SourceActionRead):
		return access.SourceActionRead, nil
	case string(access.SourceActionWrite), "upload":
		return access.SourceActionWrite, nil
	case string(access.SourceActionDelete):
		return access.SourceActionDelete, nil
	default:
		return "", sourceengine.FieldError("action", "must be read, write, upload, or delete")
	}
}

func uniqueSourceAccessDatasetIDs(datasetIDs []string) []string {
	out := make([]string, 0, len(datasetIDs))
	seen := make(map[string]struct{}, len(datasetIDs))
	for _, datasetID := range datasetIDs {
		datasetID = strings.TrimSpace(datasetID)
		if datasetID == "" {
			continue
		}
		if _, ok := seen[datasetID]; ok {
			continue
		}
		seen[datasetID] = struct{}{}
		out = append(out, datasetID)
	}
	return out
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

