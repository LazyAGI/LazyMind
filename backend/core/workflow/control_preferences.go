package workflow

import (
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func projectSessionWithApprovalPreferences(db *gorm.DB, session orm.WorkflowSession, graph *graphengine.CompiledStateGraph, snapshot graphengine.RuntimeSnapshot) graphengine.Projection {
	return projectWithApprovalPreferences(db, session.CreateUserID, session.WorkflowID, graph, snapshot)
}
