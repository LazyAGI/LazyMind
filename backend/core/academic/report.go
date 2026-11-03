package academic

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
	"reflect"
	"time"
)

type expansionCompletion struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type expansionResearchState struct {
	Completion       *expansionCompletion `json:"completion,omitempty"`
	ResearchComplete *bool                `json:"research_complete,omitempty"`
	CoverageGaps     json.RawMessage      `json:"coverage_gaps,omitempty"`
}

func readExpansionResearchState(raw json.RawMessage) (expansionResearchState, error) {
	var state expansionResearchState
	if len(raw) == 0 {
		return state, nil
	}
	err := json.Unmarshal(raw, &state)
	return state, err
}

func validateResearchCompletion(value map[string]any) error {
	completion, ok := value["completion"].(map[string]any)
	if !ok {
		return errors.New("research analysis requires its research completion outcome")
	}
	complete, ok := value["research_complete"].(bool)
	if !ok {
		return errors.New("research analysis requires research_complete")
	}
	gaps, ok := value["coverage_gaps"].([]any)
	if !ok {
		return errors.New("research analysis requires its coverage gaps")
	}
	if complete {
		if completion["status"] != "complete" || completion["reason"] != "scope_covered" || len(gaps) != 0 {
			return errors.New("complete research must have a scope-covered outcome without unresolved coverage gaps")
		}
	} else if completion["status"] != "incomplete" || (completion["reason"] != "budget_exhausted" && completion["reason"] != "material_limited") {
		return errors.New("incomplete research requires its actual stopping reason")
	}
	return nil
}

// Reports freeze producer-matched artifacts and server observations, separate
// from ReviewWorkflowInput and from the mutable progress projection.
func expansionReportSnapshot(db *gorm.DB, record orm.AcademicExpansion, session, attempt string, artifacts []workflowstore.Artifact) (json.RawMessage, error) {
	var preparation expansionPreparation
	_ = json.Unmarshal(record.PreparationJSON, &preparation)
	report := map[string]any{}
	metrics := map[string]any{}
	analysisVersion := ""
	var analysis map[string]any
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
		if (artifact.Slot == "candidate_set" || artifact.Slot == "screened_candidates") && artifact.ProducerAttemptID == attempt && value["list_mode"] != nil {
			report["list_mode"] = value["list_mode"]
			report["recommendations"] = value["recommendations"]
		}
		if artifact.Slot == "candidate_set" || artifact.Slot == "screened_candidates" || artifact.Slot == "relation_analysis" {
			if artifact.Slot == "relation_analysis" {
				analysisVersion, _ = value["analysis_version"].(string)
				analysis = value
			}
			if preparation.ExecutionStartedAt == nil || !artifact.CreatedAt.Before(*preparation.ExecutionStartedAt) {
				metrics[artifact.Slot] = value["metrics"]
			}
		}
	}
	if version, _ := report["analysis_version"].(string); version != "" || analysisVersion != "" {
		if version == "" || version != analysisVersion {
			return nil, errors.New("expansion report does not match the effective relation analysis version")
		}
		if err := validateResearchCompletion(analysis); err != nil {
			return nil, err
		}
		for _, field := range []string{"completion", "research_complete", "coverage_gaps", "selection", "input", "relations", "claims", "research_structure", "source_versions"} {
			if !reflect.DeepEqual(report[field], analysis[field]) {
				return nil, errors.New("expansion report must preserve the research analysis " + field)
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
		if step.ID == attempt && !step.UpdatedAt.IsZero() {
			report["result_projection_delay_ms"] = max(0, time.Since(step.UpdatedAt).Milliseconds())
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
	if value, present := report["source_versions"]; present {
		var sources []struct {
			PaperID       string `json:"paper_id"`
			Status        string `json:"status"`
			ContentHash   string `json:"content_hash"`
			SourceVersion string `json:"source_version"`
		}
		if err := json.Unmarshal(marshal(value), &sources); err != nil {
			return nil, err
		}
		byID := map[string]map[string]any{}
		for _, material := range materials {
			byID[material["paper_id"].(string)] = material
		}
		for _, source := range sources {
			current, exists := byID[source.PaperID]
			if !exists && source.Status == "unavailable" {
				continue
			}
			if !exists || current["status"] != source.Status || current["content_hash"] != source.ContentHash || current["source_version"] != source.SourceVersion {
				return nil, errors.New("paper content changed after research analysis; report publication requires renewed analysis")
			}
		}
	}
	return marshal(report), nil
}
