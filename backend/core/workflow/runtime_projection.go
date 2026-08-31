package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
)

const workflowDefinitionChangedCode = "WORKFLOW_DEFINITION_CHANGED"

type workflowDefinitionChangedError struct {
	expected string
	actual   string
}

func (e *workflowDefinitionChangedError) Error() string {
	return "插件代码在此任务启动后发生了变化，当前任务不能继续；请新建一个对话任务以使用更新后的插件"
}

func ensureLegacySessionGraphUnchanged(session *orm.WorkflowSession, graph *graphengine.CompiledStateGraph) error {
	if session.GraphHash != "" && graph.GraphHash != "" && session.GraphHash != graph.GraphHash {
		return &workflowDefinitionChangedError{expected: session.GraphHash, actual: graph.GraphHash}
	}
	return nil
}

func loadSessionGraph(ctx context.Context, db *gorm.DB, session *orm.WorkflowSession) (*graphengine.CompiledStateGraph, error) {
	if session.WorkflowRevisionID == "" {
		var resource orm.WorkflowResource
		if err := db.WithContext(ctx).Where("status = 'active' AND (plugin_id = ? OR plugin_ref = ?)",
			session.WorkflowID, session.WorkflowID).First(&resource).Error; err != nil {
			return nil, fmt.Errorf("workflow session has no pinned public revision: %w", err)
		}
		if resource.HeadRevisionID == "" {
			return nil, fmt.Errorf("workflow session has no pinned public revision")
		}
		var revision orm.WorkflowRevision
		if err := db.WithContext(ctx).Where("id = ? AND plugin_resource_id = ?", resource.HeadRevisionID, resource.ID).
			First(&revision).Error; err != nil {
			return nil, fmt.Errorf("load workflow head revision %s: %w", resource.HeadRevisionID, err)
		}
		graph, err := decodeSessionRevisionGraph(session, &revision)
		if err != nil {
			return nil, err
		}
		if err := ensureLegacySessionGraphUnchanged(session, graph); err != nil {
			return nil, err
		}
		result := db.WithContext(ctx).Model(&orm.WorkflowSession{}).
			Where("id = ? AND (plugin_revision_id IS NULL OR plugin_revision_id = '')", session.ID). // workflow-naming: persistence
			Updates(map[string]any{"plugin_revision_id": revision.ID, "graph_hash": graph.GraphHash, // workflow-naming: persistence
				"graph_schema_version": graph.SchemaVersion})
		if result.Error != nil {
			return nil, fmt.Errorf("pin legacy workflow session revision: %w", result.Error)
		}
		session.WorkflowRevisionID = revision.ID
		session.GraphHash = graph.GraphHash
		session.GraphSchemaVersion = graph.SchemaVersion
		return graph, nil
	}
	if session.WorkflowRevisionID != "" {
		var revision orm.WorkflowRevision
		if err := db.WithContext(ctx).Where("id = ?", session.WorkflowRevisionID).First(&revision).Error; err != nil {
			return nil, fmt.Errorf("load plugin revision %s: %w", session.WorkflowRevisionID, err)
		}
		return decodeSessionRevisionGraph(session, &revision)
	}
	return nil, fmt.Errorf("workflow session has no pinned public revision")
}

func decodeSessionRevisionGraph(session *orm.WorkflowSession, revision *orm.WorkflowRevision) (*graphengine.CompiledStateGraph, error) {
	if len(revision.CompiledGraph) == 0 {
		return nil, fmt.Errorf("plugin revision %s has no compiled graph", revision.ID)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		return nil, fmt.Errorf("decode compiled graph: %w", err)
	}
	if graph.SchemaVersion != graphengine.SchemaVersion || revision.GraphSchemaVersion != graphengine.SchemaVersion {
		return nil, fmt.Errorf("unsupported compiled graph schema: graph=%q revision=%q supported=%q", graph.SchemaVersion, revision.GraphSchemaVersion, graphengine.SchemaVersion)
	}
	if graph.GraphHash == "" || revision.GraphHash == "" || graph.GraphHash != revision.GraphHash {
		return nil, fmt.Errorf("compiled graph hash does not match revision metadata")
	}
	if session.GraphSchemaVersion != "" && session.GraphSchemaVersion != graph.SchemaVersion {
		return nil, fmt.Errorf("session graph schema mismatch: session=%q revision=%q", session.GraphSchemaVersion, graph.SchemaVersion)
	}
	if session.GraphHash != "" && session.GraphHash != graph.GraphHash {
		return nil, fmt.Errorf("session graph hash mismatch: session=%q revision=%q", session.GraphHash, graph.GraphHash)
	}
	return &graph, nil
}

