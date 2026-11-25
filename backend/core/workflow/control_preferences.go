package workflow

import (
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func projectSessionWithApprovalPreferences(db *gorm.DB, session orm.WorkflowSession, graph *graphengine.CompiledStateGraph, snapshot graphengine.RuntimeSnapshot) graphengine.Projection {
	projection := graphengine.Project(graph, snapshot)
	for id, node := range projection.Nodes {
		node.RequiresApproval = displaystate.SessionApprovalRequired(session, node.RequiresApproval, id)
		node.Mode = "auto"
		if node.RequiresApproval {
			node.Mode = "human"
		}
		projection.Nodes[id] = node
	}
	return projection
}
