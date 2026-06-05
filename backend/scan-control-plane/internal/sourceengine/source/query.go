package source

import (
	"context"
	"fmt"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	scheduleengine "github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func (e *DefaultEngine) ListSources(ctx context.Context, req ListSourcesRequest) (ListSourcesResponse, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	records, total, err := e.repo.ListSources(ctx, storeListSourcesRequest(req))
	if err != nil {
		return ListSourcesResponse{}, mapStoreError(err)
	}
	items := make([]SourceListItemResponse, 0, len(records))
	for _, record := range records {
		items = append(items, sourceListItemToResponse(record))
	}
	return ListSourcesResponse{Items: items, Total: total}, nil
}

func storeListSourcesRequest(req ListSourcesRequest) store.SourceListRequest {
	return store.SourceListRequest{
		CallerID:  req.CallerID,
		TenantID:  req.TenantID,
		SourceIDs: req.SourceIDs,
		Keyword:   req.Keyword,
		Status:    req.Status,
		Page:      req.Page,
		PageSize:  req.PageSize,
	}
}

func (e *DefaultEngine) GetSource(ctx context.Context, req GetSourceRequest) (GetSourceResponse, error) {
	src, err := e.repo.GetSource(ctx, req.SourceID)
	if err != nil {
		return GetSourceResponse{}, mapStoreError(err)
	}
	resp := GetSourceResponse{Source: sourceToResponse(src)}
	if req.IncludeBindings {
		bindings, err := e.repo.ListBindings(ctx, req.SourceID)
		if err != nil {
			return GetSourceResponse{}, mapStoreError(err)
		}
		resp.Bindings = bindingsToResponse(bindings)
	}
	if req.IncludeSummary {
		summary, err := e.GetSourceSummary(ctx, SourceSummaryRequest{CallerID: req.CallerID, SourceID: req.SourceID})
		if err != nil {
			return GetSourceResponse{}, err
		}
		resp.Summary = sourceSummaryMap(summary)
	}
	return resp, nil
}

func (e *DefaultEngine) TriggerSourceSync(ctx context.Context, req TriggerSourceSyncRequest) (TriggerSourceSyncResponse, error) {
	if req.SourceID == "" {
		return TriggerSourceSyncResponse{}, FieldError("source_id", "required")
	}
	src, err := e.repo.GetSource(ctx, req.SourceID)
	if err != nil {
		return TriggerSourceSyncResponse{}, mapStoreError(err)
	}
	if src.Status != SourceStatusActive {
		return TriggerSourceSyncResponse{}, NewError(ErrCodeInvalidRequest, "source is not active")
	}
	bindings, err := e.syncBindings(ctx, req)
	if err != nil {
		return TriggerSourceSyncResponse{}, err
	}
	return e.enqueueManualSyncs(ctx, req, bindings)
}

func (e *DefaultEngine) GetSourceSummary(ctx context.Context, req SourceSummaryRequest) (SourceSummaryResponse, error) {
	if _, err := e.repo.GetSource(ctx, req.SourceID); err != nil {
		return SourceSummaryResponse{}, mapStoreError(err)
	}
	if req.BindingID != "" {
		if _, err := e.repo.GetBinding(ctx, req.SourceID, req.BindingID); err != nil {
			return SourceSummaryResponse{}, mapStoreError(err)
		}
	}
	summary, err := e.repo.GetSourceSummary(ctx, store.SourceSummaryRequest{SourceID: req.SourceID, BindingID: req.BindingID})
	if err != nil {
		return SourceSummaryResponse{}, mapStoreError(err)
	}
	return sourceSummaryToResponse(summary), nil
}

