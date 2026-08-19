package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func executorComponentDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestHostRegistryStoresCapabilitiesWithoutExecutors(t *testing.T) {
	registry := NewHostRegistry()
	registry.RegisterHost("lazymind", HostRegistration{Capabilities: map[string]bool{"web": true}})
	registry.RegisterHost("codex", HostRegistration{AllowAllCapabilities: true, AllowLegacyTools: true})
	if !reflect.DeepEqual(registry.Hosts(), []string{"codex", "lazymind"}) {
		t.Fatalf("hosts=%v", registry.Hosts())
	}
	if ok, missing := registry.Supports("lazymind", []string{"web"}, nil); !ok || len(missing) != 0 {
		t.Fatalf("supported=%v missing=%v", ok, missing)
	}
	if ok, missing := registry.Supports("lazymind", []string{"shell"}, []string{"old_tool"}); ok ||
		!reflect.DeepEqual(missing, []string{"shell", "old_tool"}) {
		t.Fatalf("supported=%v missing=%v", ok, missing)
	}
	if ok, _ := registry.Supports("missing", []string{"web"}, nil); ok {
		t.Fatal("unregistered host must not be supported")
	}
}

func TestDBContextLoaderBuildsNeutralPinnedAttempt(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{},
		&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{},
		&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowSlotRevision{})
	now := time.Now().UTC()
	graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion,
		Nodes: map[string]graphengine.CompiledNode{"write": {ID: "write", Prompt: "write report",
			Acceptance: []string{"clear"}, Outputs: []string{"report", "notes"},
			RequiredOutputs: []string{"report"}, Capabilities: []string{"web"}}},
		MaterialCardinalities: map[string]string{"report": "single", "notes": "list", "images": "list"}}
	if err := db.Create(&orm.WorkflowRevision{ID: "revision-1", WorkflowResourceID: "resource-1", RevisionNo: 1,
		CompiledGraph: graph.JSON(), GraphSchemaVersion: graph.SchemaVersion, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	workflowYAML := []byte("slots:\n  - {id: report, cardinality: single}\n  - {id: notes, cardinality: list}\n")
	if err := db.Create(&orm.WorkflowBlob{Hash: "workflow-yaml", Size: int64(len(workflowYAML)), Content: workflowYAML,
		CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	blobHash := "workflow-yaml"
	if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: "revision-1", Path: "workflow.yaml",
		EntryType: "file", BlobHash: &blobHash, Size: int64(len(workflowYAML))}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "session-1", ConversationID: "conversation-1", WorkflowID: "workflow-1",
		WorkflowRevisionID: "revision-1", ControllerHost: "lazymind", OriginHost: "lazymind", CreateUserID: "user-1",
		Status: "active", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt-1", SessionID: "session-1", StepID: "write",
		Attempt: 2, TaskID: "task-1", Status: "queued", Validity: "effective", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(AttemptContext{Operation: "retry", Objective: "updated objective",
		Inputs: map[string]any{"images": map[string]any{"source_revision_id": "stale-provisional"}}})
	if err := db.Create(&orm.WorkflowOutbox{ID: "outbox-1", AttemptID: "attempt-1", SessionID: "session-1",
		PayloadJSON: payload, Status: "pending", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: "binding-1", SessionID: "session-1", AttemptID: "attempt-1",
		MaterialID: "brief", MaterialRevisionID: "resource-binding-1", SourceType: "input_resource", SourceID: "input-1",
		SourceRevision: "3", ContentHash: "sha256:value", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	for index, revisionID := range []string{"image-revision-1", "image-revision-2"} {
		listIndex := index
		if err := db.Create(&orm.WorkflowSlotRevision{ID: revisionID, SessionID: "session-1",
			SlotID: "images", Revision: index + 1, ListIndex: &listIndex, Selected: true,
			Slot: "images", StepID: "source", Attempt: 1, Validity: "effective",
			CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: "image-binding-" + revisionID,
			SessionID: "session-1", AttemptID: "attempt-1", MaterialID: "images",
			MaterialRevisionID: revisionID, SourceType: "artifact",
			// Reverse timestamps: list_index, not insertion time or UUID, must win.
			CreatedAt: now.Add(time.Duration(2-index) * time.Second)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	value, err := (DBContextLoader{DB: db}).LoadAttemptContext(context.Background(), "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if value.AttemptID != "attempt-1" || value.AttemptNo != 2 || value.Operation != "retry" ||
		value.Prompt != "write report" || !reflect.DeepEqual(value.DeclaredOutputs, []string{"report", "notes"}) ||
		!reflect.DeepEqual(value.RequiredOutputs, []string{"report"}) || value.OutputCardinality["report"] != "single" ||
		value.OutputCardinality["notes"] != "list" {
		t.Fatalf("context=%#v", value)
	}
	if value.Metadata["task_id"] != "task-1" || value.Metadata["controller_host"] != "lazymind" {
		t.Fatalf("metadata=%v", value.Metadata)
	}
	input := value.Inputs["brief"].(map[string]any)
	if input["source_id"] != "input-1" || input["source_revision_id"] != "resource-binding-1" {
		t.Fatalf("input=%v", input)
	}
	images := value.Inputs["images"].([]map[string]any)
	if len(images) != 2 || images[0]["source_revision_id"] != "image-revision-1" ||
		images[1]["source_revision_id"] != "image-revision-2" {
		t.Fatalf("list input=%v", images)
	}
	raw, _ := json.Marshal(value)
	for _, forbidden := range []string{"api_key", "db_dsn", "workspace_path"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("private field leaked: %s", raw)
		}
	}
}

func TestDBArtifactSinkIsIdempotentAndEmitsRevisionEvents(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{}, &orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{})
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{ID: "session-1", ConversationID: "conversation-1", WorkflowID: "workflow-1",
		CreateUserID: "user-1", Status: "active", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	sink := DBArtifactSink{DB: db}
	ctx := AttemptContext{AttemptID: "attempt-1", SessionID: "session-1", StepID: "write", AttemptNo: 1,
		OutputCardinality: map[string]string{"report": "single", "attachments": "list"}}
	first := Artifact{Slot: "report", ContentType: "text", Seq: 1, Value: json.RawMessage(`{"text":"one","caption":"first result"}`)}
	if err := sink.Save(context.Background(), ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := sink.Save(context.Background(), ctx, first); err != nil {
		t.Fatal(err)
	}
	second := Artifact{Slot: "report", ContentType: "text", Seq: 2, Value: json.RawMessage(`{"text":"two"}`)}
	if err := sink.Save(context.Background(), ctx, second); err != nil {
		t.Fatal(err)
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Order("revision").Find(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || revisions[0].Selected || !revisions[1].Selected || revisions[1].Revision != 2 {
		t.Fatalf("revisions=%#v", revisions)
	}
	var eventCount int64
	if err := db.Model(&orm.WorkflowEvent{}).Where("event_type = ?", "artifact.upsert").Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 2 {
		t.Fatalf("artifact events=%d", eventCount)
	}
	var stored orm.WorkflowHumanArtifact
	if err := db.Order("created_at ASC").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Caption == nil || *stored.Caption != "first result" {
		t.Fatalf("caption=%v", stored.Caption)
	}
	for seq := 1; seq <= 2; seq++ {
		if err := sink.Save(context.Background(), ctx, Artifact{Slot: "attachments", Seq: seq,
			ContentType: "text/plain", Value: json.RawMessage(`{"name":"file"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	var listRevisions []orm.WorkflowSlotRevision
	if err := db.Where("slot_id = ? AND selected = ?", "attachments", true).Order("list_index").Find(&listRevisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(listRevisions) != 2 || listRevisions[0].ListIndex == nil || *listRevisions[0].ListIndex != 0 ||
		listRevisions[1].ListIndex == nil || *listRevisions[1].ListIndex != 1 {
		t.Fatalf("list revisions=%#v", listRevisions)
	}
	partial := AttemptContext{AttemptID: "attempt-2", SessionID: "session-1", StepID: "write", AttemptNo: 2,
		OutputCardinality: map[string]string{"attachments": "list"}, PartialSelector: map[string][]int{"attachments": {0}}}
	if err := sink.Save(context.Background(), partial, Artifact{Slot: "attachments", Seq: 1,
		ContentType: "text/plain", Value: json.RawMessage(`{"name":"replacement"}`)}); err != nil {
		t.Fatal(err)
	}
	listRevisions = nil
	if err := db.Where("slot_id = ? AND selected = ?", "attachments", true).Order("list_index").Find(&listRevisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(listRevisions) != 2 || listRevisions[0].Revision != 2 || listRevisions[1].Revision != 1 {
		t.Fatalf("partial list revisions=%#v", listRevisions)
	}
	var session orm.WorkflowSession
	_ = db.First(&session, "id = ?", "session-1").Error
	if session.StateVersion != 5 {
		t.Fatalf("state version=%d", session.StateVersion)
	}
}

func TestDBArtifactSinkPreservesEveryListArtifact(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{}, &orm.WorkflowEvent{}, &orm.WorkflowSlotOrder{})
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{ID: "session-list", ConversationID: "conversation-1", WorkflowID: "workflow-1",
		CreateUserID: "user-1", Status: "active", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	sink := DBArtifactSink{DB: db}
	ctx := AttemptContext{AttemptID: "attempt-list", SessionID: "session-list", StepID: "render", AttemptNo: 1,
		OutputCardinality: map[string]string{"images": "list"}}
	for seq, path := range []string{"/tmp/architecture.png", "/tmp/effect.png"} {
		value, _ := json.Marshal(map[string]any{"path": path})
		if err := sink.Save(context.Background(), ctx, Artifact{Slot: "images", ContentType: "image", Seq: seq + 1, Value: value}); err != nil {
			t.Fatal(err)
		}
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND selected = ?", "session-list", true).Order("list_index").Find(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || revisions[0].ListIndex == nil || *revisions[0].ListIndex != 0 ||
		revisions[1].ListIndex == nil || *revisions[1].ListIndex != 1 {
		t.Fatalf("revisions=%#v", revisions)
	}
	var order orm.WorkflowSlotOrder
	if err := db.First(&order, "session_id = ? AND slot_id = ?", "session-list", "images").Error; err != nil {
		t.Fatal(err)
	}
	if string(order.OrderList) != "[0,1]" {
		t.Fatalf("order=%s", order.OrderList)
	}
}
