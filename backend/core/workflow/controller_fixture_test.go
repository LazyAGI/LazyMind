package workflow

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
)

var regressionControllers = []string{"native", "deepseek-harness", "workbuddy", "codex"}

// Keep the native and hosted tests on the same schema, pinned graph and owner.
// Only the real controller binding differs; commands still use production paths.
func newControllerTestSession(t *testing.T, provider string, graph graphengine.CompiledStateGraph) (*orm.DB, orm.WorkflowSession) {
	t.Helper()
	db, hash := setupBatchTransitionSession(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.AutoMigrate(&orm.WorkflowHumanArtifact{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}))
	graph.GraphHash, graph.SchemaVersion = hash, graphengine.SchemaVersion
	must(db.Model(&orm.WorkflowRevision{}).Where("id = ?", "batch-revision").Update("compiled_graph", graph.JSON()).Error)
	putRevisionWorkflowYAML(t, db, "batch-revision", "id: batch-plugin\n")
	updates := map[string]any{"controller_host": "lazymind"}
	if provider != "native" {
		updates["controller_host"], updates["control_protocol"] = "external-agent", controlpolicy.Protocol
		if provider != "external-agent" {
			binding, err := json.Marshal(controlstore.Binding{Required: true, ConnectorID: "connector", DriverSession: "driver", Provider: provider})
			must(err)
			updates["control_binding_json"] = string(binding)
		}
	}
	must(db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(updates).Error)
	var session orm.WorkflowSession
	must(db.First(&session, "id = ?", "batch-session").Error)
	return db, session
}
