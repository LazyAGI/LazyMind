// Package graphsnapshot reads the durable graph facts shared by execution and display.
package graphsnapshot

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/graphengine"
	"lazymind/core/workflow/publication"
)

func Load(db *gorm.DB, session orm.WorkflowSession, graph *graphengine.CompiledStateGraph) (graphengine.RuntimeSnapshot, error) {
	var attempts []orm.WorkflowSessionStep
	if err := db.Where("session_id = ?", session.ID).Order("created_at ASC").Find(&attempts).Error; err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND selected = ?", session.ID, true).Find(&revisions).Error; err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	var inputBindings []orm.WorkflowInputBinding
	if err := db.Where("workflow_session_id = ? AND validity = 'effective'", session.ID).
		Find(&inputBindings).Error; err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return graphengine.RuntimeSnapshot{}, err
	}
	var decisions []orm.WorkflowRouteDecision
	if err := db.Where("session_id = ?", session.ID).Order("created_at ASC").Find(&decisions).Error; err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	snapshot := graphengine.RuntimeSnapshot{}
	controlled := session.ControllerHost == "external-agent" && session.ControlProtocol == controlpolicy.Protocol
	acceptedRevisions := map[string]bool{}
	if controlled {
		var reviews []orm.WorkflowReviewCheckpoint
		if err := db.Where("session_id = ? AND status = ?", session.ID, "accepted").Find(&reviews).Error; err != nil {
			return graphengine.RuntimeSnapshot{}, err
		}
		for _, review := range reviews {
			var manifest struct {
				Items []struct {
					RevisionID string `json:"revision_id"`
				} `json:"items"`
			}
			if err := json.Unmarshal([]byte(review.ManifestJSON), &manifest); err != nil {
				return graphengine.RuntimeSnapshot{}, err
			}
			for _, item := range manifest.Items {
				acceptedRevisions[item.RevisionID] = true
			}
		}
	}
	for _, row := range attempts {
		validity := row.Validity
		if validity == "" {
			validity = "effective"
		}
		snapshot.Attempts = append(snapshot.Attempts, graphengine.AttemptFact{StepID: row.StepID, Status: row.Status, Validity: validity})
	}
	for _, row := range revisions {
		validity := row.Validity
		if validity == "" {
			validity = "effective"
		}
		valid := validity == "effective"
		if controlled && valid {
			valid = false
			for _, producer := range attempts {
				if producer.ID == row.ProducerAttemptID || (producer.StepID == row.StepID && producer.Attempt == row.Attempt) {
					valid = producer.Status == "succeeded" && producer.Validity == "effective" && (!producer.ReviewRequired || acceptedRevisions[row.ID])
					break
				}
			}
		}
		snapshot.Materials = append(snapshot.Materials, graphengine.MaterialValue{MaterialID: row.SlotID, RevisionID: row.ID, Valid: valid})
	}
	for _, row := range inputBindings {
		// Startup answers may share a name with a normalized step output.
		// They remain session inputs, but cannot witness a material whose
		// pinned contract declares a step producer, even before it has run.
		if graph.MaterialProducers[row.MaterialID].Kind == "step" {
			continue
		}
		snapshot.Materials = append(snapshot.Materials, graphengine.MaterialValue{
			MaterialID: row.MaterialID, RevisionID: row.ID, Valid: true,
		})
	}
	for _, row := range decisions {
		var active, pruned, bypassed []string
		_ = json.Unmarshal(row.ActivatedJSON, &active)
		_ = json.Unmarshal(row.PrunedJSON, &pruned)
		_ = json.Unmarshal(row.BypassedJSON, &bypassed)
		snapshot.Routes = append(snapshot.Routes, graphengine.RouteFact{From: row.FromStepID, Activated: active, Pruned: pruned, Bypassed: bypassed, Validity: row.Validity})
	}
	if err := publication.ProjectInputs(db, session, &snapshot); err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	return snapshot, nil
}
