package publication

import (
	"encoding/json"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
	"testing"
)

func TestPublicationPinsInputsBeforeProjectionAndRejectsMissingOutputs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowEvent{}); err != nil {
		t.Fatal(err)
	}
	policy := graphengine.RuntimePolicy{TransactionalOutputs: true, Publication: &graphengine.PublicationPolicy{Step: "assemble", RequiredSlots: []string{"result"}}, PublishedInputAliases: map[string]string{"seed": "result"}}
	compiled, _ := json.Marshal(graphengine.CompiledStateGraph{Runtime: policy})
	if err := db.Create(&orm.WorkflowRevision{ID: "r", CompiledGraph: compiled}).Error; err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "s", WorkflowID: "copied-report", WorkflowRevisionID: "r"}
	attempt := orm.WorkflowSessionStep{ID: "a", SessionID: "s", StepID: "assemble"}
	if err := Publish(db, session, attempt, *policy.Publication); err == nil {
		t.Fatal("published missing output")
	}
	output := orm.WorkflowSlotRevision{ID: "published", SessionID: "s", SlotID: "result", Slot: "result", Revision: 1, ProducerAttemptID: "a", Validity: "effective", ContentSnapshot: json.RawMessage(`{"value":"original"}`)}
	if err := db.Create(&output).Error; err != nil {
		t.Fatal(err)
	}
	if err := Publish(db, session, attempt, *policy.Publication); err != nil {
		t.Fatal(err)
	}
	// The mutable selection is deliberately different. Publication pins the old revision.
	if err := db.Create(&orm.WorkflowSlotRevision{ID: "draft", SessionID: "s", SlotID: "result", Slot: "result", Revision: 2, Validity: "effective", Selected: true, ContentSnapshot: json.RawMessage(`{"value":"draft"}`)}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := graphengine.RuntimeSnapshot{Materials: []graphengine.MaterialValue{{MaterialID: "seed", RevisionID: "old-input", Valid: true}}}
	if err := ProjectInputs(db, session, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Materials) != 1 || snapshot.Materials[0].RevisionID != "published" {
		t.Fatalf("%+v", snapshot)
	}
	graph := graphengine.Compile(`id: copy
slots:
  - {id: seed, type: json, external: true}
steps:
  - {id: consume}
`, `transitions:
  __start__: [{to: consume}]
  consume: [{to: __end__}]
steps:
  consume:
    input_expression: {material: seed}
    outputs: []
`, "", graphengine.ProfilePublish)
	if !graph.Valid {
		t.Fatalf("%+v", graph.Diagnostics)
	}
	projection := graphengine.Project(graph.Graph, snapshot)
	if len(projection.Ready) != 1 || projection.Ready[0] != "consume" {
		t.Fatalf("%+v", projection)
	}
	witnesses := projection.Nodes["consume"].Evaluation.Witnesses
	if len(witnesses) != 1 || witnesses[0].RevisionID != "published" || witnesses[0].MaterialID != "seed" {
		t.Fatalf("wrong frozen source: %+v", witnesses)
	}
	pub, err := Latest(db, "s")
	if err != nil || pub.Revisions["result"] != "published" {
		t.Fatalf("%+v %v", pub, err)
	}
	// Stale frozen input must reject publication and preserve the previous event.
	if err := db.Create(&orm.WorkflowAttemptInputBinding{ID: "bound", AttemptID: "next", SessionID: "s", MaterialID: "body", MaterialRevisionID: "draft", SourceType: "artifact"}).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "draft").Update("validity", "stale")
	if err := Publish(db, session, orm.WorkflowSessionStep{ID: "next"}, *policy.Publication); err == nil {
		t.Fatal("published stale input")
	}
	after, _ := Latest(db, "s")
	if after.AttemptID != "a" {
		t.Fatal("failed publication replaced old event")
	}
}
