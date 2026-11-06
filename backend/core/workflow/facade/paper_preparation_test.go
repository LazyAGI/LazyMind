package facade

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestPreparePaperWritingDoesNotRequireExperimentSetupOrEveryAlternativeSource(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "workflows", "paper-writing")
	config, err := os.ReadFile(filepath.Join(root, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(root, "scenario", "state.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compiled := graphengine.Compile(string(config), string(state), "", graphengine.ProfileRuntimeLoad)
	if !compiled.Valid {
		t.Fatal(compiled.Diagnostics)
	}
	// Starting a new task must not bypass the actual drafting node's source requirement.
	if node := graphengine.Project(compiled.Graph, graphengine.RuntimeSnapshot{}).Nodes["write_document"]; node.Readiness == "ready" {
		t.Fatal("drafting became executable without its input source")
	}
	h, db := testHandler(t)
	if err := db.AutoMigrate(&orm.WorkflowResource{}, &orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowResource{
		ID: "paper-resource", WorkflowRef: "builtin:paper-writing", WorkflowID: "paper-writing",
		OwnerUserID: "owner", OwnerScope: "owner", RelativeRoot: "paper-writing", HeadRevisionID: "paper-revision",
		Version: 1, Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{
		ID: "paper-revision", WorkflowResourceID: "paper-resource", RevisionNo: 1,
		TreeHash: "paper-tree", GraphHash: compiled.Graph.GraphHash, GraphSchemaVersion: "3",
		CompiledGraph: compiled.Graph.JSON(), CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"workflow_id": "paper-writing", "revision_id": "paper-revision",
		"idempotency_key": t.Name(), "input_bindings": map[string]any{},
		"clarification_values": map[string]any{},
		"request_context":      "基于附件 paper-materials-v4.md，从研究选题开始逐阶段写作。已有实验结果，不开展新实验。",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.Prepare(w, request(http.MethodPost, "/workflow-preparations", "owner", body))
	if w.Code != http.StatusOK {
		t.Fatalf("prepare=%d %s", w.Code, w.Body.String())
	}
	encoded, _ := json.Marshal(decodeEnvelope(t, w).Data)
	var result struct {
		Status        string   `json:"status"`
		MissingInputs []string `json:"missing_inputs"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.MissingInputs) != 0 || result.Status != "ready" {
		t.Fatalf("internal/alternative sources blocked startup: %s", encoded)
	}
}
