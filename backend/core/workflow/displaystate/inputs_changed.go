package displaystate

import (
	"encoding/json"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
	"lazymind/core/workflow/graphsnapshot"
)

// Historical stale attempts are not a reason to wait after replacement succeeds,
// nor when the affected branch is no longer reachable. Use the current graph.
func inputsChanged(db *gorm.DB, session orm.WorkflowSession, attempts, current []orm.WorkflowSessionStep) (bool, error) {
	if session.WorkflowRevisionID == "" {
		return false, nil
	}
	stale := map[string]bool{}
	for _, step := range attempts {
		if step.Validity == "stale" {
			stale[step.StepID] = true
		}
	}
	for _, step := range current {
		if step.Status != "succeeded" {
			return false, nil
		}
		delete(stale, step.StepID)
	}
	if len(stale) == 0 {
		return false, nil
	}
	var revision orm.WorkflowRevision
	if err := db.Where("id = ?", session.WorkflowRevisionID).First(&revision).Error; err != nil {
		return false, err
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		return false, err
	}
	reachableCandidate := false
	for _, edge := range graph.ControlEdges {
		reachableCandidate = reachableCandidate || stale[edge.To]
	}
	if !reachableCandidate {
		return false, nil
	}
	snapshot, err := graphsnapshot.Load(db, session, &graph)
	if err != nil {
		return false, err
	}
	projection := graphengine.Project(&graph, snapshot)
	for id := range stale {
		node := projection.Nodes[id]
		if node.Validity == "stale" && node.Reachability == "reachable" {
			return true, nil
		}
	}
	return false, nil
}
