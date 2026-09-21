package workflow

import (
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
	"testing"
)

func TestApprovalPreferencesAreSharedAcrossControllers(t *testing.T) {
	for _, scope := range []string{"step", "following"} {
		t.Run(scope, func(t *testing.T) {
			db := newTestDB(t)
			if err := db.AutoMigrate(&orm.WorkflowApprovalPreference{}); err != nil {
				t.Fatal(err)
			}
			native := orm.WorkflowSession{CreateUserID: "owner", WorkflowID: "workflow", ControllerHost: "lazymind"}
			external := native
			external.ControllerHost, external.ControlProtocol = "external-agent", "external-host-v1"
			graph := &graphengine.CompiledStateGraph{
				Nodes:        map[string]graphengine.CompiledNode{"review": {ID: "review", Mode: "human"}},
				ControlEdges: []graphengine.CompiledEdge{{From: "__start__", To: "review"}},
			}
			check := func(session orm.WorkflowSession, required bool) {
				t.Helper()
				got := projectSessionWithApprovalPreferences(db.DB, session, graph, graphengine.RuntimeSnapshot{})
				if got.Nodes["review"].RequiresApproval != required {
					t.Fatalf("controller=%s required=%v projection=%+v", session.ControllerHost, required, got)
				}
			}
			if _, err := saveWorkflowApprovalPreference(db.DB, "owner", "workflow", "review", scope); err != nil {
				t.Fatal(err)
			}
			check(external, false)
			check(native, false)
			// Future sessions use the same user/workflow preference regardless of host.
			external.ID = "future-external"
			check(external, false)
		})
	}
}
