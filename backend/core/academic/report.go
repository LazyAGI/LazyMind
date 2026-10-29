package academic

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
	"time"
)

// Reports freeze producer-matched artifacts and server observations, separate
// from ReviewWorkflowInput and from the mutable progress projection.
func expansionReportSnapshot(db *gorm.DB, record orm.AcademicExpansion, session, attempt string, artifacts []workflowstore.Artifact) (json.RawMessage, error) {
	var preparation expansionPreparation
	_ = json.Unmarshal(record.PreparationJSON, &preparation)
	report := map[string]any{}
	metrics := map[string]any{}
	for _, artifact := range artifacts {
		if artifact.Validity != "effective" || artifact.Deleted || !artifact.Selected {
			continue
		}
		raw, err := academicArtifactJSON(artifact.Value)
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		if artifact.Slot == "expansion_report" && (artifact.ProducerAttemptID == "" || artifact.ProducerAttemptID == attempt) {
			report = value
		}
		if artifact.Slot == "expansion_plan" || artifact.Slot == "candidate_set" || artifact.Slot == "relation_analysis" {
			if preparation.ExecutionStartedAt == nil || !artifact.CreatedAt.Before(*preparation.ExecutionStartedAt) {
				metrics[artifact.Slot] = value["metrics"]
			}
		}
	}
	var searches []paperDiscovery
	if err := json.Unmarshal(record.DiscoveryJSON, &searches); err != nil {
		return nil, err
	}
	calls := []paperDiscovery{}
	reused := []paperDiscovery{}
	for _, search := range searches {
		if search.SessionID == session || search.SessionID == "" {
			if preparation.ExecutionStartedAt != nil && search.CreatedAt.Before(*preparation.ExecutionStartedAt) {
				reused = append(reused, search)
			} else {
				calls = append(calls, search)
			}
		}
	}
	var steps []orm.WorkflowSessionStep
	if err := db.Where("session_id=?", session).Order("created_at,id").Find(&steps).Error; err != nil {
		return nil, err
	}
	timing := []map[string]any{}
	for _, step := range steps {
		if preparation.ExecutionStartedAt != nil && step.CreatedAt.Before(*preparation.ExecutionStartedAt) {
			continue
		}
		timing = append(timing, map[string]any{"step_id": step.StepID, "attempt": step.Attempt, "status": step.Status, "queue_and_execution_ms": step.UpdatedAt.Sub(step.CreatedAt).Milliseconds()})
	}
	report["source_queries"] = calls
	report["reused_source_queries"] = reused
	report["stage_metrics"] = metrics
	report["stage_timing"] = timing
	report["currency_cost"] = nil
	report["preparation_attempts"] = preparation.Attempts
	if preparation.ExecutionStartedAt != nil {
		report["execution_elapsed_ms"] = time.Since(*preparation.ExecutionStartedAt).Milliseconds()
	}
	// Freeze quality and versions with this round, without duplicating page text.
	ids := []string{}
	for _, artifact := range artifacts {
		if artifact.Slot != "expansion_result" || artifact.Validity != "effective" || !artifact.Selected || artifact.Deleted {
			continue
		}
		raw, err := academicArtifactJSON(artifact.Value)
		if err != nil {
			return nil, err
		}
		var input ReviewWorkflowInput
		if json.Unmarshal(raw, &input) == nil {
			for _, entry := range input.Papers {
				ids = append(ids, entry.Paper.PaperID)
			}
		}
	}
	materials := []map[string]any{}
	for _, id := range ids {
		source, err := expansionSource(db, record, id)
		if err != nil {
			return nil, err
		}
		row, err := findSourceContent(db, record.UserID, source)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		materials = append(materials, map[string]any{"paper_id": row.PaperID, "status": row.Status, "quality": json.RawMessage(row.QualityJSON), "content_hash": row.ContentHash, "pdf_hash": row.SourceHash, "source_version": row.SourceVersion, "source_provider": row.SourceProvider, "parser_version": row.ParserVersion})
	}
	report["materials"] = materials
	return marshal(report), nil
}