func loadRuntimeSnapshot(ctx context.Context, db *gorm.DB, sessionID string) (graphengine.RuntimeSnapshot, error) {
	var attempts []orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at ASC").Find(&attempts).Error; err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).Where("session_id = ? AND selected = ?", sessionID, true).Find(&revisions).Error; err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	var inputBindings []orm.WorkflowInputBinding
	if err := db.WithContext(ctx).Where("workflow_session_id = ? AND validity = 'effective'", sessionID).
		Find(&inputBindings).Error; err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return graphengine.RuntimeSnapshot{}, err
	}
	var decisions []orm.WorkflowRouteDecision
	if err := db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at ASC").Find(&decisions).Error; err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	snapshot := graphengine.RuntimeSnapshot{}
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
		snapshot.Materials = append(snapshot.Materials, graphengine.MaterialValue{MaterialID: row.SlotID, RevisionID: row.ID, Valid: validity == "effective"})
	}
	for _, row := range inputBindings {
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
	return snapshot, nil
}

type projectionResponse struct {
	SessionID      string                           `json:"session_id"`
	StateVersion   int64                            `json:"state_version"`
	GraphHash      string                           `json:"graph_hash"`
	SchemaVersion  string                           `json:"schema_version"`
	Projection     graphengine.Projection           `json:"projection"`
	Graph          *graphengine.CompiledStateGraph  `json:"graph"`
	AttemptHistory map[string][]attemptHistoryDTO   `json:"attempt_history"`
	InputWitnesses map[string][]graphengine.Witness `json:"input_witnesses"`
}

type attemptHistoryDTO struct {
	Attempt       int     `json:"attempt"`
	TaskID        string  `json:"task_id"`
	Status        string  `json:"status"`
	Validity      string  `json:"validity"`
	DurationSec   float64 `json:"duration_sec"`
	ArtifactCount int64   `json:"artifact_count"`
	StartedAt     string  `json:"started_at"`
}

func projectSession(ctx context.Context, db *gorm.DB, session *orm.WorkflowSession) (projectionResponse, error) {
	graph, err := loadSessionGraph(ctx, db, session)
	if err != nil {
		return projectionResponse{}, err
	}
	snapshot, err := loadRuntimeSnapshot(ctx, db, session.ID)
	if err != nil {
		return projectionResponse{}, err
	}
	attemptHistory := map[string][]attemptHistoryDTO{}
	inputWitnesses := map[string][]graphengine.Witness{}
	var attempts []orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("session_id = ?", session.ID).Order("created_at ASC").Find(&attempts).Error; err != nil {
		return projectionResponse{}, err
	}
	for _, attempt := range attempts {
		validity := attempt.Validity
		if validity == "" {
			validity = "effective"
		}
		var artifactCount int64
		if attempt.TaskID != "" {
			if err := db.WithContext(ctx).Model(&orm.SubAgentArtifact{}).
				Where("task_id = ?", attempt.TaskID).Count(&artifactCount).Error; err != nil {
				return projectionResponse{}, err
			}
		}
		if artifactCount == 0 {
			if err := db.WithContext(ctx).Model(&orm.WorkflowSlotRevision{}).
				Where("producer_attempt_id = ?", attempt.ID).Count(&artifactCount).Error; err != nil {
				return projectionResponse{}, err
			}
		}
		duration := attempt.UpdatedAt.Sub(attempt.CreatedAt).Seconds()
		if duration < 0 {
			duration = 0
		}
		attemptHistory[attempt.StepID] = append(attemptHistory[attempt.StepID], attemptHistoryDTO{
			Attempt: attempt.Attempt, TaskID: attempt.TaskID, Status: attempt.Status, Validity: validity,
			DurationSec: duration, ArtifactCount: artifactCount, StartedAt: attempt.CreatedAt.UTC().Format(time.RFC3339),
		})
		var bindings []orm.WorkflowAttemptInputBinding
		if err := db.WithContext(ctx).Where("attempt_id = ?", attempt.ID).Order("created_at ASC").Find(&bindings).Error; err != nil {
			return projectionResponse{}, err
		}
		for _, binding := range bindings {
			inputWitnesses[attempt.ID] = append(inputWitnesses[attempt.ID], graphengine.Witness{MaterialID: binding.MaterialID, RevisionID: binding.MaterialRevisionID, BindAs: binding.BindAs})
		}
	}
	projection := graphengine.Project(graph, snapshot)
	return projectionResponse{
		SessionID: session.ID, StateVersion: session.StateVersion, GraphHash: graph.GraphHash, SchemaVersion: graph.SchemaVersion,
		Projection: projection, Graph: graph, AttemptHistory: attemptHistory, InputWitnesses: inputWitnesses,
	}, nil
}

