package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func executorComponentDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models = append(models, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{})
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
			RequiredOutputs: []string{"report"}, Capabilities: []string{"web"},
			InputTransports: map[string]string{"brief": "path", "images": "reference"}}},
		MaterialTypes:         map[string]string{"brief": "text", "images": "image"},
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
	if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: "binding-1-duplicate", SessionID: "session-1", AttemptID: "attempt-1",
		MaterialID: "brief", MaterialRevisionID: "resource-binding-1", SourceType: "input_resource", SourceID: "input-1",
		SourceRevision: "3", ContentHash: "sha256:value", CreatedAt: now.Add(time.Millisecond)}).Error; err != nil {
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
		value.OutputCardinality["notes"] != "list" || value.DeclaredInputTypes["brief"] != "text" ||
		value.DeclaredInputTypes["images"] != "image" || value.DeclaredInputTransports["brief"] != "path" ||
		value.DeclaredInputTransports["images"] != "reference" {
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
	if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: "binding-1-conflict", SessionID: "session-1", AttemptID: "attempt-1",
		MaterialID: "brief", MaterialRevisionID: "resource-binding-2", SourceType: "input_resource", SourceID: "input-2",
		SourceRevision: "4", ContentHash: "sha256:other", CreatedAt: now.Add(2 * time.Millisecond)}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = (DBContextLoader{DB: db}).LoadAttemptContext(context.Background(), "attempt-1")
	if err == nil || !strings.Contains(err.Error(), "single-cardinality material \"brief\"") {
		t.Fatalf("distinct single-cardinality bindings must fail clearly, got %v", err)
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

func TestDBArtifactSinkCanonicalizesRootMarkdownAndPreservesLineage(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{}, &orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{})
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-text-shape", ConversationID: "conversation-text-shape", WorkflowID: "workflow-text-shape",
		CreateUserID: "user-1", Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	sink := DBArtifactSink{DB: db}
	attempt := AttemptContext{
		AttemptID: "attempt-text-shape", SessionID: "session-text-shape", StepID: "write", AttemptNo: 1,
		DeclaredOutputTypes: map[string]string{"markdown": "text"},
		OutputCardinality:   map[string]string{"markdown": "single"},
	}
	if err := sink.Save(context.Background(), attempt, Artifact{
		Slot: "markdown", ContentType: "text/markdown; charset=utf-8", Seq: 1,
		Value: json.RawMessage(`"# Heading\nBody"`),
	}); err != nil {
		t.Fatalf("save markdown: %v", err)
	}
	var markdown orm.WorkflowHumanArtifact
	if err := db.Where("session_id = ? AND slot = ?", attempt.SessionID, "markdown").First(&markdown).Error; err != nil {
		t.Fatal(err)
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ?", attempt.SessionID, "markdown").Find(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 {
		t.Fatalf("revisions = %#v, want exactly one", revisions)
	}
	revision := revisions[0]
	if revision.Revision != 1 || !revision.Selected || revision.HumanArtifactID == nil ||
		*revision.HumanArtifactID != markdown.ID || revision.ProducerAttemptID != attempt.AttemptID {
		t.Fatalf("revision = %#v", revision)
	}
	var persistedSession orm.WorkflowSession
	if err := db.First(&persistedSession, "id = ?", attempt.SessionID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedSession.StateVersion != 1 {
		t.Fatalf("state version = %d, want 1", persistedSession.StateVersion)
	}
	var events []orm.WorkflowEvent
	if err := db.Where("session_id = ?", attempt.SessionID).Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "artifact.upsert" ||
		events[0].EntityID != revision.ID || events[0].StateVersion != 1 {
		t.Fatalf("events = %#v", events)
	}
	var markdownValue any
	if err := json.Unmarshal(markdown.Value, &markdownValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(markdownValue, map[string]any{"text": "# Heading\nBody"}) {
		t.Fatalf("markdown value = %#v", markdownValue)
	}
}

func TestDBArtifactSinkPreservesNonTextRootString(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{}, &orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{})
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-json-shape", ConversationID: "conversation-json-shape", WorkflowID: "workflow-json-shape",
		CreateUserID: "user-1", Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	sink := DBArtifactSink{DB: db}
	attempt := AttemptContext{
		AttemptID: "attempt-json-shape", SessionID: "session-json-shape", StepID: "write", AttemptNo: 1,
		OutputCardinality: map[string]string{"json": "single"},
	}
	if err := sink.Save(context.Background(), attempt, Artifact{
		Slot: "json", ContentType: "application/json", Seq: 1,
		Value: json.RawMessage(`"leave as JSON string"`),
	}); err != nil {
		t.Fatalf("save JSON: %v", err)
	}
	var jsonValue orm.WorkflowHumanArtifact
	if err := db.Where("session_id = ? AND slot = ?", attempt.SessionID, "json").First(&jsonValue).Error; err != nil {
		t.Fatal(err)
	}
	var structuredValue any
	if err := json.Unmarshal(jsonValue.Value, &structuredValue); err != nil {
		t.Fatal(err)
	}
	if structuredValue != "leave as JSON string" {
		t.Fatalf("JSON value = %#v", structuredValue)
	}
}

func TestDBArtifactSinkUsesDependencyInvalidation(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		consumerStatus string
		wantInUse      bool
	}{
		{name: "terminal consumer", consumerStatus: "succeeded"},
		{name: "running consumer", consumerStatus: "running", wantInUse: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db := executorComponentDB(t,
				&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
				&orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{}, &orm.WorkflowSessionStep{},
				&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
			)
			now := time.Now().UTC()
			if err := db.Create(&orm.WorkflowSession{
				ID: "session-dependency", ConversationID: "conversation-dependency", WorkflowID: "workflow-dependency",
				CreateUserID: "owner", Status: "active", CreatedAt: now, UpdatedAt: now,
			}).Error; err != nil {
				t.Fatal(err)
			}
			humanID := "old-human"
			if err := db.Create(&orm.WorkflowHumanArtifact{
				ID: humanID, SessionID: "session-dependency", Slot: "report-key", ContentType: "text",
				Value: json.RawMessage(`{"text":"old"}`), CreatedAt: now,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowSlotRevision{
				ID: "old-revision", SessionID: "session-dependency", SlotID: "report-slot",
				Revision: 1, Selected: true, HumanArtifactID: &humanID, ChangeSource: "human",
				Slot: "report-key", StepID: "source", Attempt: 1, Validity: "effective", CreatedAt: now,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowSessionStep{
				ID: "consumer-attempt", SessionID: "session-dependency", StepID: "consumer",
				Attempt: 1, TaskID: "consumer-task", Status: testCase.consumerStatus,
				Validity: "effective", CreatedAt: now, UpdatedAt: now,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowAttemptInputBinding{
				ID: "consumer-binding", SessionID: "session-dependency", AttemptID: "consumer-attempt",
				MaterialID: "report-slot", MaterialRevisionID: "old-revision",
				SourceType: "artifact", CreatedAt: now,
			}).Error; err != nil {
				t.Fatal(err)
			}

			sink := DBArtifactSink{DB: db}
			attempt := AttemptContext{
				AttemptID: "replacement-attempt", SessionID: "session-dependency", StepID: "replacement",
				AttemptNo: 2, OutputCardinality: map[string]string{"report-slot": "single"},
			}
			err := sink.Save(t.Context(), attempt, Artifact{
				Slot: "report-slot", ContentType: "text", Seq: 1,
				Value: json.RawMessage(`{"text":"replacement"}`),
			})
			if testCase.wantInUse {
				if err == nil || err.Error() != "ARTIFACT_IN_USE" {
					t.Fatalf("running consumer save error = %v", err)
				}
				var consumer orm.WorkflowSessionStep
				if err := db.First(&consumer, "id = ?", "consumer-attempt").Error; err != nil {
					t.Fatal(err)
				}
				var old orm.WorkflowSlotRevision
				if err := db.First(&old, "id = ?", "old-revision").Error; err != nil {
					t.Fatal(err)
				}
				var revisions, events, artifacts int64
				db.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ? AND slot_id = ?", "session-dependency", "report-slot").Count(&revisions)
				db.Model(&orm.WorkflowEvent{}).Where("session_id = ?", "session-dependency").Count(&events)
				db.Model(&orm.WorkflowHumanArtifact{}).Where("session_id = ?", "session-dependency").Count(&artifacts)
				var original orm.WorkflowHumanArtifact
				db.First(&original, "id = ?", humanID)
				var session orm.WorkflowSession
				db.First(&session, "id = ?", "session-dependency")
				if consumer.Validity != "effective" || !old.Selected || old.Validity != "effective" ||
					revisions != 1 || events != 0 || artifacts != 1 ||
					string(original.Value) != `{"text":"old"}` || session.StateVersion != 0 {
					t.Fatalf("running consumer mutation leaked: consumer=%#v old=%#v original=%#v revisions=%d events=%d artifacts=%d state=%d", consumer, old, original, revisions, events, artifacts, session.StateVersion)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var consumer orm.WorkflowSessionStep
			if err := db.First(&consumer, "id = ?", "consumer-attempt").Error; err != nil {
				t.Fatal(err)
			}
			if consumer.Validity != "stale" {
				t.Fatalf("terminal consumer validity = %q, want stale", consumer.Validity)
			}
		})
	}
}

func TestDBArtifactSinkIdempotentReplayWinsBeforeDependencyInvalidation(t *testing.T) {
	db := executorComponentDB(t,
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{}, &orm.WorkflowSessionStep{},
		&orm.WorkflowAttemptInputBinding{},
	)
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-idempotent-dependency", ConversationID: "conversation", WorkflowID: "workflow",
		CreateUserID: "owner", Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	sink := DBArtifactSink{DB: db}
	attempt := AttemptContext{
		AttemptID: "same-attempt", SessionID: "session-idempotent-dependency", StepID: "write",
		AttemptNo: 1, OutputCardinality: map[string]string{"report": "single"},
	}
	artifact := Artifact{Slot: "report", ContentType: "text", Seq: 1, Value: json.RawMessage(`{"text":"same"}`)}
	if err := sink.Save(t.Context(), attempt, artifact); err != nil {
		t.Fatal(err)
	}
	var revision orm.WorkflowSlotRevision
	if err := db.Where("producer_attempt_id = ? AND slot = ? AND artifact_seq = ?", "same-attempt", "report", 1).
		First(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{
		ID: "running-after-save", SessionID: attempt.SessionID, StepID: "consumer", Attempt: 1,
		TaskID: "consumer-task", Status: "running", Validity: "effective", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "binding-after-save", SessionID: attempt.SessionID, AttemptID: "running-after-save",
		MaterialID: "report", MaterialRevisionID: revision.ID, SourceType: "artifact", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := sink.Save(t.Context(), attempt, artifact); err != nil {
		t.Fatalf("idempotent replay was rejected by later consumer: %v", err)
	}
	var revisions, events int64
	db.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ?", attempt.SessionID).Count(&revisions)
	db.Model(&orm.WorkflowEvent{}).Where("session_id = ?", attempt.SessionID).Count(&events)
	var session orm.WorkflowSession
	db.First(&session, "id = ?", attempt.SessionID)
	var consumer orm.WorkflowSessionStep
	db.First(&consumer, "id = ?", "running-after-save")
	if revisions != 1 || events != 1 || session.StateVersion != 1 || consumer.Validity != "effective" {
		t.Fatalf("replay mutated state: revisions=%d events=%d state=%d consumer=%#v", revisions, events, session.StateVersion, consumer)
	}
}

func TestDBArtifactSinkConcurrentReplayPersistsOnce(t *testing.T) {
	testDB := orm.MigrateTestDB(t,
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{}, &orm.WorkflowSessionStep{},
		&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
	)
	db := testDB.DB
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-concurrent-replay", ConversationID: "conversation", WorkflowID: "workflow",
		CreateUserID: "owner", Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	sink := DBArtifactSink{DB: db}
	attempt := AttemptContext{
		AttemptID: "concurrent-replay-attempt", SessionID: "session-concurrent-replay", StepID: "write",
		AttemptNo: 1, OutputCardinality: map[string]string{"report": "single"},
	}
	artifact := Artifact{Slot: "report", ContentType: "text", Seq: 1, Value: json.RawMessage(`{"text":"same"}`)}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- sink.Save(t.Context(), attempt, artifact)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent replay: %v", err)
		}
	}
	var revisions, artifacts, events int64
	db.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ?", attempt.SessionID).Count(&revisions)
	db.Model(&orm.WorkflowHumanArtifact{}).Where("session_id = ?", attempt.SessionID).Count(&artifacts)
	db.Model(&orm.WorkflowEvent{}).Where("session_id = ?", attempt.SessionID).Count(&events)
	var session orm.WorkflowSession
	db.First(&session, "id = ?", attempt.SessionID)
	if revisions != 1 || artifacts != 1 || events != 1 || session.StateVersion != 1 {
		t.Fatalf("concurrent replay persisted duplicates: revisions=%d artifacts=%d events=%d state=%d", revisions, artifacts, events, session.StateVersion)
	}
}

func TestDBArtifactSinkSerializesWithConcurrentConsumerBinding(t *testing.T) {
	testDB := orm.MigrateTestDB(t,
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{}, &orm.WorkflowSessionStep{},
		&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
	)
	if testDB.Dialector.Name() != "postgres" {
		t.Skip("requires PostgreSQL row locking")
	}
	db := testDB.DB
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-binding-race", ConversationID: "conversation", WorkflowID: "workflow",
		CreateUserID: "owner", Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	humanID := "binding-race-human"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: "session-binding-race", Slot: "report", ContentType: "text",
		Value: json.RawMessage(`{"text":"source"}`), CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "binding-race-revision", SessionID: "session-binding-race", SlotID: "report",
		Revision: 1, Selected: true, HumanArtifactID: &humanID, Slot: "report",
		StepID: "source", Validity: "effective", ChangeSource: "human", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	bindingTx := db.Begin()
	if bindingTx.Error != nil {
		t.Fatal(bindingTx.Error)
	}
	defer bindingTx.Rollback()
	var locked orm.WorkflowSession
	if err := bindingTx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", "session-binding-race").First(&locked).Error; err != nil {
		t.Fatal(err)
	}
	if err := bindingTx.Create(&orm.WorkflowSessionStep{
		ID: "binding-race-consumer", SessionID: "session-binding-race", StepID: "consumer",
		Attempt: 1, TaskID: "consumer-task", Status: "running", Validity: "effective",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := bindingTx.Create(&orm.WorkflowAttemptInputBinding{
		ID: "binding-race-binding", SessionID: "session-binding-race", AttemptID: "binding-race-consumer",
		MaterialID: "report", MaterialRevisionID: "binding-race-revision",
		SourceType: "artifact", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- (DBArtifactSink{DB: db}).Save(t.Context(), AttemptContext{
			AttemptID: "replacement-attempt", SessionID: "session-binding-race", StepID: "replacement",
			AttemptNo: 2, OutputCardinality: map[string]string{"report": "single"},
		}, Artifact{Slot: "report", ContentType: "text", Seq: 1, Value: json.RawMessage(`{"text":"replacement"}`)})
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("mutation escaped uncommitted Session lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := bindingTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	err := <-done
	if err == nil || err.Error() != "ARTIFACT_IN_USE" {
		t.Fatalf("mutation after binding commit error = %v", err)
	}
	var old orm.WorkflowSlotRevision
	db.First(&old, "id = ?", "binding-race-revision")
	var consumer orm.WorkflowSessionStep
	db.First(&consumer, "id = ?", "binding-race-consumer")
	var revisions, events, artifacts int64
	db.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ?", "session-binding-race").Count(&revisions)
	db.Model(&orm.WorkflowEvent{}).Where("session_id = ?", "session-binding-race").Count(&events)
	db.Model(&orm.WorkflowHumanArtifact{}).Where("session_id = ?", "session-binding-race").Count(&artifacts)
	var original orm.WorkflowHumanArtifact
	db.First(&original, "id = ?", humanID)
	var originalValue any
	if err := json.Unmarshal(original.Value, &originalValue); err != nil {
		t.Fatal(err)
	}
	var session orm.WorkflowSession
	db.First(&session, "id = ?", "session-binding-race")
	if !old.Selected || consumer.Validity != "effective" || revisions != 1 || events != 0 ||
		artifacts != 1 || !reflect.DeepEqual(originalValue, map[string]any{"text": "source"}) || session.StateVersion != 0 {
		t.Fatalf("race mutation leaked: old=%#v consumer=%#v original=%#v revisions=%d events=%d artifacts=%d state=%d", old, consumer, original, revisions, events, artifacts, session.StateVersion)
	}
}

func TestDBArtifactSinkRejectsDeclaredTypeMismatch(t *testing.T) {
	db := executorComponentDB(t)
	sink := DBArtifactSink{DB: db}
	ctx := AttemptContext{AttemptID: "attempt-image", SessionID: "session-image", StepID: "enhance",
		DeclaredOutputTypes: map[string]string{"enhanced_image_output": "image"}}
	err := sink.Save(context.Background(), ctx, Artifact{Slot: "enhanced_image_output",
		ContentType: "text", Seq: 1, Value: json.RawMessage(`{"text":"BLOCKED"}`)})
	if err == nil || !strings.Contains(err.Error(), `requires content type "image"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeclaredTypeAcceptsTypedOffloadedTextCarrier(t *testing.T) {
	ctx := AttemptContext{DeclaredOutputTypes: map[string]string{"preview_html": "text"}}
	artifact := Artifact{Slot: "preview_html", ContentType: "file", Seq: 1,
		Value: json.RawMessage(`{"type":"text","path":"/tmp/preview.html"}`)}
	if err := validateDeclaredArtifactType(ctx, artifact); err != nil {
		t.Fatalf("typed text carrier rejected: %v", err)
	}
}

func TestDeclaredTypeRejectsUntypedOffloadedFile(t *testing.T) {
	ctx := AttemptContext{DeclaredOutputTypes: map[string]string{"preview_html": "text"}}
	for _, value := range []json.RawMessage{
		json.RawMessage(`{"path":"/tmp/preview.html"}`),
		json.RawMessage(`{"type":"image","path":"/tmp/preview.html"}`),
	} {
		err := validateDeclaredArtifactType(ctx, Artifact{Slot: "preview_html", ContentType: "file", Value: value})
		if err == nil || !strings.Contains(err.Error(), `requires content type "text"`) {
			t.Fatalf("unexpected error for carrier %s: %v", value, err)
		}
	}
}

func TestDBArtifactSinkLegacyAttemptWithoutManifestDefaultsSingle(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{}, &orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{},
		&orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{})
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{ID: "session-legacy", WorkflowRevisionID: "revision-legacy",
		CreateUserID: "user-1", Status: "active", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	sink := DBArtifactSink{DB: db}
	ctx := AttemptContext{AttemptID: "attempt-legacy", SessionID: "session-legacy", StepID: "write", AttemptNo: 1}
	if err := sink.Save(context.Background(), ctx, Artifact{Slot: "report", Seq: 1,
		ContentType: "text/plain", Value: json.RawMessage(`{"text":"result"}`)}); err != nil {
		t.Fatal(err)
	}

	var revision orm.WorkflowSlotRevision
	if err := db.First(&revision, "session_id = ? AND slot_id = ?", "session-legacy", "report").Error; err != nil {
		t.Fatal(err)
	}
	if revision.ListIndex != nil || revision.Revision != 1 || !revision.Selected {
		t.Fatalf("legacy revision=%#v", revision)
	}
}

func TestDBArtifactSinkAppendsListSlotsAndReplacesOnlyExplicitIndex(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{}, &orm.WorkflowEvent{}, &orm.WorkflowRevision{},
		&orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.WorkflowSlotOrder{})
	now := time.Now().UTC()
	manifest := []byte("slots:\n  - id: slide_outline\n    type: text\n    cardinality: list\n")
	if err := db.Create(&orm.WorkflowBlob{Hash: "manifest-hash", Size: int64(len(manifest)),
		FileType: "yaml", Content: manifest, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "revision-list", WorkflowResourceID: "resource-list",
		RevisionNo: 1, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: "revision-list", Path: "workflow.yaml",
		EntryType: "file", BlobHash: ptr("manifest-hash"), Size: int64(len(manifest)), FileType: "yaml"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "session-list", ConversationID: "conversation-list",
		WorkflowID: "workflow-list", WorkflowRevisionID: "revision-list", CreateUserID: "user-1",
		Status: "active", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	sink := DBArtifactSink{DB: db}
	ctx := AttemptContext{AttemptID: "attempt-list", SessionID: "session-list", StepID: "outline", AttemptNo: 1}
	for seq, text := range []string{"one", "two", "three"} {
		artifact := Artifact{Slot: "slide_outline", ContentType: "text", Seq: seq + 1,
			Value: json.RawMessage(`{"text":"` + text + `"}`)}
		if err := sink.Save(context.Background(), ctx, artifact); err != nil {
			t.Fatal(err)
		}
	}

	var selected []orm.WorkflowSlotRevision
	if err := db.Where("selected = ?", true).Order("list_index").Find(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if len(selected) != 3 || selected[0].ListIndex == nil || *selected[0].ListIndex != 0 ||
		selected[2].ListIndex == nil || *selected[2].ListIndex != 2 {
		t.Fatalf("selected appends=%#v", selected)
	}

	replacement := Artifact{Slot: "slide_outline", ContentType: "text", Seq: 4,
		Value: json.RawMessage(`{"text":"two updated","list_index":1}`)}
	if err := sink.Save(context.Background(), ctx, replacement); err != nil {
		t.Fatal(err)
	}
	selected = nil
	if err := db.Where("selected = ?", true).Order("list_index").Find(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if len(selected) != 3 || selected[1].ListIndex == nil || *selected[1].ListIndex != 1 || selected[1].Revision != 2 {
		t.Fatalf("selected after replacement=%#v", selected)
	}
	var order orm.WorkflowSlotOrder
	if err := db.First(&order, "session_id = ? AND slot_id = ?", "session-list", "slide_outline").Error; err != nil {
		t.Fatal(err)
	}
	if string(order.OrderList) != "[0,1,2]" {
		t.Fatalf("order=%s", order.OrderList)
	}

	// Package publishers resolve stable indices before emitting artifacts. A
	// first-time explicit index must still join the durable display order;
	// otherwise the revision exists but composite widgets receive no sort_order.
	explicit := Artifact{Slot: "slide_outline", ContentType: "text", Seq: 5,
		Value: json.RawMessage(`{"text":"four","list_index":3}`)}
	if err := sink.Save(context.Background(), ctx, explicit); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&order, "session_id = ? AND slot_id = ?", "session-list", "slide_outline").Error; err != nil {
		t.Fatal(err)
	}
	if string(order.OrderList) != "[0,1,2,3]" {
		t.Fatalf("order after explicit append=%s", order.OrderList)
	}
}

func ptr(value string) *string { return &value }

func TestAttemptContextHasNoLocalWorkspaceRootContract(t *testing.T) {
	body, err := json.Marshal(AttemptContext{Metadata: map[string]string{"safe": "value"}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{"workspace_root", "canonical_path", "local_fs_sources"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public context contains %s: %s", forbidden, text)
		}
	}
}
