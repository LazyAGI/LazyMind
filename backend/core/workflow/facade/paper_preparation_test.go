package facade

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	for _, tc := range []struct {
		name, content string
		material      string
		status        int
	}{
		{"valid", `{"topic":"RAG chunk size","constraints":{"paper_language":"zh-CN"}}`, "paper_input", http.StatusOK},
		{"utf8-bom", "\xef\xbb\xbf" + `{"topic":"RAG chunk size"}`, "paper_input", http.StatusOK},
		{"artifact-envelope", `{"schema_version":1,"data":{"topic":"RAG chunk size"}}`, "paper_input", http.StatusOK},
		{"unknown-fields", `{"research_topic":"RAG chunk size","experiment_policy":"simulate_only"}`, "paper_input", http.StatusUnprocessableEntity},
		{"unknown-constraints", `{"topic":"RAG chunk size","constraints":{"no_local_execution":true}}`, "paper_input", http.StatusUnprocessableEntity},
		{"malformed-json", `{"topic":`, "paper_input", http.StatusUnprocessableEntity},
		{"valid-pool", `{"schema":"paper-material-pool.v2","topic":"Writing","sequence":0,"materials":[],"current":{},"history":[]}`, "seed_pool", http.StatusOK},
		{"raw-markdown-pool", "# Research notes\nObserved: 28/30", "seed_pool", http.StatusUnprocessableEntity},
		{"business-json-pool", `{"topic":"Writing"}`, "seed_pool", http.StatusUnprocessableEntity},
		{"wrong-pool-version", `{"schema":"paper-material-pool.v1","topic":"Writing","sequence":0,"materials":[],"current":{},"history":[]}`, "seed_pool", http.StatusUnprocessableEntity},
		{"raw-research-material", "# Research notes\nObserved: 28/30", "research_materials", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum := sha256.Sum256([]byte(tc.content))
			resource, _, err := h.Store.ImportInputResource(t.Context(), "owner", "input.json", "application/json", fmt.Sprintf("sha256:%x", sum), []byte(tc.content))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]any{
				"workflow_id": "paper-writing", "revision_id": "paper-revision", "idempotency_key": t.Name(),
				"input_bindings": map[string]any{tc.material: map[string]any{"resource_id": resource.ID}},
			})
			w := httptest.NewRecorder()
			h.Prepare(w, request(http.MethodPost, "/workflow-preparations", "owner", body))
			if w.Code != tc.status || tc.status != http.StatusOK && decodeEnvelope(t, w).Error.Code != "WORKFLOW_INPUT_INVALID" {
				t.Fatalf("prepare=%d %s", w.Code, w.Body.String())
			}
			if tc.status != http.StatusOK {
				var count int64
				if err := db.Table("workflow_preparations").Where("idempotency_key = ?", t.Name()).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("invalid input persisted a preparation: count=%d err=%v", count, err)
				}
			}
		})
	}
}
