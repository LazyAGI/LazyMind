package facade

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

const facadeSourceWorkflow = "id: writer\nslots:\n  - {id: source, external: true}\n  - {id: result}\nsteps:\n  - {id: write}\n"
const facadeSourceState = "transitions:\n  __start__: [{to: write}]\n  write: [{to: __end__}]\nsteps:\n  write:\n    inputs: [source]\n    outputs: [result]\n    prompt: Write from the supplied material\n"

// Runtime fixtures contain the same immutable files and compiled graph as a publication.
func seedFacadeSources(t *testing.T, db *gorm.DB, revisionID, workflowYAML, stateYAML string) {
	t.Helper()
	compiled := graphengine.Compile(workflowYAML, stateYAML, "", graphengine.ProfilePublish)
	if !compiled.Valid {
		t.Fatalf("invalid publication fixture: %+v", compiled.Diagnostics)
	}
	if err := db.Model(&orm.WorkflowRevision{}).Where("id = ?", revisionID).Updates(map[string]any{"compiled_graph": compiled.Graph.JSON(), "graph_hash": compiled.GraphHash, "graph_schema_version": graphengine.SchemaVersion}).Error; err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{"workflow.yaml": workflowYAML, "scenario/state.yml": stateYAML} {
		sum := sha256.Sum256([]byte(body))
		hash := hex.EncodeToString(sum[:])
		if err := db.Create(&orm.WorkflowBlob{Hash: hash, Content: []byte(body)}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: revisionID, Path: path, EntryType: "file", BlobHash: &hash}).Error; err != nil {
			t.Fatal(err)
		}
	}
}
func TestReadinessBlocksInvalidPrepareAndConsumeWithoutExecutionRecords(t *testing.T) {
	h, db := testHandler(t)
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.TaskCenterTask{}, &orm.WorkflowResource{}, &orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowResource{ID: "readiness", WorkflowRef: "builtin:invalid", WorkflowID: "invalid", Status: "active", HeadRevisionID: "invalid-revision", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "invalid-revision", WorkflowResourceID: "readiness", RevisionNo: 1, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	// Missing publication files are a configuration failure, not a runnable package.
	w := httptest.NewRecorder()
	h.Prepare(w, request(http.MethodPost, "/workflow-preparations", "owner", []byte(`{"workflow_id":"invalid","idempotency_key":"invalid"}`)))
	if w.Code != 422 || decodeEnvelope(t, w).Error.Code != "WORKFLOW_CONFIGURATION_INVALID" {
		t.Fatalf("prepare %d %s", w.Code, w.Body.String())
	}
	prepared, _, err := h.Store.Prepare(t.Context(), "owner", "stale", "invalid", ContractVersion, []byte(`{"workflow_id":"invalid"}`), []byte(`{"status":"ready","workflow_revision":"invalid-revision"}`))
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	req := mux.SetURLVars(request(http.MethodPost, "/workflow-preparations/"+prepared.ID+"/consume", "owner", nil), map[string]string{"preparation_id": prepared.ID})
	h.Consume(w, req)
	if w.Code != 422 || decodeEnvelope(t, w).Error.Code != "WORKFLOW_CONFIGURATION_INVALID" {
		t.Fatalf("consume %d %s", w.Code, w.Body.String())
	}
	for _, model := range []any{&orm.WorkflowSession{}, &orm.TaskCenterTask{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("execution persisted: %T count=%d err=%v", model, count, err)
		}
	}
}