func (e *DefaultEngine) syncBindings(ctx context.Context, req TriggerSourceSyncRequest) ([]store.Binding, error) {
	if req.BindingID != "" {
		binding, err := e.repo.GetBinding(ctx, req.SourceID, req.BindingID)
		if err != nil {
			return nil, mapStoreError(err)
		}
		if binding.Status != BindingStatusActive {
			return nil, NewError(ErrCodeInvalidRequest, "binding is not active")
		}
		return []store.Binding{binding}, nil
	}
	bindings, err := e.repo.ListBindings(ctx, req.SourceID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	active := make([]store.Binding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Status == BindingStatusActive {
			active = append(active, binding)
		}
	}
	return active, nil
}

func (e *DefaultEngine) enqueueManualSyncs(ctx context.Context, req TriggerSourceSyncRequest, bindings []store.Binding) (TriggerSourceSyncResponse, error) {
	resp := TriggerSourceSyncResponse{RunIDs: []string{}, JobIDs: []string{}, Intents: []SyncRunIntentResponse{}}
	for _, binding := range bindings {
		for idx, scopeRef := range syncScopeRefs(req.ScopeRef, binding.BindingID) {
			intent, err := e.schedule.EnqueueManualSync(ctx, scheduleengine.ManualSyncRequest{
				RequestID: scopedSyncRequestID(req.RequestID, idx),
				SourceID:  binding.SourceID,
				BindingID: binding.BindingID,
				ScopeType: connector.ScopeType(req.ScopeType),
				ScopeRef:  scopeRef,
			})
			if err != nil {
				return resp, mapStoreError(err)
			}
			if intent.Run.RunID == "" {
				continue
			}
			resp.RunIDs = append(resp.RunIDs, intent.Run.RunID)
			resp.JobIDs = append(resp.JobIDs, intent.Run.RunID)
			resp.Intents = append(resp.Intents, syncRunIntentToResponse(intent))
		}
	}
	return resp, nil
}

func syncScopeRef(values map[string]any) connector.ScopeRef {
	refs := syncScopeRefs(values, "")
	if len(refs) == 0 {
		return nil
	}
	return refs[0]
}

func syncScopeRefs(values map[string]any, bindingID string) []connector.ScopeRef {
	if len(values) == 0 {
		return []connector.ScopeRef{nil}
	}
	if scopes := scopeRefListFromAny(values["scopes"], bindingID); len(scopes) > 0 {
		return scopes
	}
	if keys := stringListFromAny(values["object_keys"]); len(keys) > 0 {
		out := make([]connector.ScopeRef, 0, len(keys))
		for _, key := range keys {
			out = append(out, connector.ScopeRef{"object_key": key})
		}
		return out
	}
	out := make(connector.ScopeRef, len(values))
	for key, value := range values {
		if s, ok := value.(string); ok {
			out[key] = s
		}
	}
	return []connector.ScopeRef{out}
}

func scopeRefListFromAny(value any, bindingID string) []connector.ScopeRef {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]connector.ScopeRef, 0, len(values))
	for _, item := range values {
		scope, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ref := syncScopeRefFromMap(scope, bindingID)
		if len(ref) > 0 {
			out = append(out, ref)
		}
	}
	return out
}

func syncScopeRefFromMap(scope map[string]any, bindingID string) connector.ScopeRef {
	ref := connector.ScopeRef{}
	objectKey := firstSourceNonBlank(
		stringFromAny(scope["object_key"]),
		objectKeyFromSourceTreeKey(stringFromAny(scope["key"]), bindingID),
	)
	nodeRef := firstSourceNonBlank(
		stringFromAny(scope["node_ref"]),
		stringFromAny(scope["path"]),
		objectKey,
	)
	if boolFromAny(scope["is_container"]) {
		if nodeRef != "" {
			ref["node_ref"] = nodeRef
		}
		if objectKey != "" {
			ref["subtree_root"] = objectKey
		}
		return ref
	}
	if objectKey != "" {
		ref["object_key"] = objectKey
		return ref
	}
	if path := strings.TrimSpace(stringFromAny(scope["path"])); path != "" {
		ref["path"] = path
		return ref
	}
	if nodeRef != "" {
		ref["node_ref"] = nodeRef
	}
	return ref
}

func objectKeyFromSourceTreeKey(key, bindingID string) string {
	key = strings.TrimSpace(key)
	bindingID = strings.TrimSpace(bindingID)
	if key == "" || bindingID == "" || !strings.HasPrefix(key, bindingID+":") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(key, bindingID+":"))
}

func firstSourceNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringFromAny(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func boolFromAny(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func stringListFromAny(value any) []string {
	switch v := value.(type) {
	case []string:
		return compactSourceStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return compactSourceStrings(out)
	default:
		return nil
	}
}

func compactSourceStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func scopedSyncRequestID(requestID string, index int) string {
	if requestID == "" || index == 0 {
		return requestID
	}
	return fmt.Sprintf("%s-%d", requestID, index+1)
}

func syncRunIntentToResponse(intent scheduleengine.SyncRunIntent) SyncRunIntentResponse {
	run := intent.Run
	return SyncRunIntentResponse{
		RunID:             run.RunID,
		JobID:             run.RunID,
		SourceID:          run.SourceID,
		BindingID:         run.BindingID,
		BindingGeneration: run.BindingGeneration,
		Status:            run.Status,
		TriggerType:       run.TriggerType,
		ScopeType:         run.ScopeType,
		ScopeRef:          store.CloneJSON(run.ScopeRef),
		Created:           intent.Created,
	}
}

func sourceSummaryToResponse(summary store.SourceSummary) SourceSummaryResponse {
	resp := SourceSummaryResponse{
		SourceID:            summary.SourceID,
		BindingID:           summary.BindingID,
		TotalObjects:        summary.TotalObjects,
		DocumentObjects:     summary.DocumentObjects,
		ContainerObjects:    summary.ContainerObjects,
		NewCount:            summary.NewCount,
		ModifiedCount:       summary.ModifiedCount,
		DeletedCount:        summary.DeletedCount,
		UnchangedCount:      summary.UnchangedCount,
		PendingTaskCount:    summary.PendingTaskCount,
		RunningTaskCount:    summary.RunningTaskCount,
		SubmittedTaskCount:  summary.SubmittedTaskCount,
		FailedTaskCount:     summary.FailedTaskCount,
		SucceededTaskCount:  summary.SucceededTaskCount,
		SupersededTaskCount: summary.SupersededTaskCount,
		StorageBytes:        summary.StorageBytes,
		LastSuccessAt:       summary.LastSuccessAt,
		LastError:           store.CloneJSON(summary.LastError),
	}
	for _, binding := range summary.Bindings {
		resp.Bindings = append(resp.Bindings, sourceSummaryToResponse(binding))
	}
	return resp
}

func sourceSummaryMap(summary SourceSummaryResponse) map[string]any {
	out := map[string]any{
		"source_id":             summary.SourceID,
		"total_objects":         summary.TotalObjects,
		"document_objects":      summary.DocumentObjects,
		"container_objects":     summary.ContainerObjects,
		"new_count":             summary.NewCount,
		"modified_count":        summary.ModifiedCount,
		"deleted_count":         summary.DeletedCount,
		"unchanged_count":       summary.UnchangedCount,
		"pending_task_count":    summary.PendingTaskCount,
		"running_task_count":    summary.RunningTaskCount,
		"submitted_task_count":  summary.SubmittedTaskCount,
		"failed_task_count":     summary.FailedTaskCount,
		"succeeded_task_count":  summary.SucceededTaskCount,
		"superseded_task_count": summary.SupersededTaskCount,
		"storage_bytes":         summary.StorageBytes,
		"total_document_count":  summary.DocumentObjects,
		"parsed_document_count": summary.SucceededTaskCount,
		"pending_pull_count":    summary.PendingTaskCount + summary.RunningTaskCount + summary.SubmittedTaskCount,
	}
	if summary.BindingID != "" {
		out["binding_id"] = summary.BindingID
	}
	if summary.LastSuccessAt != nil {
		out["last_success_at"] = summary.LastSuccessAt
	}
	if len(summary.LastError) > 0 {
		out["last_error"] = summary.LastError
	}
	if len(summary.Bindings) > 0 {
		bindings := make([]any, 0, len(summary.Bindings))
		for _, binding := range summary.Bindings {
			bindings = append(bindings, sourceSummaryMap(binding))
		}
		out["bindings"] = bindings
	}
	return out
}
