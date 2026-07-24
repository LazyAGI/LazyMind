package source

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type preparedBinding struct {
	binding    store.Binding
	checkpoint store.SyncCheckpoint
}

const scanDatasetTag = "scan"

func (e *DefaultEngine) prepareCreateBinding(ctx context.Context, sourceID, datasetID, sourceName, callerID, callerName, tenantID, requestID string, bindingIndex int, input BindingInput, now time.Time) (preparedBinding, error) {
	input.ProviderOptions = providerOptionsWithActor(input.ProviderOptions, callerID, tenantID)
	if err := validateBindingInput(input, true); err != nil {
		return preparedBinding{}, err
	}
	normalized, err := e.validateTarget(ctx, callerID, input)
	if err != nil {
		return preparedBinding{}, err
	}
	displayName := bindingRootDisplayName(input.DisplayName, sourceName, normalized)
	if input.BindingID == "" {
		input.BindingID = e.newID("binding")
	}
	folderID, err := e.createCoreFolder(ctx, coreclient.CreateBindingRootDocumentRequest{
		IdempotencyKey: createFolderIdempotencyKey(callerID, requestID, input.BindingID, bindingIndex),
		DatasetID:      datasetID,
		Name:           displayName,
		UserID:         callerID,
		UserName:       callerName,
	})
	if err != nil {
		return preparedBinding{}, err
	}
	binding := e.newBinding(sourceID, folderID, displayName, input, normalized, now)
	checkpoint, err := e.schedule.BuildCheckpoint(ctx, binding, now)
	if err != nil {
		_ = e.deleteCoreFolder(ctx, datasetID, folderID, callerID)
		return preparedBinding{}, err
	}
	binding.NextSyncAt = checkpoint.NextSyncAt
	return preparedBinding{binding: binding, checkpoint: checkpoint}, nil
}

func (e *DefaultEngine) createCoreDataset(ctx context.Context, req CreateSourceRequest) (string, error) {
	resp, err := e.core.CreateDataset(ctx, coreclient.CreateDatasetRequest{
		IdempotencyKey: createDatasetIdempotencyKey(req.CallerID, req.RequestID),
		Name:           req.Name,
		DisplayName:    req.Name,
		CreatedBy:      req.CallerID,
		UserName:       req.CallerName,
		TenantID:       req.TenantID,
		Tags:           []string{scanDatasetTag},
		Algo:           e.defaultDatasetAlgoRef(),
	})
	return resp.DatasetID, err
}

func (e *DefaultEngine) defaultDatasetAlgoRef() *coreclient.DatasetAlgo {
	if strings.TrimSpace(e.defaultDatasetAlgo.AlgoID) == "" {
		return nil
	}
	algo := e.defaultDatasetAlgo
	return &algo
}

func (e *DefaultEngine) createCoreFolder(ctx context.Context, req coreclient.CreateBindingRootDocumentRequest) (string, error) {
	resp, err := e.core.CreateBindingRootDocument(ctx, req)
	return resp.DocumentID, err
}

func bindingRootDisplayName(explicit, sourceName string, target connector.NormalizedTarget) string {
	if name := strings.TrimSpace(explicit); name != "" {
		return name
	}
	if isSingleFileTarget(target) {
		if name := singleFileBindingRootDisplayName(target.DisplayName); name != "" {
			return name
		}
		if name := strings.TrimSpace(sourceName); name != "" {
			return name
		}
	}
	return strings.TrimSpace(target.DisplayName)
}

func singleFileBindingRootDisplayName(displayName string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		return ""
	}
	if ext := path.Ext(name); ext != "" {
		withoutExt := strings.TrimSpace(strings.TrimSuffix(name, ext))
		if withoutExt != "" {
			return withoutExt
		}
	}
	return name
}

func isSingleFileTarget(target connector.NormalizedTarget) bool {
	meta := target.ProviderMeta
	return meta["kind"] == "wiki_node" && meta["file_type"] == "file"
}

