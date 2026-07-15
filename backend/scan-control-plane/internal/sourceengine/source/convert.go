package source

import (
	"slices"
	"time"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func sourceToResponse(src store.Source) SourceResponse {
	return SourceResponse{
		SourceID:          src.SourceID,
		TenantID:          src.TenantID,
		CreatedBy:         src.CreatedBy,
		Name:              src.Name,
		DatasetID:         src.DatasetID,
		Status:            src.Status,
		SourceOptions:     store.CloneJSON(src.SourceOptions),
		IncludeExtensions: jsonStringSlice(src.IncludeExtensions, "items"),
		ExcludeExtensions: jsonStringSlice(src.ExcludeExtensions, "items"),
		ConfigVersion:     src.ConfigVersion,
		DeletedAt:         src.DeletedAt,
		CreatedAt:         src.CreatedAt,
		UpdatedAt:         src.UpdatedAt,
	}
}

func sourceListItemToResponse(record store.SourceListRecord) SourceListItemResponse {
	src := sourceToResponse(record.Source)
	return SourceListItemResponse{
		SourceID:             src.SourceID,
		TenantID:             src.TenantID,
		CreatedBy:            src.CreatedBy,
		Name:                 src.Name,
		DatasetID:            src.DatasetID,
		Status:               src.Status,
		SourceOptions:        src.SourceOptions,
		IncludeExtensions:    src.IncludeExtensions,
		ExcludeExtensions:    src.ExcludeExtensions,
		ConfigVersion:        src.ConfigVersion,
		BindingCount:         record.BindingCount,
		AuthConnectionStatus: nil,
		Summary:              record.Summary,
		DeletedAt:            src.DeletedAt,
		CreatedAt:            src.CreatedAt,
		UpdatedAt:            src.UpdatedAt,
	}
}

func bindingToResponse(binding store.Binding) SourceBindingResponse {
	return SourceBindingResponse{
		BindingID:              binding.BindingID,
		SourceID:               binding.SourceID,
		ConnectorType:          binding.ConnectorType,
		TargetType:             binding.TargetType,
		TargetRef:              binding.TargetRef,
		TargetFingerprint:      binding.TargetFingerprint,
		AgentID:                binding.AgentID,
		AuthConnectionID:       binding.AuthConnectionID,
		ProviderOptions:        store.CloneJSON(binding.ProviderOptions),
		TreeKey:                binding.TreeKey,
		BindingGeneration:      binding.BindingGeneration,
		CoreParentDocumentID:   binding.CoreParentDocumentID,
		CoreParentDocumentName: binding.CoreParentDocumentName,
		SyncMode:               binding.SyncMode,
		SchedulePolicy:         store.CloneJSON(binding.SchedulePolicy),
		NextSyncAt:             binding.NextSyncAt,
		IncludeExtensions:      jsonStringSlice(binding.IncludeExtensions, "items"),
		ExcludeExtensions:      jsonStringSlice(binding.ExcludeExtensions, "items"),
		ChatEnabled:            binding.ChatEnabled,
			Status:                 binding.Status,
		LastError:              store.CloneJSON(binding.LastError),
		DeletedAt:              binding.DeletedAt,
		CreatedAt:              binding.CreatedAt,
		UpdatedAt:              binding.UpdatedAt,
	}
}

func bindingsToResponse(bindings []store.Binding) []SourceBindingResponse {
	out := make([]SourceBindingResponse, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, bindingToResponse(binding))
	}
	return out
}

func jsonFromMap(in map[string]any) store.JSON {
	if in == nil {
		return nil
	}
	return store.CloneJSON(store.JSON(in))
}

func jsonFromStrings(items []string) store.JSON {
	if items == nil {
		return nil
	}
	values := make([]any, len(items))
	for i, item := range items {
		values[i] = item
	}
	return store.JSON{"items": values}
}

func jsonStringSlice(in store.JSON, key string) []string {
	if in == nil {
		return nil
	}
	raw, ok := in[key]
	if !ok {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return slices.Clone(values)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s, ok := value.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func providerOptionsJSON(in map[string]any) store.JSON {
	if in == nil {
		return nil
	}
	return store.CloneJSON(store.JSON(in))
}

func schedulePolicyForSyncMode(syncMode string, policy store.JSON) store.JSON {
	if syncMode != SyncModeScheduled {
		return nil
	}
	return store.CloneJSON(policy)
}

func applyDeletedAt(now time.Time) *time.Time {
	t := now
	return &t
}
