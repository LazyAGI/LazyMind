package source

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	scheduleengine "github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	spyConnectorType connector.ConnectorType = "spy"
	spyTargetType    connector.TargetType    = "spy_root"
)

func TestCreateSourcePersistsTargetsInBindingsOnly(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	core := &sourceCoreSpy{}
	spy := &sourceSpyConnector{
		target: connector.NormalizedTarget{
			TargetType:        spyTargetType,
			TargetRef:         "normalized-target",
			TargetFingerprint: "fingerprint-from-validate",
			DisplayName:       "Validated Target",
			ProviderMeta:      connector.ProviderMeta{"agent_id": "agent-from-target"},
			RootObjectKey:     "validated-root",
		},
	}
	engine := newTestSourceEngine(t, repo, core, spy, now)

	resp, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{{
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "raw-target",
			SyncMode:      SyncModeManual,
		}},
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if resp.Source.Name != "Docs" || len(resp.Bindings) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(repo.createRecords) != 1 {
		t.Fatalf("expected one persisted source record, got %d", len(repo.createRecords))
	}

	record := repo.createRecords[0]
	assertNoSourceTargetFields(t, record.Source)
	if record.Source.SourceID == "" || record.Source.DatasetID == "" {
		t.Fatalf("source identity was not persisted: %+v", record.Source)
	}
	if record.Source.Name != "Docs" || record.Source.CreatedBy != "user-1" || record.Source.TenantID != "tenant-1" {
		t.Fatalf("source fields were not persisted from source request: %+v", record.Source)
	}

	binding := record.Bindings[0]
	if binding.TargetRef != "normalized-target" {
		t.Fatalf("expected normalized target_ref in binding, got %q", binding.TargetRef)
	}
	if binding.TargetFingerprint != "fingerprint-from-validate" {
		t.Fatalf("expected connector fingerprint, got %q", binding.TargetFingerprint)
	}
	if binding.AgentID != "agent-from-target" {
		t.Fatalf("expected binding agent_id from validated target provider meta, got %q", binding.AgentID)
	}
	if binding.TreeKey != "validated-root" {
		t.Fatalf("expected connector root object key as tree_key, got %q", binding.TreeKey)
	}
	if binding.BindingGeneration != 1 || binding.CoreParentDocumentID == "" {
		t.Fatalf("binding was not initialized correctly: %+v", binding)
	}
	if len(spy.validateRequests) != 1 || spy.validateRequests[0].TargetRef != "raw-target" || spy.validateRequests[0].UserID != "user-1" {
		t.Fatalf("ValidateTarget was not called with the binding input and caller: %+v", spy.validateRequests)
	}
	if len(core.folderRequests) != 1 || core.folderRequests[0].UserID != "user-1" {
		t.Fatalf("core folder create should carry caller user id: %+v", core.folderRequests)
	}
}

func TestCreateSourceQueuesLocalWatcherStartForManualBinding(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	local := &sourceSpyConnector{
		connectorType: connector.ConnectorType(localFSConnectorType),
		targetType:    connector.TargetType(localFSTargetType),
		target: connector.NormalizedTarget{
			TargetType:        connector.TargetType(localFSTargetType),
			TargetRef:         "/workspace/docs",
			TargetFingerprint: "local_fs:agent-1:/workspace/docs",
			DisplayName:       "docs",
			ProviderMeta:      connector.ProviderMeta{"agent_id": "agent-1"},
			RootObjectKey:     "local_fs:agent-1:path:/workspace/docs",
		},
	}
	engine := newTestSourceEngine(t, repo, &sourceCoreSpy{}, local, now)

	resp, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Local_Docs",
		Bindings: []BindingInput{{
			ConnectorType: connector.ConnectorType(localFSConnectorType),
			TargetType:    connector.TargetType(localFSTargetType),
			TargetRef:     "/workspace/docs",
			SyncMode:      SyncModeManual,
		}},
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if len(repo.agentCommands) != 1 {
		t.Fatalf("expected one local watcher command, got %+v", repo.agentCommands)
	}
	command := repo.agentCommands[0]
	if _, err := strconv.ParseInt(command.CommandID, 10, 64); err != nil {
		t.Fatalf("agent command id must be numeric for file-watcher ack, got %q", command.CommandID)
	}
	if command.AgentID != "agent-1" || command.CommandType != agentCommandStartSource {
		t.Fatalf("unexpected watcher command identity: %+v", command)
	}
	if command.Payload["type"] != agentCommandStartSource || command.Payload[agentCommandRootPathKey] != "/workspace/docs" || command.Payload["skip_initial_scan"] != true {
		t.Fatalf("start_source payload does not match file-watcher contract: %+v", command.Payload)
	}
	if command.Payload["source_id"] != resp.Source.SourceID || command.Payload["tenant_id"] != "tenant-1" {
		t.Fatalf("start_source payload lost source identity: %+v", command.Payload)
	}
}

func TestCreateSourceAllowsEmptyTenant(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	core := &sourceCoreSpy{}
	spy := &sourceSpyConnector{
		target: connector.NormalizedTarget{
			TargetType:        spyTargetType,
			TargetRef:         "normalized-target",
			TargetFingerprint: "fingerprint-from-validate",
			DisplayName:       "Validated Target",
			RootObjectKey:     "validated-root",
		},
	}
	engine := newTestSourceEngine(t, repo, core, spy, now)

	_, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{{
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "raw-target",
			SyncMode:      SyncModeManual,
		}},
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if got := repo.createRecords[0].Source.TenantID; got != "" {
		t.Fatalf("expected empty tenant to be preserved, got %q", got)
	}
}

func TestCreateSourceUsesSourceNameForSingleFileBindingRoot(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	core := &sourceCoreSpy{}
	spy := &sourceSpyConnector{
		target: connector.NormalizedTarget{
			TargetType:        spyTargetType,
			TargetRef:         "wiki:space-1:node-pdf",
			TargetFingerprint: "feishu:wiki:space-1:node-pdf",
			DisplayName:       "ALCOHOLDINGS.pdf",
			ProviderMeta:      connector.ProviderMeta{"kind": "wiki_node", "file_type": "file"},
			RootObjectKey:     "feishu:wiki:space-1:node-pdf",
		},
	}
	engine := newTestSourceEngine(t, repo, core, spy, now)

	resp, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "132",
		Bindings: []BindingInput{{
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "wiki:space-1:node-pdf",
			SyncMode:      SyncModeManual,
		}},
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if len(core.folderRequests) != 1 || core.folderRequests[0].Name != "132" {
		t.Fatalf("single-file binding root should use source name, got %+v", core.folderRequests)
	}
	if len(resp.Bindings) != 1 || resp.Bindings[0].CoreParentDocumentName != "132" {
		t.Fatalf("binding should keep the core parent name, got %+v", resp.Bindings)
	}
}

func TestCreateSourceKeepsExplicitSingleFileBindingRootName(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	core := &sourceCoreSpy{}
	spy := &sourceSpyConnector{
		target: connector.NormalizedTarget{
			TargetType:        spyTargetType,
			TargetRef:         "wiki:space-1:node-pdf",
			TargetFingerprint: "feishu:wiki:space-1:node-pdf",
			DisplayName:       "ALCOHOLDINGS.pdf",
			ProviderMeta:      connector.ProviderMeta{"kind": "wiki_node", "file_type": "file"},
			RootObjectKey:     "feishu:wiki:space-1:node-pdf",
		},
	}
	engine := newTestSourceEngine(t, repo, core, spy, now)

	_, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "132",
		Bindings: []BindingInput{{
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "wiki:space-1:node-pdf",
			DisplayName:   "Reports",
			SyncMode:      SyncModeManual,
		}},
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if len(core.folderRequests) != 1 || core.folderRequests[0].Name != "Reports" {
		t.Fatalf("explicit binding root name should be kept, got %+v", core.folderRequests)
	}
}

func TestCreateSourcePreservesStructuredProviderOptions(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	spy := &sourceSpyConnector{}
	engine := newTestSourceEngine(t, repo, &sourceCoreSpy{}, spy, now)
	providerOptions := map[string]any{
		"include_patterns":        []any{"**/*.md", "**/*.docx"},
		"exclude_patterns":        []any{"**/~$*"},
		"max_object_size_bytes":   json.Number("209715200"),
		"reconcile_after_sync":    true,
		"reconcile_delay_minutes": json.Number("10"),
	}

	_, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{{
			ConnectorType:    spyConnectorType,
			TargetType:       spyTargetType,
			TargetRef:        "target-1",
			ProviderOptions:  providerOptions,
			SyncMode:         SyncModeManual,
			AuthConnectionID: "conn-1",
		}},
		SourceOptions: map[string]any{"source_type": "feishu"},
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	validateOptions := spy.validateRequests[0].ProviderOptions
	assertSourceAnySlice(t, validateOptions["include_patterns"], []string{"**/*.md", "**/*.docx"})
	assertSourceJSONNumber(t, validateOptions["max_object_size_bytes"], "209715200")
	if validateOptions["reconcile_after_sync"] != true {
		t.Fatalf("connector boundary lost bool provider option: %+v", validateOptions)
	}

	storedOptions := repo.createRecords[0].Bindings[0].ProviderOptions
	assertSourceAnySlice(t, storedOptions["include_patterns"], []string{"**/*.md", "**/*.docx"})
	assertSourceAnySlice(t, storedOptions["exclude_patterns"], []string{"**/~$*"})
	assertSourceJSONNumber(t, storedOptions["max_object_size_bytes"], "209715200")
	assertSourceJSONNumber(t, storedOptions["reconcile_delay_minutes"], "10")
	if storedOptions["reconcile_after_sync"] != true {
		t.Fatalf("store record lost bool provider option: %+v", storedOptions)
	}
}

func TestCreateSourceRejectsInvalidSchedulePolicyAsInvalidRequest(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	core := &sourceCoreSpy{}
	engine := newTestSourceEngine(t, repo, core, &sourceSpyConnector{}, now)
	_, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{{
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "target-1",
			SyncMode:      SyncModeScheduled,
			SchedulePolicy: sourceTestSchedulePolicy("Asia/Shanghai",
				sourceTestScheduleRule([]string{"everyday"}, "02:00:99"),
			),
		}},
	})
	assertSourceErrorCode(t, err, ErrCodeInvalidRequest)
	if len(core.datasetRequests) != 0 {
		t.Fatalf("invalid request should not create core dataset, got %+v", core.datasetRequests)
	}
}

func TestCreateSourceRejectsInvalidNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "Bad Name", "Bad/Name", " Docs", "知识库🙂", strings.Repeat("a", 101)} {
		t.Run(name, func(t *testing.T) {
			now := fixedSourceTestTime()
			repo := newSourceEngineRepoStub()
			core := &sourceCoreSpy{}
			engine := newTestSourceEngine(t, repo, core, &sourceSpyConnector{}, now)

			_, err := engine.CreateSource(context.Background(), CreateSourceRequest{
				CallerID:  "user-1",
				TenantID:  "tenant-1",
				RequestID: "request-1",
				Name:      name,
				Bindings: []BindingInput{{
					ConnectorType: spyConnectorType,
					TargetType:    spyTargetType,
					TargetRef:     "target-1",
					SyncMode:      SyncModeManual,
				}},
			})
			assertSourceErrorCode(t, err, ErrCodeInvalidRequest)
			if len(core.datasetRequests) != 0 {
				t.Fatalf("invalid source name should not create core dataset, got %+v", core.datasetRequests)
			}
		})
	}
}

func TestTriggerSourceSyncSplitsObjectKeysScope(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		Status:            BindingStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}}
	scheduler := &sourceScheduleSpy{}
	engine := newTestSourceEngineWithSchedule(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.TriggerSourceSync(context.Background(), TriggerSourceSyncRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		RequestID: "request-1",
		ScopeType: string(connector.ScopeTypePartial),
		ScopeRef: map[string]any{
			"object_keys": []any{"doc-1", "doc-2"},
		},
	})
	if err != nil {
		t.Fatalf("trigger source sync: %v", err)
	}
	if len(resp.RunIDs) != 2 || len(scheduler.manual) != 2 {
		t.Fatalf("expected one manual sync per object key, resp=%+v manual=%+v", resp, scheduler.manual)
	}
	if scheduler.manual[0].ScopeRef["object_key"] != "doc-1" || scheduler.manual[1].ScopeRef["object_key"] != "doc-2" {
		t.Fatalf("object_keys scope was not split: %+v", scheduler.manual)
	}
	if scheduler.manual[0].RequestID != "request-1" || scheduler.manual[1].RequestID != "request-1-2" {
		t.Fatalf("split manual sync request ids are not stable: %+v", scheduler.manual)
	}
}

func TestTriggerSourceSyncSplitsGenerateScopes(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		Status:            BindingStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}}
	scheduler := &sourceScheduleSpy{}
	engine := newTestSourceEngineWithSchedule(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.TriggerSourceSync(context.Background(), TriggerSourceSyncRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		RequestID: "request-1",
		ScopeType: string(connector.ScopeTypePartial),
		ScopeRef: map[string]any{
			"scopes": []any{
				map[string]any{
					"key":          "binding-1:feishu:wiki:space-1:node-1",
					"object_key":   "feishu:wiki:space-1:node-1",
					"node_ref":     "wiki:space-1:node-1",
					"is_document":  true,
					"is_container": true,
				},
				map[string]any{
					"object_key":  "feishu:wiki:space-1:node-2",
					"is_document": true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("trigger source sync: %v", err)
	}
	if len(resp.RunIDs) != 2 || len(scheduler.manual) != 2 {
		t.Fatalf("expected one manual sync per scope, resp=%+v manual=%+v", resp, scheduler.manual)
	}
	if scheduler.manual[0].ScopeRef["node_ref"] != "wiki:space-1:node-1" || scheduler.manual[0].ScopeRef["subtree_root"] != "feishu:wiki:space-1:node-1" {
		t.Fatalf("container scope was not converted to subtree sync: %+v", scheduler.manual[0])
	}
	if scheduler.manual[1].ScopeRef["object_key"] != "feishu:wiki:space-1:node-2" {
		t.Fatalf("document scope was not converted to object sync: %+v", scheduler.manual[1])
	}
}

func TestTriggerSourceSyncConvertsIndexedContainerObjectKeyToSubtree(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		Status:            BindingStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}}
	repo.objects["source-1\x00binding-1\x00folder-1"] = store.SourceObject{
		SourceID:    "source-1",
		BindingID:   "binding-1",
		ObjectKey:   "folder-1",
		IsContainer: true,
		HasChildren: true,
	}
	scheduler := &sourceScheduleSpy{}
	engine := newTestSourceEngineWithSchedule(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, scheduler, now)

	_, err := engine.TriggerSourceSync(context.Background(), TriggerSourceSyncRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		RequestID: "request-1",
		ScopeType: string(connector.ScopeTypePartial),
		ScopeRef:  map[string]any{"object_key": "folder-1"},
	})
	if err != nil {
		t.Fatalf("trigger source sync: %v", err)
	}
	if len(scheduler.manual) != 1 {
		t.Fatalf("expected one manual sync, got %+v", scheduler.manual)
	}
	if scheduler.manual[0].ScopeRef["node_ref"] != "folder-1" || scheduler.manual[0].ScopeRef["subtree_root"] != "folder-1" {
		t.Fatalf("indexed container object_key should be converted to subtree sync: %+v", scheduler.manual[0])
	}
}

func TestCreateSourceDuplicateConnectorFingerprintCompensates(t *testing.T) {
	t.Parallel()

	repo := newSourceEngineRepoStub()
	core := &sourceCoreSpy{}
	spy := &sourceSpyConnector{
		targetFunc: func(req connector.ValidateTargetRequest) connector.NormalizedTarget {
			return connector.NormalizedTarget{
				TargetType:        req.TargetType,
				TargetRef:         "normalized-" + req.TargetRef,
				TargetFingerprint: "same-validated-fingerprint",
				DisplayName:       req.TargetRef,
				RootObjectKey:     "root-" + req.TargetRef,
			}
		},
	}
	engine := newTestSourceEngine(t, repo, core, spy, fixedSourceTestTime())

	_, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{
			{ConnectorType: spyConnectorType, TargetType: spyTargetType, TargetRef: "a", SyncMode: SyncModeManual},
			{ConnectorType: spyConnectorType, TargetType: spyTargetType, TargetRef: "b", SyncMode: SyncModeManual},
		},
	})
	assertSourceErrorCode(t, err, ErrCodeBindingTargetDuplicated)
	if len(repo.createRecords) != 0 {
		t.Fatalf("duplicate binding request should not persist source, got %d records", len(repo.createRecords))
	}
	if len(core.deletedFolders) != 2 {
		t.Fatalf("expected both created folders to be compensated, got %v", core.deletedFolders)
	}
	if len(core.deletedDatasets) != 1 || core.deletedDatasets[0] == "" {
		t.Fatalf("expected dataset compensation, got %v", core.deletedDatasets)
	}
	if core.datasetDeletes[0].UserID != "user-1" {
		t.Fatalf("expected dataset compensation to use caller identity, got %+v", core.datasetDeletes[0])
	}
	op := repo.operations["user-1\x00request-1"]
	if op.Status != OperationStatusFailed || op.CompensationStatus != CompensationStatusSucceeded {
		t.Fatalf("operation was not marked as compensated failure: %+v", op)
	}
}

