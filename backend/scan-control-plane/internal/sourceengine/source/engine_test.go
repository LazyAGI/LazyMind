package source

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
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
	drifted.Name = "Different Docs"
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
	engine := newTestSourceEngine(t, repo, core, spy, now)

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

func TestAddBindingRecordsSyncJobErrorOnBindingAndCheckpoint(t *testing.T) {
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
	if len(resp.CompensationErrors) != 1 {
		t.Fatalf("expected job warning, got %+v", resp)
	}
	if len(repo.recordedJobErrors) != 1 {
		t.Fatalf("expected recorded sync job error, got %+v", repo.recordedJobErrors)
	}
	recorded := repo.recordedJobErrors[0]
	if recorded.bindingID != resp.Binding.BindingID || recorded.generation != int64(1) || recorded.lastError["message"] != "queue is down" {
		t.Fatalf("unexpected recorded job error: %+v", recorded)
	}
	binding := repo.bindings["source-1"][0]
	if binding.Status != BindingStatusActive || binding.LastError["message"] != "queue is down" {
		t.Fatalf("binding should stay active with last_error: %+v", binding)
	}
}

func TestCreateSourceDoesNotTriggerInitialSync(t *testing.T) {
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
		t.Fatalf("create source should not enqueue sync jobs: %+v", resp)
	}
	op := repo.operations["user-1\x00request-1"]
	if op.Status != OperationStatusSucceeded {
		t.Fatalf("operation should succeed without sync warning, got %+v", op)
	}
	if len(scheduler.triggered) != 0 {
		t.Fatalf("create source should not trigger initial sync: %+v", scheduler.triggered)
	}
	if len(repo.recordedJobErrors) != 0 {
		t.Fatalf("create source should not record sync job errors: %+v", repo.recordedJobErrors)
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
	triggered  []store.Binding
	manual     []scheduleengine.ManualSyncRequest
	triggerErr error
	nextSyncAt *time.Time
}

func (s *sourceScheduleSpy) BuildCheckpoint(_ context.Context, binding store.Binding, now time.Time) (store.SyncCheckpoint, error) {
	nextSyncAt := binding.NextSyncAt
	if s.nextSyncAt != nil {
		nextSyncAt = s.nextSyncAt
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
	target           connector.NormalizedTarget
	targetFunc       func(connector.ValidateTargetRequest) connector.NormalizedTarget
	validateRequests []connector.ValidateTargetRequest
}

func (c *sourceSpyConnector) Spec() connector.ConnectorSpec {
	return connector.ConnectorSpec{
		ConnectorType:         spyConnectorType,
		TargetTypes:           []connector.TargetType{spyTargetType},
		SupportsExportFormats: []connector.ExportFormat{connector.ExportFormatOriginal},
		MaxPageSize:           100,
	}
}

func (c *sourceSpyConnector) ValidateTarget(_ context.Context, req connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	c.validateRequests = append(c.validateRequests, req)
	if req.UserID == "" {
		return connector.NormalizedTarget{}, connector.NewError(connector.ErrorCodeInvalidArgument, "user_id is required")
	}
	if req.ConnectorType != spyConnectorType || req.TargetType != spyTargetType || req.TargetRef == "" {
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
	createRecords       []store.SourceCreateRecord
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

func (r *sourceEngineRepoStub) DeleteSource(context.Context, string, time.Time) (store.SourceDeleteResult, error) {
	panic("sourceEngineRepoStub.DeleteSource is not used by these tests")
}

func (r *sourceEngineRepoStub) ListBindings(_ context.Context, sourceID string) ([]store.Binding, error) {
	if _, ok := r.sources[sourceID]; !ok && len(r.bindings[sourceID]) == 0 {
		return nil, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return append([]store.Binding(nil), r.bindings[sourceID]...), nil
}

func (r *sourceEngineRepoStub) GetBinding(_ context.Context, sourceID, bindingID string) (store.Binding, error) {
	for _, binding := range r.bindings[sourceID] {
		if binding.BindingID == bindingID {
			return binding, nil
		}
	}
	return store.Binding{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
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