func removeStepID(values []string, target string) []string {
	filtered := values[:0]
	for _, value := range values {
		if value != target {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func GetSessionProjection(w http.ResponseWriter, r *http.Request) {
	var session orm.WorkflowSession
	if err := store.DB().Where("id = ? AND dismissed = false", common.PathVar(r, "session_id")).First(&session).Error; err != nil {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}
	projection, err := projectSession(r.Context(), store.DB(), &session)
	if err != nil {
		var changed *workflowDefinitionChangedError
		if errors.As(err, &changed) {
			common.ReplyErrWithData(w, changed.Error(), map[string]any{
				"code":    workflowDefinitionChangedCode,
				"details": map[string]any{"expected": changed.expected, "actual": changed.actual},
			}, http.StatusConflict)
			return
		}
		common.ReplyErr(w, "project session failed: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	common.ReplyOK(w, projection)
}

func persistRouteDecision(ctx context.Context, db *gorm.DB, sessionID, from, attemptID string, active, pruned, bypassed []string, witnesses []graphengine.Witness, stateVersion int64) error {
	a, _ := json.Marshal(active)
	p, _ := json.Marshal(pruned)
	b, _ := json.Marshal(bypassed)
	wi, _ := json.Marshal(witnesses)
	return db.WithContext(ctx).Create(&orm.WorkflowRouteDecision{ID: "prd_" + common.GenerateID(), SessionID: sessionID, FromStepID: from, SourceAttemptID: attemptID, ActivatedJSON: a, PrunedJSON: p, BypassedJSON: b, WitnessJSON: wi, Validity: "effective", StateVersion: stateVersion, CreatedAt: time.Now().UTC()}).Error
}

func freezeRouteDecision(ctx context.Context, db *gorm.DB, sessionID, from, taskID string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session orm.WorkflowSession
		if err := tx.Where("id = ?", sessionID).First(&session).Error; err != nil {
			return err
		}
		graph, err := loadSessionGraph(ctx, tx, &session)
		if err != nil {
			return err
		}
		snapshot, err := loadRuntimeSnapshot(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		decision := graphengine.DecideRoute(graph, from, snapshot.Materials)
		var attempt orm.WorkflowSessionStep
		if err := tx.Select("id", "result_json").Where(
			"session_id = ? AND step_id = ? AND task_id = ?", sessionID, from, taskID,
		).First(&attempt).Error; err != nil {
			return err
		}
		var result executor.Result
		if raw := strings.TrimSpace(attempt.ResultJSON); raw != "" && raw != "{}" {
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				return fmt.Errorf("decode attempt result: %w", err)
			}
		}
		if result.Control != nil && strings.TrimSpace(result.Control.NextStep) != "" {
			target := strings.TrimSpace(result.Control.NextStep)
			selected := graphengine.SelectRouteTarget(graph, from, target, decision)
			if len(selected.Activated) != 1 || selected.Activated[0] != target {
				return fmt.Errorf("control.next_step %q is not a reachable choice from %q", target, from)
			}
			decision = selected
		}
		if err := tx.Model(&orm.WorkflowRouteDecision{}).Where("session_id = ? AND from_step_id = ? AND validity = ?", sessionID, from, "effective").Update("validity", "stale").Error; err != nil {
			return err
		}
		if err := persistRouteDecision(ctx, tx, sessionID, from, attempt.ID, decision.Activated, decision.Pruned, decision.Bypassed, decision.Witnesses, session.StateVersion); err != nil {
			return err
		}
		return reconcileSessionProjection(ctx, tx, &session)
	})
}

// FinalizeHostAttempt applies the same terminal projection semantics used by
// LazyMind's managed ChatAgent after a Host-neutral Attempt reaches terminal
// state. Host transport and product names never enter the graph algorithm.
func FinalizeHostAttempt(ctx context.Context, db *gorm.DB, sessionID, stepID, attemptID, status string) error {
	switch status {
	case "succeeded":
		var existing int64
		if err := db.WithContext(ctx).Model(&orm.WorkflowRouteDecision{}).
			Where("session_id = ? AND from_step_id = ? AND source_attempt_id = ? AND validity = ?",
				sessionID, stepID, attemptID, "effective").Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}
		return freezeRouteDecision(ctx, db, sessionID, stepID, attemptID)
	case "failed":
		return UpdateSessionStatus(ctx, db, sessionID, SessionStatusFailed)
	case "cancelled", "interrupted":
		return UpdateSessionStatus(ctx, db, sessionID, SessionStatusWaiting)
	default:
		return gorm.ErrInvalidData
	}
}

// reconcileSessionProjection derives terminal state from the same projection
// used for admission. Reaching one end edge is insufficient while another
// effective branch remains current, ready, or blocked.
func reconcileSessionProjection(ctx context.Context, db *gorm.DB, session *orm.WorkflowSession) error {
	projected, err := projectSession(ctx, db, session)
	if err != nil {
		return err
	}
	status := SessionStatusWaiting
	if projected.Projection.Completed {
		status = SessionStatusCompleted
	} else if len(projected.Projection.Current) > 0 {
		status = SessionStatusActive
	}
	updates := map[string]any{
		"status":        status,
		"state_version": gorm.Expr("state_version + 1"),
		"updated_at":    time.Now().UTC(),
	}
	return db.WithContext(ctx).Model(&orm.WorkflowSession{}).Where("id = ?", session.ID).Updates(updates).Error
}