func TestListSourcesReturnsPlanFlatItems(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.listRecords = []store.SourceListRecord{{
		Source: store.Source{
			SourceID:      "source-1",
			TenantID:      "tenant-1",
			CreatedBy:     "user-1",
			Name:          "Docs",
			DatasetID:     "dataset-1",
			Status:        SourceStatusActive,
			ConfigVersion: 2,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		BindingCount: 2,
		Summary:      map[string]any{"new_count": int64(3)},
	}}
	engine := newTestSourceEngine(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, now)

	resp, err := engine.ListSources(context.Background(), ListSourcesRequest{CallerID: "user-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Fatalf("unexpected list response: %+v", resp)
	}
	item := resp.Items[0]
	if item.SourceID != "source-1" || item.Name != "Docs" || item.DatasetID != "dataset-1" || item.BindingCount != 2 || item.ConfigVersion != 2 {
		t.Fatalf("source list item was not flat plan shape: %+v", item)
	}
}

func TestListSourcesAttachesBatchAuthConnectionStatus(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.listRecords = []store.SourceListRecord{
		{Source: store.Source{SourceID: "source-1", TenantID: "tenant-1", CreatedBy: "user-1", Name: "Docs", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}, BindingCount: 2},
		{Source: store.Source{SourceID: "source-2", TenantID: "tenant-1", CreatedBy: "user-1", Name: "Sheets", DatasetID: "dataset-2", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}, BindingCount: 1},
	}
	repo.sources["source-1"] = repo.listRecords[0].Source
	repo.sources["source-2"] = repo.listRecords[1].Source
	repo.bindings["source-1"] = []store.Binding{
		{SourceID: "source-1", BindingID: "binding-1", ConnectorType: "feishu", AuthConnectionID: "auth-1", Status: BindingStatusActive},
		{SourceID: "source-1", BindingID: "binding-2", ConnectorType: "feishu", AuthConnectionID: "auth-2", Status: BindingStatusActive},
	}
	repo.bindings["source-2"] = []store.Binding{
		{SourceID: "source-2", BindingID: "binding-3", ConnectorType: "feishu", AuthConnectionID: "auth-1", Status: BindingStatusActive},
		{SourceID: "source-2", BindingID: "binding-4", ConnectorType: "local_fs", AgentID: "agent-1", Status: BindingStatusActive},
	}
	authStatus := &sourceAuthStatusStub{
		statuses: map[string]AuthConnectionStatus{
			"auth-1": {ConnectionID: "auth-1", Status: "ACTIVE"},
		},
	}
	engine := newTestSourceEngineWithScheduleAndAuthStatus(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, sourceTestScheduleEngine{}, authStatus, now)

	resp, err := engine.ListSources(context.Background(), ListSourcesRequest{CallerID: "user-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if authStatus.calls != 1 {
		t.Fatalf("expected one batch auth status call, got %d", authStatus.calls)
	}
	if !reflect.DeepEqual(authStatus.lastReq.ConnectionIDs, []string{"auth-1", "auth-2"}) {
		t.Fatalf("unexpected batch auth ids: %#v", authStatus.lastReq.ConnectionIDs)
	}
	if authStatus.lastReq.UserID != "" || authStatus.lastReq.TenantID != "tenant-1" {
		t.Fatalf("auth status request did not carry caller scope: %+v", authStatus.lastReq)
	}
	if resp.Items[0].AuthConnectionStatus == nil || resp.Items[0].AuthConnectionStatus.Status != "REVOKED" {
		t.Fatalf("source-1 should aggregate missing auth-2 as revoked: %+v", resp.Items[0].AuthConnectionStatus)
	}
	if !reflect.DeepEqual(resp.Items[0].AuthConnectionStatus.ConnectionIDs, []string{"auth-1", "auth-2"}) {
		t.Fatalf("unexpected source-1 connection ids: %#v", resp.Items[0].AuthConnectionStatus.ConnectionIDs)
	}
	if resp.Items[1].AuthConnectionStatus == nil || resp.Items[1].AuthConnectionStatus.Status != "ACTIVE" {
		t.Fatalf("source-2 should aggregate active auth: %+v", resp.Items[1].AuthConnectionStatus)
	}
}

func TestCreateSourceIdempotencyReplaysSameRequestAndRejectsDrift(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	core := &sourceCoreSpy{}
	engine := newTestSourceEngine(t, repo, core, &sourceSpyConnector{}, now)
	req := CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{{
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "target-1",
			SyncMode:      SyncModeManual,
		}},
	}

	first, err := engine.CreateSource(context.Background(), req)
	if err != nil {
		t.Fatalf("first create source: %v", err)
	}
	replay, err := engine.CreateSource(context.Background(), req)
	if err != nil {
		t.Fatalf("replay create source: %v", err)
	}
	if replay.Source.SourceID != first.Source.SourceID || replay.Source.DatasetID != first.Source.DatasetID {
		t.Fatalf("idempotent replay returned a different source: first=%+v replay=%+v", first.Source, replay.Source)
	}
	if len(core.createdDatasets) != 1 || len(core.createdFolders) != 1 || len(repo.createRecords) != 1 {
		t.Fatalf("idempotent replay should not create resources again: datasets=%v folders=%v records=%d", core.createdDatasets, core.createdFolders, len(repo.createRecords))
	}
	if core.datasetRequests[0].IdempotencyKey != "create-source:user-1:request-1:dataset" {
		t.Fatalf("core dataset create missed idempotency key: %+v", core.datasetRequests[0])
	}
	if core.datasetRequests[0].DisplayName != "Docs" {
		t.Fatalf("core dataset create missed display_name: %+v", core.datasetRequests[0])
	}
	if core.datasetRequests[0].Algo == nil || core.datasetRequests[0].Algo.AlgoID != "general_algo" || core.datasetRequests[0].Algo.DisplayName != "General" {
		t.Fatalf("core dataset create missed default algo: %+v", core.datasetRequests[0])
	}
	if core.folderRequests[0].IdempotencyKey != "create-source:user-1:request-1:folder:0" {
		t.Fatalf("core folder create missed idempotency key: %+v", core.folderRequests[0])
	}
	if core.folderRequests[0].UserID != "user-1" {
		t.Fatalf("core folder create missed caller user id: %+v", core.folderRequests[0])
	}

	drifted := req
	drifted.Name = "Different_Docs"
	_, err = engine.CreateSource(context.Background(), drifted)
	assertSourceErrorCode(t, err, ErrCodeIdempotencyKeyReused)
	if len(core.createdDatasets) != 1 || len(core.createdFolders) != 1 || len(repo.createRecords) != 1 {
		t.Fatalf("drifted idempotency request should not create resources: datasets=%v folders=%v records=%d", core.createdDatasets, core.createdFolders, len(repo.createRecords))
	}
}

func TestAddBindingRejectsDuplicateTargetAndDeletesNewFolder(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", CreatedBy: "user-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:         "binding-existing",
		SourceID:          "source-1",
		ConnectorType:     string(spyConnectorType),
		TargetType:        string(spyTargetType),
		TargetFingerprint: "same-fingerprint",
		Status:            BindingStatusActive,
	}}
	core := &sourceCoreSpy{}
	spy := &sourceSpyConnector{target: connector.NormalizedTarget{
		TargetType:        spyTargetType,
		TargetRef:         "normalized-new",
		TargetFingerprint: "same-fingerprint",
		DisplayName:       "New",
		RootObjectKey:     "new-root",
	}}
	engine := newTestSourceEngine(t, repo, core, spy, now)

	_, err := engine.AddBinding(context.Background(), "user-1", "source-1", BindingInput{
		ConnectorType: spyConnectorType,
		TargetType:    spyTargetType,
		TargetRef:     "new",
		SyncMode:      SyncModeManual,
	})
	assertSourceErrorCode(t, err, ErrCodeBindingTargetDuplicated)
	if len(repo.bindings["source-1"]) != 1 {
		t.Fatalf("duplicate binding should not be persisted: %+v", repo.bindings["source-1"])
	}
	if len(core.deletedFolders) != 1 {
		t.Fatalf("expected newly created folder to be compensated, got %v", core.deletedFolders)
	}
}

func TestUpdateBindingTargetChangeKeepsExistingTargetFieldsAndIncrementsGeneration(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", CreatedBy: "user-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:              "binding-1",
		SourceID:               "source-1",
		ConnectorType:          string(spyConnectorType),
		TargetType:             string(spyTargetType),
		TargetRef:              "old",
		TargetFingerprint:      "fp-old",
		AgentID:                "agent-1",
		TreeKey:                "root-old",
		BindingGeneration:      3,
		CoreParentDocumentID:   "folder-old",
		CoreParentDocumentName: "Old",
		SyncMode:               SyncModeManual,
		Status:                 BindingStatusActive,
		CreatedAt:              now,
		UpdatedAt:              now,
	}}
	core := &sourceCoreSpy{}
	spy := &sourceSpyConnector{
		targetFunc: func(req connector.ValidateTargetRequest) connector.NormalizedTarget {
			return connector.NormalizedTarget{
				TargetType:        req.TargetType,
				TargetRef:         "normalized-" + req.TargetRef,
				TargetFingerprint: "fp-" + req.TargetRef,
				DisplayName:       "Updated",
				RootObjectKey:     "root-" + req.TargetRef,
			}
		},
	}
	scheduler := &sourceScheduleSpy{triggerErr: errors.New("queue is down")}
	engine := newTestSourceEngineWithSchedule(t, repo, core, spy, scheduler, now)

	resp, err := engine.UpdateBinding(context.Background(), "user-1", "source-1", "binding-1", BindingInput{
		TargetRef: "new",
		SyncMode:  SyncModeManual,
	})
	if err != nil {
		t.Fatalf("update binding: %v", err)
	}
	if len(spy.validateRequests) != 1 {
		t.Fatalf("expected ValidateTarget call, got %d", len(spy.validateRequests))
	}
	if spy.validateRequests[0].ConnectorType != spyConnectorType || spy.validateRequests[0].TargetType != spyTargetType || spy.validateRequests[0].AgentID != "agent-1" {
		t.Fatalf("target update did not complete existing target fields: %+v", spy.validateRequests[0])
	}
	if resp.OldGeneration != 3 || resp.NewGeneration != 4 {
		t.Fatalf("expected generation increment, got old=%d new=%d", resp.OldGeneration, resp.NewGeneration)
	}
	if len(resp.JobIDs) != 0 || len(resp.CompensationErrors) != 0 {
		t.Fatalf("target change should not create initial sync jobs: %+v", resp)
	}
	if len(scheduler.triggered) != 0 {
		t.Fatalf("target change triggered initial sync: %+v", scheduler.triggered)
	}
	if len(repo.recordedJobErrors) != 0 {
		t.Fatalf("target change should not record sync job errors: %+v", repo.recordedJobErrors)
	}
	updated := repo.bindings["source-1"][0]
	if updated.TargetRef != "normalized-new" || updated.TargetFingerprint != "fp-new" || updated.TreeKey != "root-new" {
		t.Fatalf("target fields were not replaced from ValidateTarget: %+v", updated)
	}
	if updated.CoreParentDocumentID == "folder-old" || len(core.deletedFolders) != 1 || core.deletedFolders[0] != "folder-old" {
		t.Fatalf("old folder was not deleted after target change: updated=%+v deleted=%v", updated, core.deletedFolders)
	}
	if len(core.folderRequests) != 1 || core.folderRequests[0].UserID != "user-1" {
		t.Fatalf("target update folder create should carry caller user id: %+v", core.folderRequests)
	}
	if !repo.lastCleanup.ClearIndexedState || repo.lastCleanup.OldCoreParentDocumentID != "folder-old" {
		t.Fatalf("target change cleanup was not requested: %+v", repo.lastCleanup)
	}
}

func TestUpdateBindingLocalTargetChangeQueuesWatcherReload(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", TenantID: "tenant-1", CreatedBy: "user-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:              "binding-1",
		SourceID:               "source-1",
		ConnectorType:          localFSConnectorType,
		TargetType:             localFSTargetType,
		TargetRef:              "/workspace/old",
		TargetFingerprint:      "fp-old",
		AgentID:                "agent-1",
		TreeKey:                "local_fs:agent-1:path:/workspace/old",
		BindingGeneration:      3,
		CoreParentDocumentID:   "folder-old",
		CoreParentDocumentName: "Old",
		SyncMode:               SyncModeManual,
		Status:                 BindingStatusActive,
		CreatedAt:              now,
		UpdatedAt:              now,
	}}
	local := &sourceSpyConnector{
		connectorType: connector.ConnectorType(localFSConnectorType),
		targetType:    connector.TargetType(localFSTargetType),
		targetFunc: func(req connector.ValidateTargetRequest) connector.NormalizedTarget {
			return connector.NormalizedTarget{
				TargetType:        req.TargetType,
				TargetRef:         req.TargetRef,
				TargetFingerprint: "fp-" + req.TargetRef,
				DisplayName:       "Updated",
				ProviderMeta:      connector.ProviderMeta{"agent_id": req.AgentID},
				RootObjectKey:     "local_fs:" + req.AgentID + ":path:" + req.TargetRef,
			}
		},
	}
	engine := newTestSourceEngine(t, repo, &sourceCoreSpy{}, local, now)

	_, err := engine.UpdateBinding(context.Background(), "user-1", "source-1", "binding-1", BindingInput{
		TargetRef: "/workspace/new",
		SyncMode:  SyncModeManual,
	})
	if err != nil {
		t.Fatalf("update local binding: %v", err)
	}
	if len(repo.agentCommands) != 1 {
		t.Fatalf("expected one watcher reload command, got %+v", repo.agentCommands)
	}
	command := repo.agentCommands[0]
	if command.CommandType != agentCommandReloadSource || command.AgentID != "agent-1" {
		t.Fatalf("unexpected watcher reload command: %+v", command)
	}
	if command.Payload[agentCommandRootPathKey] != "/workspace/new" || command.Payload["source_id"] != "source-1" || command.Payload["tenant_id"] != "tenant-1" {
		t.Fatalf("reload_source payload lost updated target: %+v", command.Payload)
	}
}

