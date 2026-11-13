package executor

import (
	"encoding/json"
	"reflect"
	"testing"

	"lazymind/core/common/orm"
)

func TestKnowledgeScopeFreezesOnlySelectedSourcesAtGrant(t *testing.T) {
	db := externalExecutionDB(t)
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.Document{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.Conversation{ID: "chat", SearchConfig: json.RawMessage(`{"dataset_ids":["selected-b"],"dataset_list":[{"id":"selected-a"},{"id":"selected-b"}]}`), BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.Document{ID: "one-doc", DatasetID: "not-the-whole-library", ResourceVersion: 7},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", json.RawMessage(`{"inputs":{"document":{"source_type":"document","source_id":"one-doc","source_revision":"7"},"dataset":{"source_type":"dataset","source_id":"explicit"}}}`)).Error; err != nil {
		t.Fatal(err)
	}
	if err := FreezeControlledInputs(t.Context(), db, "attempt"); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "chat").Update("search_config", json.RawMessage(`{"dataset_ids":["later-library"]}`)).Error; err != nil {
		t.Fatal(err)
	}
	if err := FreezeControlledInputs(t.Context(), db, "attempt"); err != nil {
		t.Fatal(err)
	}
	contract, err := (DBContextLoader{DB: db}).LoadAttemptContext(t.Context(), "attempt")
	if err != nil {
		t.Fatal(err)
	}
	want := &KnowledgeScope{DatasetIDs: []string{"explicit", "selected-a", "selected-b"}, Documents: []KnowledgeDocumentRef{{DatasetID: "not-the-whole-library", DocumentID: "one-doc", ResourceVersion: 7}}}
	if !reflect.DeepEqual(contract.KnowledgeScope, want) {
		t.Fatalf("knowledge scope drifted or broadened: %+v", contract.KnowledgeScope)
	}
}

func TestKnowledgeScopeDoesNotMigrateAlreadyGrantedContract(t *testing.T) {
	db := externalExecutionDB(t)
	if err := db.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", json.RawMessage(`{"metadata":{"workflow_control_inputs_frozen":"true"}}`)).Error; err != nil {
		t.Fatal(err)
	}
	if err := FreezeControlledInputs(t.Context(), db, "attempt"); err != nil {
		t.Fatal(err)
	}
	contract, err := (DBContextLoader{DB: db}).LoadAttemptContext(t.Context(), "attempt")
	if err != nil || contract.KnowledgeScope != nil {
		t.Fatalf("legacy grant silently gained knowledge access: %+v %v", contract.KnowledgeScope, err)
	}
}
