package server

import (
	"context"
	"net/http"

	"github.com/lazymind/scan_control_plane/internal/access"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/tree"
)

func (h *Handler) listBindingTargetChildren(w http.ResponseWriter, r *http.Request) {
	if h.targetTree == nil {
		writeError(w, missingDependency("target tree engine"))
		return
	}
	var req tree.TargetTreeChildrenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := h.access.CanAccessBindingTarget(r.Context(), actor, targetAccessFromChildren(req)); err != nil {
		writeError(w, err)
		return
	}
	req.ProviderOptions = withActorProviderOptions(req.ProviderOptions, actor)
	page, err := h.targetTree.ListChildren(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) searchBindingTargets(w http.ResponseWriter, r *http.Request) {
	if h.targetTree == nil {
		writeError(w, missingDependency("target tree engine"))
		return
	}
	var req tree.TargetTreeSearchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := h.access.CanAccessBindingTarget(r.Context(), actor, targetAccessFromSearch(req)); err != nil {
		writeError(w, err)
		return
	}
	req.ProviderOptions = withActorProviderOptions(req.ProviderOptions, actor)
	page, err := h.targetTree.Search(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) recommendBindingTargets(w http.ResponseWriter, r *http.Request) {
	h.handleRecommendBindingTargets(w, r, false)
}

func (h *Handler) listRecommendedBindingTargets(w http.ResponseWriter, r *http.Request) {
	h.handleRecommendBindingTargets(w, r, true)
}

func (h *Handler) handleRecommendBindingTargets(w http.ResponseWriter, r *http.Request, flatten bool) {
	if h.targetTree == nil {
		writeError(w, missingDependency("target tree engine"))
		return
	}
	var req tree.TargetTreeRecommendationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := h.access.CanAccessBindingTarget(r.Context(), actor, targetAccessFromRecommendation(req)); err != nil {
		writeError(w, err)
		return
	}
	req.ProviderOptions = withActorProviderOptions(req.ProviderOptions, actor)
	var page tree.TreeNodePage
	if flatten {
		page, err = h.targetTree.RecommendList(r.Context(), req)
	} else {
		page, err = h.targetTree.Recommend(r.Context(), req)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) listSourceTreeChildren(w http.ResponseWriter, r *http.Request) {
	if h.sourceTree == nil {
		writeError(w, missingDependency("source tree query engine"))
		return
	}
	var req tree.SourceTreeChildrenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	req.SourceID = r.PathValue("source_id")
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := h.access.CanReadSource(r.Context(), actor, req.SourceID); err != nil {
		writeError(w, err)
		return
	}
	req.ProviderOptions = withActorProviderOptions(req.ProviderOptions, actor)
	if sourceTreeShouldRefreshState(req) {
		if err := h.refreshSourceState(r.Context(), req.SourceID, req.BindingID); err != nil {
			writeError(w, err)
			return
		}
	}
	page, err := h.sourceTree.ListChildren(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) searchSourceTree(w http.ResponseWriter, r *http.Request) {
	if h.sourceTree == nil {
		writeError(w, missingDependency("source tree query engine"))
		return
	}
	var req tree.SourceTreeSearchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	req.SourceID = r.PathValue("source_id")
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := h.access.CanReadSource(r.Context(), actor, req.SourceID); err != nil {
		writeError(w, err)
		return
	}
	if treeShouldRefreshState(req.RefreshState) {
		if err := h.refreshSourceState(r.Context(), req.SourceID, req.BindingID); err != nil {
			writeError(w, err)
			return
		}
	}
	page, err := h.sourceTree.Search(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) listSourceDocuments(w http.ResponseWriter, r *http.Request) {
	if h.documents == nil {
		writeError(w, missingDependency("source document query"))
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
	req := tree.SourceDocumentListRequest{
		SourceID:      sourceID,
		BindingID:     r.URL.Query().Get("binding_id"),
		Keyword:       r.URL.Query().Get("keyword"),
		StateFilter:   r.URL.Query()["state_filter"],
		ParseStatuses: r.URL.Query()["parse_status"],
		Page:          parseIntQuery(r, "page"),
		PageSize:      parseIntQuery(r, "page_size"),
	}
	if boolQueryDefault(r, "refresh_state", true) {
		if err := h.refreshSourceState(r.Context(), req.SourceID, req.BindingID); err != nil {
			writeError(w, err)
			return
		}
	}
	page, err := h.documents.ListDocuments(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func targetAccessFromChildren(req tree.TargetTreeChildrenRequest) access.BindingTargetRequest {
	return access.BindingTargetRequest{
		SourceID:         stringOption(req.ProviderOptions, "source_id"),
		BindingID:        stringOption(req.ProviderOptions, "binding_id"),
		ConnectorType:    req.ConnectorType,
		TargetType:       req.TargetType,
		AgentID:          req.AgentID,
		AuthConnectionID: req.AuthConnectionID,
	}
}

func (h *Handler) refreshSourceState(ctx context.Context, sourceID, bindingID string) error {
	if h.refresher == nil {
		return nil
	}
	return h.refresher.RefreshSourceRead(ctx, tree.SourceReadRefreshRequest{
		SourceID:  sourceID,
		BindingID: bindingID,
	})
}

func sourceTreeShouldRefreshState(req tree.SourceTreeChildrenRequest) bool {
	return treeShouldRefreshState(req.RefreshState)
}

func treeShouldRefreshState(refreshState *bool) bool {
	if refreshState != nil {
		return *refreshState
	}
	return true
}

func targetAccessFromSearch(req tree.TargetTreeSearchRequest) access.BindingTargetRequest {
	return access.BindingTargetRequest{
		SourceID:         stringOption(req.ProviderOptions, "source_id"),
		BindingID:        stringOption(req.ProviderOptions, "binding_id"),
		ConnectorType:    req.ConnectorType,
		TargetType:       req.TargetType,
		AgentID:          req.AgentID,
		AuthConnectionID: req.AuthConnectionID,
	}
}

func targetAccessFromRecommendation(req tree.TargetTreeRecommendationRequest) access.BindingTargetRequest {
	return access.BindingTargetRequest{
		ConnectorType: connector.ConnectorType("local_fs"),
		TargetType:    connector.TargetType("local_path"),
		AgentID:       req.AgentID,
	}
}

func withActorProviderOptions(options map[string]any, actor access.Actor) map[string]any {
	out := make(map[string]any, len(options)+2)
	for key, value := range options {
		out[key] = value
	}
	out["user_id"] = actor.UserID
	out["tenant_id"] = actor.TenantID
	return out
}

func stringOption(options map[string]any, key string) string {
	value, _ := options[key].(string)
	return value
}