func TestUpdateBindingTargetChangeKeepsExistingDisplayNameForSingleFileTarget(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", Name: "132", CreatedBy: "user-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:              "binding-1",
		SourceID:               "source-1",
		ConnectorType:          string(spyConnectorType),
		TargetType:             string(spyTargetType),
		TargetRef:              "old",
		TargetFingerprint:      "fp-old",
		TreeKey:                "root-old",
		BindingGeneration:      3,
		CoreParentDocumentID:   "folder-old",
		CoreParentDocumentName: "Reports",
		SyncMode:               SyncModeManual,
		Status:                 BindingStatusActive,
		CreatedAt:              now,
		UpdatedAt:              now,
	}}
	core := &sourceCoreSpy{}
	spy := &sourceSpyConnector{
		target: connector.NormalizedTarget{
			TargetType:        spyTargetType,
			TargetRef:         "wiki:space-1:node-pdf",
			TargetFingerprint: "feishu:wiki:space-1:node-pdf",
			DisplayName:       "ALCOHOLDINGS.pdf",
			ProviderMeta:      connector.ProviderMeta{"kind": "wiki_node", "file_type": "file"},
			RootObjectKey:     "feishu:wiki:space-1:node-pdf",
		},
	}
	engine := newTestSourceEngine(t, repo, core, spy, now)

	resp, err := engine.UpdateBinding(context.Background(), "user-1", "source-1", "binding-1", BindingInput{
		TargetRef: "wiki:space-1:node-pdf",
		SyncMode:  SyncModeManual,
	})
	if err != nil {
		t.Fatalf("update binding: %v", err)
	}
	if len(core.folderRequests) != 1 || core.folderRequests[0].Name != "Reports" {
		t.Fatalf("single-file target update should keep existing binding name, got %+v", core.folderRequests)
	}
	if resp.Binding.CoreParentDocumentName != "Reports" {
		t.Fatalf("binding response should keep existing display name, got %+v", resp.Binding)
	}
}

func TestUpdateBindingNonTargetChangeDoesNotTriggerSync(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", CreatedBy: "user-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:              "binding-1",
		SourceID:               "source-1",
		ConnectorType:          string(spyConnectorType),
		TargetType:             string(spyTargetType),
		TargetRef:              "target",
		TargetFingerprint:      "fp-target",
		TreeKey:                "root-target",
		BindingGeneration:      3,
		CoreParentDocumentID:   "folder-1",
		CoreParentDocumentName: "Before",
		SyncMode:               SyncModeManual,
		Status:                 BindingStatusActive,
		CreatedAt:              now,
		UpdatedAt:              now,
	}}
	core := &sourceCoreSpy{}
	scheduler := &sourceScheduleSpy{}
	engine := newTestSourceEngineWithSchedule(t, repo, core, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.UpdateBinding(context.Background(), "user-1", "source-1", "binding-1", BindingInput{
		DisplayName: "After",
		SyncMode:    SyncModeManual,
	})
	if err != nil {
		t.Fatalf("update binding: %v", err)
	}
	if resp.OldGeneration != 3 || resp.NewGeneration != 3 || len(resp.JobIDs) != 0 {
		t.Fatalf("non-target update should not change generation or create jobs: %+v", resp)
	}
	if len(scheduler.triggered) != 0 {
		t.Fatalf("non-target update triggered sync: %+v", scheduler.triggered)
	}
	if len(core.createdFolders) != 0 || len(core.deletedFolders) != 0 {
		t.Fatalf("non-target update touched core folders: created=%v deleted=%v", core.createdFolders, core.deletedFolders)
	}
}

func TestUpdateBindingProviderOptionsDoNotClearIndexedState(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", TenantID: "tenant-1", CreatedBy: "user-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:              "binding-1",
		SourceID:               "source-1",
		ConnectorType:          string(localFSConnectorType),
		TargetType:             string(localFSTargetType),
		TargetRef:              "/tmp/docs",
		TargetFingerprint:      "fp-/tmp/docs",
		AgentID:                "agent-1",
		ProviderOptions:        store.JSON{"user_id": "user-1", "tenant_id": "tenant-1", "include_patterns": []any{"**/*.pdf"}},
		TreeKey:                "root-target",
		BindingGeneration:      3,
		CoreParentDocumentID:   "folder-1",
		CoreParentDocumentName: "Before",
		SyncMode:               SyncModeManual,
		IncludeExtensions:      store.JSON{"items": []any{"pdf"}},
		Status:                 BindingStatusActive,
		CreatedAt:              now,
		UpdatedAt:              now,
	}}
	core := &sourceCoreSpy{}
	scheduler := &sourceScheduleSpy{}
	engine := newTestSourceEngineWithSchedule(t, repo, core, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.UpdateBinding(context.Background(), "user-1", "source-1", "binding-1", BindingInput{
		SyncMode:          SyncModeManual,
		IncludeExtensions: []string{"doc", "docx"},
		ProviderOptions:   map[string]any{"include_patterns": []any{"**/*.doc", "**/*.docx"}},
	})
	if err != nil {
		t.Fatalf("update binding: %v", err)
	}
	if resp.OldGeneration != 3 || resp.NewGeneration != 3 || len(resp.JobIDs) != 0 {
		t.Fatalf("provider option update should not change generation or create jobs: %+v", resp)
	}
	if repo.lastCleanup.ClearIndexedState {
		t.Fatalf("provider option update should not clear indexed state: %+v", repo.lastCleanup)
	}
	if len(core.createdFolders) != 0 || len(core.deletedFolders) != 0 {
		t.Fatalf("provider option update touched core folders: created=%v deleted=%v", core.createdFolders, core.deletedFolders)
	}
	if len(scheduler.triggered) != 0 {
		t.Fatalf("provider option update triggered sync: %+v", scheduler.triggered)
	}
	updated := repo.bindings["source-1"][0]
	if updated.BindingGeneration != 3 {
		t.Fatalf("binding generation changed: %+v", updated)
	}
	if !jsonEqual(updated.IncludeExtensions, store.JSON{"items": []any{"doc", "docx"}}) {
		t.Fatalf("include extensions were not updated: %+v", updated.IncludeExtensions)
	}
	wantOptions := store.JSON{"user_id": "user-1", "tenant_id": "tenant-1", "include_patterns": []any{"**/*.doc", "**/*.docx"}}
	if !jsonEqual(updated.ProviderOptions, wantOptions) {
		t.Fatalf("provider options were not updated: got=%+v want=%+v", updated.ProviderOptions, wantOptions)
	}
}

func TestUpdateBindingScheduleChangeRecomputesNextSyncAndCancelsPendingScheduledRun(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	oldNext := now.Add(time.Hour)
	newNext := now.Add(2 * time.Hour)
	oldPolicy := sourceTestSchedulePolicy("UTC", sourceTestScheduleRule([]string{"everyday"}, "02:00:00"))
	newPolicy := sourceTestSchedulePolicy("UTC", sourceTestScheduleRule([]string{"everyday"}, "04:00:00"))
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", CreatedBy: "user-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:              "binding-1",
		SourceID:               "source-1",
		ConnectorType:          string(spyConnectorType),
		TargetType:             string(spyTargetType),
		TargetRef:              "target",
		TargetFingerprint:      "fp-target",
		TreeKey:                "root-target",
		BindingGeneration:      3,
		CoreParentDocumentID:   "folder-1",
		CoreParentDocumentName: "Before",
		SyncMode:               SyncModeScheduled,
		SchedulePolicy:         oldPolicy,
		NextSyncAt:             &oldNext,
		Status:                 BindingStatusActive,
		CreatedAt:              now,
		UpdatedAt:              now,
	}}
	core := &sourceCoreSpy{}
	scheduler := &sourceScheduleSpy{nextSyncAt: &newNext}
	engine := newTestSourceEngineWithSchedule(t, repo, core, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.UpdateBinding(context.Background(), "user-1", "source-1", "binding-1", BindingInput{
		SyncMode:       SyncModeScheduled,
		SchedulePolicy: newPolicy,
	})
	if err != nil {
		t.Fatalf("update binding: %v", err)
	}
	if resp.OldGeneration != 3 || resp.NewGeneration != 3 || len(resp.JobIDs) != 0 {
		t.Fatalf("schedule update should not change generation or create jobs: %+v", resp)
	}
	if !repo.lastCleanup.CancelPendingScheduled || repo.lastCleanup.ClearIndexedState {
		t.Fatalf("schedule update should only cancel pending scheduled runs: %+v", repo.lastCleanup)
	}
	updated := repo.bindings["source-1"][0]
	if updated.NextSyncAt == nil || !updated.NextSyncAt.Equal(newNext) {
		t.Fatalf("schedule update did not refresh binding next_sync_at: %+v want=%v", updated.NextSyncAt, newNext)
	}
	if !jsonEqual(updated.SchedulePolicy, newPolicy) {
		t.Fatalf("schedule update did not persist policy: %+v", updated.SchedulePolicy)
	}
	if len(core.createdFolders) != 0 || len(core.deletedFolders) != 0 {
		t.Fatalf("schedule update touched core folders: created=%v deleted=%v", core.createdFolders, core.deletedFolders)
	}
}