func (e *DefaultEngine) deleteCoreFolder(ctx context.Context, datasetID, folderID, callerID string) error {
	// TODO: Replace this best-effort root document delete if Core adds a real
	// directory subtree delete/list-children contract for binding cleanup.
	return e.core.DeleteDocument(ctx, coreclient.DeleteDocumentRequest{
		DatasetID:  datasetID,
		DocumentID: folderID,
		UserID:     callerID,
	})
}

func createDatasetIdempotencyKey(callerID, requestID string) string {
	return fmt.Sprintf("create-source:%s:%s:dataset", callerID, requestID)
}

func createFolderIdempotencyKey(callerID, requestID, bindingID string, bindingIndex int) string {
	if requestID == "" {
		return bindingFolderIdempotencyKey(bindingID, 1)
	}
	return fmt.Sprintf("create-source:%s:%s:folder:%d", callerID, requestID, bindingIndex)
}

func (e *DefaultEngine) validateTarget(ctx context.Context, callerID string, input BindingInput) (connector.NormalizedTarget, error) {
	conn, err := e.registry.Get(input.ConnectorType)
	if err != nil {
		return connector.NormalizedTarget{}, mapConnectorError(err)
	}
	target, err := conn.ValidateTarget(ctx, connector.ValidateTargetRequest{
		ConnectorType:    input.ConnectorType,
		TargetType:       input.TargetType,
		TargetRef:        input.TargetRef,
		AgentID:          input.AgentID,
		AuthConnectionID: input.AuthConnectionID,
		ProviderOptions:  connector.ProviderOptions(input.ProviderOptions),
		UserID:           callerID,
	})
	if err != nil {
		return connector.NormalizedTarget{}, mapConnectorError(err)
	}
	return target, nil
}

func (e *DefaultEngine) newBinding(sourceID, folderID, displayName string, input BindingInput, target connector.NormalizedTarget, now time.Time) store.Binding {
	bindingID := input.BindingID
	status := input.Status
	if status == "" {
		status = BindingStatusActive
	}
	return store.Binding{
		BindingID:              bindingID,
		SourceID:               sourceID,
		BindingType:            "connector_target",
		ConnectorType:          string(input.ConnectorType),
		TargetType:             string(target.TargetType),
		TargetRef:              target.TargetRef,
		TargetFingerprint:      target.TargetFingerprint,
		AgentID:                effectiveAgentID(input.AgentID, target),
		AuthConnectionID:       input.AuthConnectionID,
		ProviderOptions:        providerOptionsJSON(input.ProviderOptions),
		TreeKey:                target.RootObjectKey,
		BindingGeneration:      1,
		CoreParentDocumentID:   folderID,
		CoreParentDocumentName: displayName,
		SyncMode:               input.SyncMode,
		SchedulePolicy:         schedulePolicyForSyncMode(input.SyncMode, input.SchedulePolicy),
		IncludeExtensions:      jsonFromStrings(input.IncludeExtensions),
		ExcludeExtensions:      jsonFromStrings(input.ExcludeExtensions),
		Status:                 status,
		LastError:              store.JSON{},
		ChatEnabled:            true,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

func providerOptionsWithActor(options map[string]any, userID, tenantID string) map[string]any {
	out := make(map[string]any, len(options)+2)
	for key, value := range options {
		out[key] = value
	}
	out["user_id"] = userID
	out["tenant_id"] = tenantID
	return out
}

func effectiveAgentID(input string, target connector.NormalizedTarget) string {
	if input != "" {
		return input
	}
	return target.ProviderMeta["agent_id"]
}

func duplicateInRequest(bindings []preparedBinding) (string, bool) {
	seen := make(map[string]struct{}, len(bindings))
	for _, item := range bindings {
		key := item.binding.ConnectorType + "\x00" + item.binding.TargetType + "\x00" + item.binding.TargetFingerprint
		if _, ok := seen[key]; ok {
			return item.binding.TargetFingerprint, true
		}
		seen[key] = struct{}{}
	}
	return "", false
}
