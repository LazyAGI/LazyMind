package executor

import (
	"context"
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/graphengine"
	"strings"
	"testing"
)

func TestControlledInputSnapshotPinsReviewedTextAndListOrder(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{},
		&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.WorkflowAttemptInputBinding{},
		&orm.WorkflowSlotRevision{}, &orm.WorkflowSlotOrder{})
	graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, Nodes: map[string]graphengine.CompiledNode{"render": {ID: "render", Prompt: "render slides"}}, MaterialTypes: map[string]string{"slides": "text"}, MaterialCardinalities: map[string]string{"slides": "list"}}
	for _, row := range []any{
		&orm.WorkflowRevision{ID: "graph", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON()},
		&orm.WorkflowSession{ID: "run", WorkflowRevisionID: "graph", ControlProtocol: controlpolicy.Protocol},
		&orm.WorkflowSessionStep{ID: "attempt", SessionID: "run", StepID: "render", TaskID: "attempt", Attempt: 1, Status: "queued", Validity: "effective"},
		&orm.WorkflowOutbox{ID: "outbox", SessionID: "run", AttemptID: "attempt", PayloadJSON: json.RawMessage(`{}`)},
		&orm.WorkflowSlotOrder{SessionID: "run", SlotID: "slides", OrderList: json.RawMessage(`[1,0]`)},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for index, id := range []string{"first", "second"} {
		value := json.RawMessage(`{"type":"text","path":"/server/private/large.md"}`)
		if index == 1 {
			value = json.RawMessage(`{"text":"Approved subtitle: the edited value"}`)
		}
		i := index
		if err := db.Create(&orm.WorkflowSlotRevision{ID: id, SessionID: "run", SlotID: "slides", Slot: "slides", ListIndex: &i, ContentSnapshot: value, Revision: 1, Selected: true, Validity: "effective"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: "binding-" + id, SessionID: "run", AttemptID: "attempt", MaterialID: "slides", MaterialRevisionID: id, SourceType: "artifact"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := FreezeControlledInputs(context.Background(), db, "attempt"); err != nil {
		t.Fatal(err)
	}
	// Later changes cannot silently alter a contract already granted to an executor.
	db.Model(&orm.WorkflowSlotOrder{}).Where("session_id = ?", "run").Update("order_list", json.RawMessage(`[0,1]`))
	db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "second").Update("content_snapshot", json.RawMessage(`{"text":"later unrelated edit"}`))
	result, err := (DBContextLoader{DB: db}).LoadAttemptContext(context.Background(), "attempt")
	if err != nil {
		t.Fatal(err)
	}
	inputs := result.Inputs["slides"].([]any)
	first := inputs[0].(map[string]any)
	second := inputs[1].(map[string]any)
	if first["source_revision_id"] != "second" || first["value"].(map[string]any)["text"] != "Approved subtitle: the edited value" || second["source_revision_id"] != "first" {
		t.Fatalf("snapshot drifted: %+v", inputs)
	}
	if _, ok := second["value"]; ok {
		t.Fatal("file carrier was inlined")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "/server/private") {
		t.Fatal("private file path leaked into the contract")
	}
}