func TestAddBindingDoesNotTriggerInitialSync(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", CreatedBy: "user-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	scheduler := &sourceScheduleSpy{triggerErr: errors.New("queue is down")}
	engine := newTestSourceEngineWithSchedule(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.AddBinding(context.Background(), "user-1", "source-1", BindingInput{
		ConnectorType: spyConnectorType,
		TargetType:    spyTargetType,
		TargetRef:     "target-1",
		SyncMode:      SyncModeManual,
	})
	if err != nil {
		t.Fatalf("add binding: %v", err)
	}
	if len(resp.JobIDs) != 0 || len(resp.CompensationErrors) != 0 {
		t.Fatalf("add binding should not create initial sync jobs: %+v", resp)
	}
	if len(scheduler.triggered) != 0 {
		t.Fatalf("add binding triggered initial sync: %+v", scheduler.triggered)
	}
	binding := repo.bindings["source-1"][0]
	if binding.Status != BindingStatusActive || len(binding.LastError) != 0 {
		t.Fatalf("binding should stay active without sync last_error: %+v", binding)
	}
	if len(repo.recordedJobErrors) != 0 {
		t.Fatalf("add binding should not record sync job errors: %+v", repo.recordedJobErrors)
	}
}

func TestCreateSourceDoesNotTriggerInitialSyncForAnySyncMode(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	scheduler := &sourceScheduleSpy{}
	engine := newTestSourceEngineWithSchedule(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{
			{
				ConnectorType: spyConnectorType,
				TargetType:    spyTargetType,
				TargetRef:     "target-1",
				SyncMode:      SyncModeManual,
			},
			{
				ConnectorType:  spyConnectorType,
				TargetType:     spyTargetType,
				TargetRef:      "target-2",
				SyncMode:       SyncModeScheduled,
				SchedulePolicy: sourceTestSchedulePolicy("UTC", sourceTestScheduleRule([]string{"everyday"}, "02:00:00")),
			},
			{
				ConnectorType: spyConnectorType,
				TargetType:    spyTargetType,
				TargetRef:     "target-3",
				SyncMode:      SyncModeWatch,
			},
		},
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if len(resp.JobIDs) != 0 || len(resp.JobErrors) != 0 {
		t.Fatalf("create source should not return initial sync jobs: %+v", resp)
	}
	op := repo.operations["user-1\x00request-1"]
	if op.Status != OperationStatusSucceeded {
		t.Fatalf("operation should succeed without initial sync enqueue, got %+v", op)
	}
	if len(scheduler.triggered) != 0 {
		t.Fatalf("create source triggered initial sync: %+v", scheduler.triggered)
	}
	if len(repo.recordedJobErrors) != 0 {
		t.Fatalf("create source should not record sync job errors: %+v", repo.recordedJobErrors)
	}
}

func TestCreateSourceDoesNotRecordInitialSyncWarning(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	scheduler := &sourceScheduleSpy{triggerErr: errors.New("queue is down")}
	engine := newTestSourceEngineWithSchedule(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{{
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "target-1",
			SyncMode:      SyncModeManual,
		}},
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if len(resp.JobIDs) != 0 || len(resp.JobErrors) != 0 {
		t.Fatalf("create source should not return initial sync warning: %+v", resp)
	}
	op := repo.operations["user-1\x00request-1"]
	if op.Status != OperationStatusSucceeded {
		t.Fatalf("operation should succeed without sync warning, got %+v", op)
	}
	if len(op.Warning) != 0 {
		t.Fatalf("operation should not persist sync warning details: %+v", op.Warning)
	}
	if len(scheduler.triggered) != 0 {
		t.Fatalf("create source triggered initial sync: %+v", scheduler.triggered)
	}
	if len(repo.recordedJobErrors) != 0 {
		t.Fatalf("create source should not record sync job errors: %+v", repo.recordedJobErrors)
	}
}

func TestCreateSourceSyncStrategyMatrix(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	cases := []struct {
		name         string
		syncMode     string
		explicitSync bool
		wantNextSync bool
	}{
		{name: "manual create only", syncMode: SyncModeManual},
		{name: "manual create and sync", syncMode: SyncModeManual, explicitSync: true},
		{name: "scheduled create only", syncMode: SyncModeScheduled, wantNextSync: true},
		{name: "scheduled create and sync", syncMode: SyncModeScheduled, explicitSync: true, wantNextSync: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := newSourceEngineRepoStub()
			scheduler := &sourceScheduleSpy{usePolicyNextSync: true}
			engine := newTestSourceEngineWithSchedule(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, scheduler, now)
			bindingInput := BindingInput{
				ConnectorType: spyConnectorType,
				TargetType:    spyTargetType,
				TargetRef:     "target-1",
				SyncMode:      tc.syncMode,
			}
			if tc.syncMode == SyncModeScheduled {
				bindingInput.SchedulePolicy = sourceTestSchedulePolicy("UTC", sourceTestScheduleRule([]string{"everyday"}, "02:00:00"))
			}

			createResp, err := engine.CreateSource(context.Background(), CreateSourceRequest{
				CallerID:  "user-1",
				TenantID:  "tenant-1",
				RequestID: "create-" + tc.name,
				Name:      "Docs",
				Bindings:  []BindingInput{bindingInput},
			})
			if err != nil {
				t.Fatalf("create source: %v", err)
			}
			if len(createResp.JobIDs) != 0 || len(createResp.JobErrors) != 0 || len(scheduler.triggered) != 0 {
				t.Fatalf("create should not enqueue initial sync: resp=%+v triggered=%+v", createResp, scheduler.triggered)
			}
			binding := repo.bindings[createResp.Source.SourceID][0]
			if tc.wantNextSync {
				if binding.NextSyncAt == nil || !binding.NextSyncAt.After(now) {
					t.Fatalf("scheduled create should keep a future next_sync_at: got=%v now=%v", binding.NextSyncAt, now)
				}
			} else if binding.NextSyncAt != nil {
				t.Fatalf("manual create should not set next_sync_at: %+v", binding.NextSyncAt)
			}

			if !tc.explicitSync {
				if len(scheduler.manual) != 0 {
					t.Fatalf("create-only should not enqueue explicit sync: %+v", scheduler.manual)
				}
				return
			}

			triggerResp, err := engine.TriggerSourceSync(context.Background(), TriggerSourceSyncRequest{
				SourceID:  createResp.Source.SourceID,
				RequestID: "sync-" + tc.name,
				ScopeType: string(connector.ScopeTypeFull),
				ScopeRef:  map[string]any{},
			})
			if err != nil {
				t.Fatalf("trigger source sync: %v", err)
			}
			if len(triggerResp.RunIDs) != 1 || len(scheduler.manual) != 1 {
				t.Fatalf("create-and-sync should enqueue one explicit sync: resp=%+v manual=%+v", triggerResp, scheduler.manual)
			}
			if scheduler.manual[0].SourceID != createResp.Source.SourceID || scheduler.manual[0].BindingID != binding.BindingID || scheduler.manual[0].ScopeType != connector.ScopeTypeFull {
				t.Fatalf("explicit sync request lost source/binding/full scope: %+v", scheduler.manual[0])
			}
			if len(scheduler.triggered) != 0 {
				t.Fatalf("explicit sync should not use initial sync path: %+v", scheduler.triggered)
			}
		})
	}
}

func TestCreateSourceReturnsOperationUpdateError(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.failUpdateOperation = true
	engine := newTestSourceEngine(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, now)

	_, err := engine.CreateSource(context.Background(), CreateSourceRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		RequestID: "request-1",
		Name:      "Docs",
		Bindings: []BindingInput{{
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "target-1",
			SyncMode:      SyncModeManual,
		}},
	})
	if err == nil || !errors.Is(err, errOperationUpdateFailed) {
		t.Fatalf("expected operation update error, got %v", err)
	}
}

func TestDeleteBindingSoftDeletesAndDeletesCoreFolder(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:            "binding-1",
		SourceID:             "source-1",
		CoreParentDocumentID: "folder-1",
		Status:               BindingStatusActive,
	}}
	core := &sourceCoreSpy{}
	engine := newTestSourceEngine(t, repo, core, &sourceSpyConnector{}, now)

	resp, err := engine.DeleteBinding(context.Background(), "source-1", "binding-1")
	if err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	if !resp.Deleted || resp.RemovedCoreParentDocumentID != "folder-1" {
		t.Fatalf("unexpected delete response: %+v", resp)
	}
	deleted := repo.bindings["source-1"][0]
	if deleted.Status != BindingStatusDeleting || deleted.DeletedAt == nil {
		t.Fatalf("binding was not soft deleted: %+v", deleted)
	}
	if len(core.deletedFolders) != 1 || core.deletedFolders[0] != "folder-1" {
		t.Fatalf("core folder was not deleted: %v", core.deletedFolders)
	}
	if len(core.deleteRequests) != 1 || core.deleteRequests[0].DatasetID != "dataset-1" || core.deleteRequests[0].DocumentID != "folder-1" {
		t.Fatalf("binding cleanup should call core document delete with dataset scope: %+v", core.deleteRequests)
	}
}

func TestDeleteBindingQueuesLocalWatcherStop(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{SourceID: "source-1", TenantID: "tenant-1", DatasetID: "dataset-1", Status: SourceStatusActive, CreatedAt: now, UpdatedAt: now}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:            "binding-1",
		SourceID:             "source-1",
		ConnectorType:        localFSConnectorType,
		TargetType:           localFSTargetType,
		TargetRef:            "/workspace/docs",
		AgentID:              "agent-1",
		CoreParentDocumentID: "folder-1",
		SyncMode:             SyncModeManual,
		Status:               BindingStatusActive,
	}}
	engine := newTestSourceEngine(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, now)

	_, err := engine.DeleteBinding(context.Background(), "source-1", "binding-1")
	if err != nil {
		t.Fatalf("delete local binding: %v", err)
	}
	if len(repo.agentCommands) != 1 {
		t.Fatalf("expected one watcher stop command, got %+v", repo.agentCommands)
	}
	command := repo.agentCommands[0]
	if command.CommandType != agentCommandStopSource || command.AgentID != "agent-1" {
		t.Fatalf("unexpected watcher stop command: %+v", command)
	}
	if command.Payload["type"] != agentCommandStopSource || command.Payload["source_id"] != "source-1" || command.Payload[agentCommandRootPathKey] != nil {
		t.Fatalf("stop_source payload should only stop by source identity: %+v", command.Payload)
	}
}

