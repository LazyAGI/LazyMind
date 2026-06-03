package server

import (
	"net/http"

	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
)

func (h *Handler) generateParseTasks(w http.ResponseWriter, r *http.Request) {
	if h.tasks == nil {
		writeError(w, missingDependency("task planner"))
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
	var req taskengine.GenerateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	req.CallerID = actor.UserID
	req.TenantID = actor.TenantID
	req.SourceID = sourceID
	resp, err := h.tasks.GenerateTasks(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) expediteParseTasks(w http.ResponseWriter, r *http.Request) {
	if h.tasks == nil {
		writeError(w, missingDependency("task planner"))
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
	var req taskengine.ExpediteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	req.CallerID = actor.UserID
	req.TenantID = actor.TenantID
	req.SourceID = sourceID
	resp, err := h.tasks.ExpediteTasks(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listParseTasks(w http.ResponseWriter, r *http.Request) {
	if h.taskQuery == nil {
		writeError(w, missingDependency("parse task query"))
		return
	}
	req, err := h.authorizedParseTaskQueryRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.taskQuery.ListParseTasks(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getParseTaskStats(w http.ResponseWriter, r *http.Request) {
	if h.taskQuery == nil {
		writeError(w, missingDependency("parse task query"))
		return
	}
	req, err := h.authorizedParseTaskQueryRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.taskQuery.GetParseTaskStats(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getParseTask(w http.ResponseWriter, r *http.Request) {
	if h.taskQuery == nil {
		writeError(w, missingDependency("parse task query"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	taskID := r.PathValue("task_id")
	if err := h.access.CanReadTask(r.Context(), actor, taskID); err != nil {
		writeError(w, err)
		return
	}
	resp, err := h.taskQuery.GetParseTask(r.Context(), taskID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) retryParseTask(w http.ResponseWriter, r *http.Request) {
	if h.tasks == nil {
		writeError(w, missingDependency("task planner"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	taskID := r.PathValue("task_id")
	if err := h.access.CanWriteTask(r.Context(), actor, taskID); err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		Force bool `json:"force,omitempty"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	resp, err := h.tasks.RetryTask(r.Context(), taskengine.RetryRequest{
		CallerID: actor.UserID,
		TenantID: actor.TenantID,
		TaskID:   taskID,
		Force:    body.Force,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) authorizedParseTaskQueryRequest(r *http.Request) (taskengine.ParseTaskQueryRequest, error) {
	actor, err := actorFromRequest(r)
	if err != nil {
		return taskengine.ParseTaskQueryRequest{}, err
	}
	req := parseTaskQueryRequest(r)
	req.CallerID = actor.UserID
	req.TenantID = actor.TenantID
	if req.SourceID != "" {
		if err := h.access.CanReadSource(r.Context(), actor, req.SourceID); err != nil {
			return taskengine.ParseTaskQueryRequest{}, err
		}
		req.SourceIDs = []string{req.SourceID}
		return req, nil
	}
	sourceIDs, err := h.access.ListReadableSourceIDs(r.Context(), actor)
	if err != nil {
		return taskengine.ParseTaskQueryRequest{}, err
	}
	req.SourceIDs = sourceIDs
	return req, nil
}

func parseTaskQueryRequest(r *http.Request) taskengine.ParseTaskQueryRequest {
	query := r.URL.Query()
	return taskengine.ParseTaskQueryRequest{
		SourceID:    query.Get("source_id"),
		BindingID:   query.Get("binding_id"),
		DocumentID:  query.Get("document_id"),
		Statuses:    query["status"],
		TaskActions: query["task_action"],
		Page:        parseIntQuery(r, "page"),
		PageSize:    parseIntQuery(r, "page_size"),
	}
}