func TestDeleteSourceByDatasetIDSkipsCoreDatasetDelete(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{
		SourceID:  "source-1",
		TenantID:  "tenant-1",
		CreatedBy: "user-1",
		Name:      "Docs",
		DatasetID: "dataset-1",
		Status:    SourceStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:            "binding-1",
		SourceID:             "source-1",
		ConnectorType:        localFSConnectorType,
		TargetType:           localFSTargetType,
		TargetRef:            "/workspace/docs",
		AgentID:              "agent-1",
		CoreParentDocumentID: "folder-1",
		Status:               BindingStatusActive,
	}}
	core := &sourceCoreSpy{}
	engine := newTestSourceEngine(t, repo, core, &sourceSpyConnector{}, now)

	resp, err := engine.DeleteSourceByDatasetID(context.Background(), "dataset-1", DeleteSourceOptions{
		SkipCoreDatasetDelete: true,
	})
	if err != nil {
		t.Fatalf("delete source by dataset: %v", err)
	}
	if !resp.Deleted || resp.SourceID != "source-1" || resp.RemovedDatasetID != "dataset-1" {
		t.Fatalf("unexpected delete response: %+v", resp)
	}
	if !reflect.DeepEqual(resp.RemovedBindingIDs, []string{"binding-1"}) {
		t.Fatalf("expected removed binding id, got %v", resp.RemovedBindingIDs)
	}
	if repo.sources["source-1"].Status != "DELETING" || repo.sources["source-1"].DeletedAt == nil {
		t.Fatalf("source was not soft deleted: %+v", repo.sources["source-1"])
	}
	if repo.bindings["source-1"][0].Status != BindingStatusDeleting || repo.bindings["source-1"][0].DeletedAt == nil {
		t.Fatalf("binding was not soft deleted: %+v", repo.bindings["source-1"][0])
	}
	if len(core.deletedFolders) != 1 || core.deletedFolders[0] != "folder-1" {
		t.Fatalf("core folders should still be cleaned up, got %v", core.deletedFolders)
	}
	if len(core.deletedDatasets) != 0 {
		t.Fatalf("core dataset delete should be skipped, got %v", core.deletedDatasets)
	}
	if len(repo.agentCommands) != 1 || repo.agentCommands[0].CommandType != agentCommandStopSource {
		t.Fatalf("local watcher stop should still be queued, got %+v", repo.agentCommands)
	}
}

func TestDeleteSourceDeletesCoreDatasetByDefault(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{
		SourceID:  "source-1",
		CreatedBy: "user-1",
		DatasetID: "dataset-1",
		Status:    SourceStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	core := &sourceCoreSpy{}
	engine := newTestSourceEngine(t, repo, core, &sourceSpyConnector{}, now)

	_, err := engine.DeleteSource(context.Background(), "source-1")
	if err != nil {
		t.Fatalf("delete source: %v", err)
	}
	if len(core.deletedDatasets) != 1 || core.deletedDatasets[0] != "dataset-1" {
		t.Fatalf("core dataset should be deleted by default, got %v", core.deletedDatasets)
	}
	if len(core.datasetDeletes) != 1 || core.datasetDeletes[0].UserID != "user-1" {
		t.Fatalf("core dataset delete should use source owner, got %+v", core.datasetDeletes)
	}
}

func TestUpdateSourceWithBindingsUsesAtomicStoreContract(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{
		SourceID:      "source-1",
		CreatedBy:     "user-1",
		Name:          "Before",
		DatasetID:     "dataset-1",
		Status:        SourceStatusActive,
		ConfigVersion: 7,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	repo.bindings["source-1"] = []store.Binding{{
		BindingID:              "binding-1",
		SourceID:               "source-1",
		ConnectorType:          string(spyConnectorType),
		TargetType:             string(spyTargetType),
		TargetRef:              "old",
		TargetFingerprint:      "fp-old",
		TreeKey:                "root-old",
		BindingGeneration:      1,
		CoreParentDocumentID:   "folder-old",
		CoreParentDocumentName: "Old",
		SyncMode:               SyncModeManual,
		Status:                 BindingStatusActive,
		CreatedAt:              now,
		UpdatedAt:              now,
	}}
	repo.failUpdateMutation = true
	core := &sourceCoreSpy{}
	engine := newTestSourceEngine(t, repo, core, &sourceSpyConnector{}, now)
	name := "After"

	_, err := engine.UpdateSource(context.Background(), "user-1", "source-1", UpdateSourceRequest{
		ConfigVersion:    7,
		Name:             &name,
		BindingsProvided: true,
		Bindings: []BindingInput{{
			BindingID:     "binding-1",
			ConnectorType: spyConnectorType,
			TargetType:    spyTargetType,
			TargetRef:     "new",
			SyncMode:      SyncModeManual,
		}},
	})
	if err == nil {
		t.Fatalf("expected store mutation failure")
	}
	if repo.sources["source-1"].Name != "Before" || repo.sources["source-1"].ConfigVersion != 7 {
		t.Fatalf("source was partially updated before binding mutation: %+v", repo.sources["source-1"])
	}
	if repo.bindings["source-1"][0].TargetRef != "old" || repo.bindings["source-1"][0].BindingGeneration != 1 {
		t.Fatalf("binding was partially updated after failed mutation: %+v", repo.bindings["source-1"][0])
	}
	if len(core.createdFolders) != 1 || len(core.deletedFolders) != 1 || core.createdFolders[0] != core.deletedFolders[0] {
		t.Fatalf("new target folder was not compensated after failed mutation: created=%v deleted=%v", core.createdFolders, core.deletedFolders)
	}
}

func TestUpdateSourceWithNewBindingDoesNotTriggerInitialSync(t *testing.T) {
	t.Parallel()

	now := fixedSourceTestTime()
	repo := newSourceEngineRepoStub()
	repo.sources["source-1"] = store.Source{
		SourceID:      "source-1",
		TenantID:      "tenant-1",
		CreatedBy:     "user-1",
		Name:          "Docs",
		DatasetID:     "dataset-1",
		Status:        SourceStatusActive,
		ConfigVersion: 7,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	scheduler := &sourceScheduleSpy{triggerErr: errors.New("queue is down")}
	engine := newTestSourceEngineWithSchedule(t, repo, &sourceCoreSpy{}, &sourceSpyConnector{}, scheduler, now)

	resp, err := engine.UpdateSource(context.Background(), "user-1", "source-1", UpdateSourceRequest{
		ConfigVersion:    7,
		BindingsProvided: true,
		Bindings: []BindingInput{{
			ConnectorType:  spyConnectorType,
			TargetType:     spyTargetType,
			TargetRef:      "target-new",
			SyncMode:       SyncModeScheduled,
			SchedulePolicy: sourceTestSchedulePolicy("UTC", sourceTestScheduleRule([]string{"everyday"}, "02:00:00")),
		}},
	})
	if err != nil {
		t.Fatalf("update source: %v", err)
	}
	if len(resp.CreatedBindingIDs) != 1 || len(resp.UpdatedBindingIDs) != 0 || len(resp.RemovedBindingIDs) != 0 {
		t.Fatalf("unexpected binding mutation summary: %+v", resp)
	}
	if len(resp.JobIDs) != 0 || len(resp.JobErrors) != 0 {
		t.Fatalf("source update should not create initial sync jobs: %+v", resp)
	}
	if len(scheduler.triggered) != 0 {
		t.Fatalf("source update triggered initial sync: %+v", scheduler.triggered)
	}
	binding := repo.bindings["source-1"][0]
	if binding.Status != BindingStatusActive || len(binding.LastError) != 0 {
		t.Fatalf("new binding should stay active without sync last_error: %+v", binding)
	}
	if len(repo.recordedJobErrors) != 0 {
		t.Fatalf("source update should not record sync job errors: %+v", repo.recordedJobErrors)
	}
}

func assertNoSourceTargetFields(t *testing.T, src store.Source) {
	t.Helper()
	sourceType := reflect.TypeOf(src)
	for _, name := range []string{"TargetRef", "RootPath", "AgentID", "Provider", "Origin" + "Type"} {
		if _, ok := sourceType.FieldByName(name); ok {
			t.Fatalf("source record must not contain target field %s", name)
		}
	}
}

func newTestSourceEngine(t *testing.T, repo *sourceEngineRepoStub, core *sourceCoreSpy, spy *sourceSpyConnector, now time.Time) *DefaultEngine {
	return newTestSourceEngineWithSchedule(t, repo, core, spy, sourceTestScheduleEngine{}, now)
}

func newTestSourceEngineWithSchedule(t *testing.T, repo *sourceEngineRepoStub, core *sourceCoreSpy, spy *sourceSpyConnector, scheduler ScheduleEngine, now time.Time) *DefaultEngine {
	return newTestSourceEngineWithScheduleAndAuthStatus(t, repo, core, spy, scheduler, nil, now)
}

func newTestSourceEngineWithScheduleAndAuthStatus(t *testing.T, repo *sourceEngineRepoStub, core *sourceCoreSpy, spy *sourceSpyConnector, scheduler ScheduleEngine, authStatus AuthConnectionStatusClient, now time.Time) *DefaultEngine {
	t.Helper()
	registry, err := connector.NewDefaultConnectorRegistry(spy)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	return NewDefaultEngine(
		repo,
		registry,
		core,
		scheduler,
		WithClock(func() time.Time { return now }),
		WithIDGenerator(sourceTestIDGenerator()),
		WithDefaultDatasetAlgo(coreclient.DatasetAlgo{AlgoID: "general_algo", DisplayName: "General"}),
		WithAuthConnectionStatusClient(authStatus),
	)
}

type sourceTestScheduleEngine struct{}

func (sourceTestScheduleEngine) BuildCheckpoint(_ context.Context, binding store.Binding, now time.Time) (store.SyncCheckpoint, error) {
	return store.SyncCheckpoint{
		SourceID:          binding.SourceID,
		BindingID:         binding.BindingID,
		BindingGeneration: binding.BindingGeneration,
		NextSyncAt:        binding.NextSyncAt,
		LastError:         store.JSON{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func (sourceTestScheduleEngine) TriggerInitialSync(context.Context, store.Binding) ([]string, error) {
	return nil, nil
}

func (sourceTestScheduleEngine) EnqueueManualSync(context.Context, scheduleengine.ManualSyncRequest) (scheduleengine.SyncRunIntent, error) {
	return scheduleengine.SyncRunIntent{}, nil
}

type sourceScheduleSpy struct {
	triggered         []store.Binding
	manual            []scheduleengine.ManualSyncRequest
	triggerErr        error
	nextSyncAt        *time.Time
	usePolicyNextSync bool
}

func (s *sourceScheduleSpy) BuildCheckpoint(_ context.Context, binding store.Binding, now time.Time) (store.SyncCheckpoint, error) {
	nextSyncAt := binding.NextSyncAt
	if s.nextSyncAt != nil {
		nextSyncAt = s.nextSyncAt
	} else if s.usePolicyNextSync && binding.SyncMode == SyncModeScheduled {
		next, err := scheduleengine.NextSyncAt(binding.SchedulePolicy, now)
		if err != nil {
			return store.SyncCheckpoint{}, err
		}
		nextSyncAt = &next
	}
	return store.SyncCheckpoint{
		SourceID:          binding.SourceID,
		BindingID:         binding.BindingID,
		BindingGeneration: binding.BindingGeneration,
		NextSyncAt:        nextSyncAt,
		LastError:         store.JSON{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func (s *sourceScheduleSpy) TriggerInitialSync(_ context.Context, binding store.Binding) ([]string, error) {
	s.triggered = append(s.triggered, binding)
	if s.triggerErr != nil {
		return nil, s.triggerErr
	}
	return []string{"job-" + strconv.Itoa(len(s.triggered))}, nil
}

func (s *sourceScheduleSpy) EnqueueManualSync(_ context.Context, req scheduleengine.ManualSyncRequest) (scheduleengine.SyncRunIntent, error) {
	s.manual = append(s.manual, req)
	runID := "manual-job-" + strconv.Itoa(len(s.manual))
	return scheduleengine.SyncRunIntent{
		Run: store.SyncRun{
			RunID:     runID,
			SourceID:  req.SourceID,
			BindingID: req.BindingID,
			ScopeType: string(req.ScopeType),
			ScopeRef:  sourceTestScopeRefJSON(req.ScopeRef),
			Status:    store.SyncRunStatusPending,
		},
		Created: true,
	}, nil
}

func sourceTestScopeRefJSON(scopeRef connector.ScopeRef) store.JSON {
	out := store.JSON{}
	for key, value := range scopeRef {
		out[key] = value
	}
	return out
}

func sourceTestIDGenerator() func(string) string {
	counts := map[string]int{}
	return func(prefix string) string {
		counts[prefix]++
		return prefix + "-" + strconv.Itoa(counts[prefix])
	}
}

func fixedSourceTestTime() time.Time {
	return time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
}

func sourceTestSchedulePolicy(timezone string, rules ...store.JSON) store.JSON {
	items := make([]any, 0, len(rules))
	for _, rule := range rules {
		items = append(items, rule)
	}
	return store.JSON{
		"timezone": timezone,
		"calendar": "weekly",
		"rules":    items,
	}
}

func sourceTestScheduleRule(days []string, fireTime string) store.JSON {
	items := make([]any, 0, len(days))
	for _, day := range days {
		items = append(items, day)
	}
	return store.JSON{"days": items, "time": fireTime}
}

func assertSourceErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	if got := ErrorCodeOf(err); got != code {
		t.Fatalf("expected error code %s, got %s (%v)", code, got, err)
	}
}

func assertSourceAnySlice(t *testing.T, value any, want []string) {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("expected []any, got %#v", value)
	}
	got := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("expected string array item, got %#v", item)
		}
		got = append(got, text)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected array option: got=%v want=%v", got, want)
	}
}

func assertSourceJSONNumber(t *testing.T, value any, want string) {
	t.Helper()
	number, ok := value.(json.Number)
	if !ok {
		t.Fatalf("expected json.Number %q, got %#v", want, value)
	}
	if number.String() != want {
		t.Fatalf("unexpected number option: got=%s want=%s", number.String(), want)
	}
}

type sourceSpyConnector struct {
	connectorType    connector.ConnectorType
	targetType       connector.TargetType
	target           connector.NormalizedTarget
	targetFunc       func(connector.ValidateTargetRequest) connector.NormalizedTarget
	validateRequests []connector.ValidateTargetRequest
}

func (c *sourceSpyConnector) Spec() connector.ConnectorSpec {
	return connector.ConnectorSpec{
		ConnectorType:         c.expectedConnectorType(),
		TargetTypes:           []connector.TargetType{c.expectedTargetType()},
		SupportsExportFormats: []connector.ExportFormat{connector.ExportFormatOriginal},
		MaxPageSize:           100,
	}
}

func (c *sourceSpyConnector) ValidateTarget(_ context.Context, req connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	c.validateRequests = append(c.validateRequests, req)
	if req.UserID == "" {
		return connector.NormalizedTarget{}, connector.NewError(connector.ErrorCodeInvalidArgument, "user_id is required")
	}
	if req.ConnectorType != c.expectedConnectorType() || req.TargetType != c.expectedTargetType() || req.TargetRef == "" {
		return connector.NormalizedTarget{}, connector.NewError(connector.ErrorCodeInvalidTarget, "invalid target")
	}
	if c.targetFunc != nil {
		return c.targetFunc(req), nil
	}
	if c.target.TargetFingerprint != "" {
		return c.target, nil
	}
	return connector.NormalizedTarget{
		TargetType:        req.TargetType,
		TargetRef:         req.TargetRef,
		TargetFingerprint: req.TargetRef,
		DisplayName:       req.TargetRef,
		RootObjectKey:     req.TargetRef,
	}, nil
}

func (c *sourceSpyConnector) expectedConnectorType() connector.ConnectorType {
	if c.connectorType != "" {
		return c.connectorType
	}
	return spyConnectorType
}

func (c *sourceSpyConnector) expectedTargetType() connector.TargetType {
	if c.targetType != "" {
		return c.targetType
	}
	return spyTargetType
}

func (c *sourceSpyConnector) ListChildren(context.Context, connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "not used")
}

func (c *sourceSpyConnector) Search(context.Context, connector.SearchRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "not used")
}

func (c *sourceSpyConnector) FetchPage(context.Context, connector.FetchPageRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "not used")
}

func (c *sourceSpyConnector) ExportObject(context.Context, connector.ExportObjectRequest) (connector.ExportedObject, error) {
	return connector.ExportedObject{}, connector.NewError(connector.ErrorCodeUnsupported, "not used")
}

func (c *sourceSpyConnector) MapObject(context.Context, connector.RawObject) (connector.NormalizedSourceObject, error) {
	return connector.NormalizedSourceObject{}, connector.NewError(connector.ErrorCodeUnsupported, "not used")
}

type sourceCoreSpy struct {
	createdDatasets []string
	deletedDatasets []string
	createdFolders  []string
	deletedFolders  []string
	datasetRequests []coreclient.CreateDatasetRequest
	folderRequests  []coreclient.CreateBindingRootDocumentRequest
	datasetDeletes  []coreclient.DeleteDatasetRequest
	deleteRequests  []coreclient.DeleteDocumentRequest
	batchDeletes    []coreclient.BatchDeleteDocumentsRequest
}

func (c *sourceCoreSpy) CreateDataset(_ context.Context, req coreclient.CreateDatasetRequest) (coreclient.CreateDatasetResponse, error) {
	id := "dataset-" + strconv.Itoa(len(c.createdDatasets)+1)
	c.createdDatasets = append(c.createdDatasets, id)
	c.datasetRequests = append(c.datasetRequests, req)
	return coreclient.CreateDatasetResponse{DatasetID: id, Created: true}, nil
}

func (c *sourceCoreSpy) DeleteDataset(_ context.Context, req coreclient.DeleteDatasetRequest) error {
	c.deletedDatasets = append(c.deletedDatasets, req.DatasetID)
	c.datasetDeletes = append(c.datasetDeletes, req)
	return nil
}

func (c *sourceCoreSpy) CreateBindingRootDocument(_ context.Context, req coreclient.CreateBindingRootDocumentRequest) (coreclient.CreateBindingRootDocumentResponse, error) {
	id := "folder-" + strconv.Itoa(len(c.createdFolders)+1)
	c.createdFolders = append(c.createdFolders, id)
	c.folderRequests = append(c.folderRequests, req)
	return coreclient.CreateBindingRootDocumentResponse{DocumentID: id, Created: true}, nil
}

func (c *sourceCoreSpy) DeleteDocument(_ context.Context, req coreclient.DeleteDocumentRequest) error {
	c.deletedFolders = append(c.deletedFolders, req.DocumentID)
	c.deleteRequests = append(c.deleteRequests, req)
	return nil
}

func (c *sourceCoreSpy) BatchDeleteDocuments(_ context.Context, req coreclient.BatchDeleteDocumentsRequest) error {
	c.batchDeletes = append(c.batchDeletes, req)
	return nil
}

type sourceEngineRepoStub struct {
	operations          map[string]store.CreateOperation
	sources             map[string]store.Source
	bindings            map[string][]store.Binding
	listRecords         []store.SourceListRecord
	objects             map[string]store.SourceObject
	createRecords       []store.SourceCreateRecord
	agentCommands       []store.AgentCommand
	recordedJobErrors   []recordedSyncJobError
	lastCleanup         store.BindingUpdateCleanup
	failUpdateMutation  bool
	failUpdateOperation bool
}

type recordedSyncJobError struct {
	sourceID   string
	bindingID  string
	generation int64
	lastError  store.JSON
}

var errOperationUpdateFailed = errors.New("operation update failed")

func newSourceEngineRepoStub() *sourceEngineRepoStub {
	return &sourceEngineRepoStub{
		operations: map[string]store.CreateOperation{},
		sources:    map[string]store.Source{},
		bindings:   map[string][]store.Binding{},
		objects:    map[string]store.SourceObject{},
	}
}

func (r *sourceEngineRepoStub) GetCreateOperation(_ context.Context, callerID, requestID string) (store.CreateOperation, error) {
	op, ok := r.operations[operationKey(callerID, requestID)]
	if !ok {
		return store.CreateOperation{}, store.NewStoreError(store.ErrCodeNotFound, "operation not found")
	}
	return op, nil
}

func (r *sourceEngineRepoStub) SaveCreateOperation(_ context.Context, operation store.CreateOperation) error {
	key := operationKey(operation.CallerID, operation.RequestID)
	if _, ok := r.operations[key]; ok {
		return store.NewStoreError(store.ErrCodeIdempotencyKeyReused, "operation exists")
	}
	r.operations[key] = operation
	return nil
}

func (r *sourceEngineRepoStub) UpdateCreateOperation(_ context.Context, operation store.CreateOperation) error {
	if r.failUpdateOperation {
		return errOperationUpdateFailed
	}
	r.operations[operationKey(operation.CallerID, operation.RequestID)] = operation
	return nil
}

func (r *sourceEngineRepoStub) CreateSourceWithBindings(_ context.Context, record store.SourceCreateRecord) error {
	r.createRecords = append(r.createRecords, record)
	r.sources[record.Source.SourceID] = record.Source
	r.bindings[record.Source.SourceID] = append([]store.Binding(nil), record.Bindings...)
	r.operations[operationKey(record.Operation.CallerID, record.Operation.RequestID)] = record.Operation
	return nil
}

func (r *sourceEngineRepoStub) ListSources(context.Context, store.SourceListRequest) ([]store.SourceListRecord, int, error) {
	return append([]store.SourceListRecord(nil), r.listRecords...), len(r.listRecords), nil
}

func (r *sourceEngineRepoStub) GetSource(_ context.Context, sourceID string) (store.Source, error) {
	src, ok := r.sources[sourceID]
	if !ok {
		return store.Source{}, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return src, nil
}

func (r *sourceEngineRepoStub) GetSourceByDatasetID(_ context.Context, datasetID string) (store.Source, error) {
	for _, src := range r.sources {
		if src.DatasetID == datasetID && src.DeletedAt == nil {
			return src, nil
		}
	}
	return store.Source{}, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
}

func (r *sourceEngineRepoStub) UpdateSource(context.Context, store.Source) error {
	panic("sourceEngineRepoStub.UpdateSource is not used by these tests")
}

func (r *sourceEngineRepoStub) UpdateSourceWithBindings(_ context.Context, mutation store.SourceUpdateMutation) (store.SourceUpdateResult, error) {
	if r.failUpdateMutation {
		return store.SourceUpdateResult{}, store.NewStoreError(store.ErrCodeInternal, "mutation failed")
	}
	if _, ok := r.sources[mutation.Source.SourceID]; !ok {
		return store.SourceUpdateResult{}, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	r.sources[mutation.Source.SourceID] = mutation.Source
	for _, item := range mutation.CreateBindings {
		r.bindings[item.Binding.SourceID] = append(r.bindings[item.Binding.SourceID], item.Binding)
	}
	for _, item := range mutation.UpdateBindings {
		r.updateBindingRecord(item.Binding)
		r.lastCleanup = item.Cleanup
	}
	result := store.SourceUpdateResult{}
	for _, item := range mutation.DeleteBindings {
		deleted, cleanup := r.markBindingDeleted(item.SourceID, item.BindingID, item.DeletedAt)
		if deleted.BindingID == "" {
			return store.SourceUpdateResult{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
		}
		result.Cleanup.Add(cleanup)
	}
	return result, nil
}

func (r *sourceEngineRepoStub) DeleteSource(_ context.Context, sourceID string, deletedAt time.Time) (store.SourceDeleteResult, error) {
	src, ok := r.sources[sourceID]
	if !ok {
		return store.SourceDeleteResult{}, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	src.Status = "DELETING"
	src.DeletedAt = applyDeletedAt(deletedAt)
	src.UpdatedAt = deletedAt
	r.sources[sourceID] = src

	result := store.SourceDeleteResult{Source: src}
	for _, binding := range r.bindings[sourceID] {
		deleted, cleanup := r.markBindingDeleted(sourceID, binding.BindingID, deletedAt)
		if deleted.BindingID == "" {
			continue
		}
		result.Bindings = append(result.Bindings, deleted)
		result.Cleanup.Add(cleanup)
	}
	return result, nil
}

func (r *sourceEngineRepoStub) ListBindings(_ context.Context, sourceID string) ([]store.Binding, error) {
	if _, ok := r.sources[sourceID]; !ok && len(r.bindings[sourceID]) == 0 {
		return nil, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return append([]store.Binding(nil), r.bindings[sourceID]...), nil
}

func (r *sourceEngineRepoStub) ListBindingsBySourceIDs(_ context.Context, sourceIDs []string) ([]store.Binding, error) {
	sourceSet := map[string]struct{}{}
	for _, sourceID := range sourceIDs {
		sourceSet[sourceID] = struct{}{}
	}
	out := []store.Binding{}
	for sourceID, bindings := range r.bindings {
		if _, ok := sourceSet[sourceID]; !ok {
			continue
		}
		for _, binding := range bindings {
			if binding.Status == BindingStatusDeleting {
				continue
			}
			out = append(out, binding)
		}
	}
	return out, nil
}

func (r *sourceEngineRepoStub) GetBinding(_ context.Context, sourceID, bindingID string) (store.Binding, error) {
	for _, binding := range r.bindings[sourceID] {
		if binding.BindingID == bindingID {
			return binding, nil
		}
	}
	return store.Binding{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
}

type sourceAuthStatusStub struct {
	calls    int
	lastReq  AuthConnectionStatusRequest
	statuses map[string]AuthConnectionStatus
	err      error
}

func (s *sourceAuthStatusStub) BatchStatus(_ context.Context, req AuthConnectionStatusRequest) (map[string]AuthConnectionStatus, error) {
	s.calls++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]AuthConnectionStatus, len(s.statuses))
	for key, value := range s.statuses {
		out[key] = value
	}
	return out, nil
}

func (r *sourceEngineRepoStub) GetObject(_ context.Context, sourceID, bindingID, objectKey string) (store.SourceObject, error) {
	object, ok := r.objects[sourceID+"\x00"+bindingID+"\x00"+objectKey]
	if !ok {
		return store.SourceObject{}, store.NewStoreError(store.ErrCodeNotFound, "object not found")
	}
	return object, nil
}

func (r *sourceEngineRepoStub) FindActiveBindingByTarget(_ context.Context, sourceID, excludeBindingID, connectorType, targetType, targetFingerprint string) (store.Binding, bool, error) {
	for _, binding := range r.bindings[sourceID] {
		if binding.BindingID == excludeBindingID || binding.Status == BindingStatusDeleting {
			continue
		}
		if binding.ConnectorType == connectorType && binding.TargetType == targetType && binding.TargetFingerprint == targetFingerprint {
			return binding, true, nil
		}
	}
	return store.Binding{}, false, nil
}

func (r *sourceEngineRepoStub) AddBinding(_ context.Context, binding store.Binding, _ store.SyncCheckpoint) error {
	if _, ok := r.sources[binding.SourceID]; !ok {
		return store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	r.bindings[binding.SourceID] = append(r.bindings[binding.SourceID], binding)
	return nil
}

func (r *sourceEngineRepoStub) RecordSyncJobError(_ context.Context, sourceID, bindingID string, generation int64, lastError store.JSON, _ time.Time) error {
	r.recordedJobErrors = append(r.recordedJobErrors, recordedSyncJobError{
		sourceID:   sourceID,
		bindingID:  bindingID,
		generation: generation,
		lastError:  store.CloneJSON(lastError),
	})
	bindings := r.bindings[sourceID]
	for i := range bindings {
		if bindings[i].BindingID == bindingID && bindings[i].BindingGeneration == generation {
			bindings[i].LastError = store.CloneJSON(lastError)
			r.bindings[sourceID] = bindings
			return nil
		}
	}
	return store.NewStoreError(store.ErrCodeGenerationConflict, "binding generation is stale")
}

func (r *sourceEngineRepoStub) CreateAgentCommand(_ context.Context, command store.AgentCommand) error {
	r.agentCommands = append(r.agentCommands, command)
	return nil
}

func (r *sourceEngineRepoStub) UpdateBinding(_ context.Context, binding store.Binding, _ store.SyncCheckpoint, cleanup store.BindingUpdateCleanup) error {
	bindings := r.bindings[binding.SourceID]
	for i := range bindings {
		if bindings[i].BindingID == binding.BindingID {
			r.updateBindingRecord(binding)
			r.lastCleanup = cleanup
			return nil
		}
	}
	return store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
}

func (r *sourceEngineRepoStub) DeleteBinding(_ context.Context, sourceID, bindingID string, deletedAt time.Time) (store.BindingDeleteResult, error) {
	binding, cleanup := r.markBindingDeleted(sourceID, bindingID, deletedAt)
	if binding.BindingID == "" {
		return store.BindingDeleteResult{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
	}
	return store.BindingDeleteResult{Binding: binding, Cleanup: cleanup}, nil
}

func (r *sourceEngineRepoStub) updateBindingRecord(binding store.Binding) {
	bindings := r.bindings[binding.SourceID]
	for i := range bindings {
		if bindings[i].BindingID == binding.BindingID {
			bindings[i] = binding
			r.bindings[binding.SourceID] = bindings
			return
		}
	}
}

func (r *sourceEngineRepoStub) markBindingDeleted(sourceID, bindingID string, deletedAt time.Time) (store.Binding, store.CleanupResult) {
	bindings := r.bindings[sourceID]
	for i := range bindings {
		if bindings[i].BindingID == bindingID {
			bindings[i].Status = BindingStatusDeleting
			bindings[i].DeletedAt = applyDeletedAt(deletedAt)
			r.bindings[sourceID] = bindings
			return bindings[i], store.CleanupResult{CancelledParseTaskCount: 1}
		}
	}
	return store.Binding{}, store.CleanupResult{}
}

func (r *sourceEngineRepoStub) GetSourceSummary(context.Context, store.SourceSummaryRequest) (store.SourceSummary, error) {
	panic("sourceEngineRepoStub.GetSourceSummary is not used by these tests")
}

func operationKey(callerID, requestID string) string {
	return callerID + "\x00" + requestID
}

var _ SourceRepository = (*sourceEngineRepoStub)(nil)
var _ coreclient.ResourceClient = (*sourceCoreSpy)(nil)
var _ connector.SourceConnector = (*sourceSpyConnector)(nil)
