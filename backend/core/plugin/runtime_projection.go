package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/plugin/graphengine"
	"lazymind/core/store"
)

const pluginDefinitionChangedCode = "PLUGIN_DEFINITION_CHANGED"

type pluginDefinitionChangedError struct {
	expected string
	actual   string
}

func (e *pluginDefinitionChangedError) Error() string {
	return "插件代码在此任务启动后发生了变化，当前任务不能继续；请新建一个对话任务以使用更新后的插件"
}

func ensureLegacySessionGraphUnchanged(session *orm.PluginSession, graph *graphengine.CompiledStateGraph) error {
	if session.GraphHash != "" && graph.GraphHash != "" && session.GraphHash != graph.GraphHash {
		return &pluginDefinitionChangedError{expected: session.GraphHash, actual: graph.GraphHash}
	}
	return nil
}

func loadSessionGraph(ctx context.Context, db *gorm.DB, session *orm.PluginSession) (*graphengine.CompiledStateGraph, error) {
	if session.PluginRevisionID != "" {
		var revision orm.PluginRevision
		if err := db.WithContext(ctx).Where("id = ?", session.PluginRevisionID).First(&revision).Error; err != nil {
			return nil, fmt.Errorf("load plugin revision %s: %w", session.PluginRevisionID, err)
		}
		if len(revision.CompiledGraph) == 0 {
			return nil, fmt.Errorf("plugin revision %s has no compiled graph", session.PluginRevisionID)
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
	// Compatibility path for built-ins and pre-v2 revisions. It is read-only;
	// new publishes are required to persist a strict compiled graph.
	upstream := common.JoinURL(common.ChatServiceEndpoint(), "/api/plugins/"+session.PluginID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("load legacy plugin spec: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("load legacy plugin spec: status %d", resp.StatusCode)
	}
	var body struct {
		PluginYAML string `json:"plugin_yaml_raw"`
		StateYAML  string `json:"state_yaml_raw"`
		Scenario   string `json:"scenario_raw"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	compiled := graphengine.Compile(body.PluginYAML, body.StateYAML, body.Scenario, graphengine.ProfileRuntimeLoad)
	if !compiled.Valid || compiled.Graph == nil {
		return nil, fmt.Errorf("legacy plugin cannot be compiled: %v", compiled.Diagnostics)
	}
	if err := ensureLegacySessionGraphUnchanged(session, compiled.Graph); err != nil {
		return nil, err
	}
	return compiled.Graph, nil
}

func loadRuntimeSnapshot(ctx context.Context, db *gorm.DB, sessionID string) (graphengine.RuntimeSnapshot, error) {
	var attempts []orm.PluginSessionStep
	if err := db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at ASC").Find(&attempts).Error; err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	var revisions []orm.PluginSlotRevision
	if err := db.WithContext(ctx).Where("session_id = ? AND selected = ?", sessionID, true).Find(&revisions).Error; err != nil {
		return graphengine.RuntimeSnapshot{}, err
	}
	var decisions []orm.PluginRouteDecision
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

func projectSession(ctx context.Context, db *gorm.DB, session *orm.PluginSession) (projectionResponse, error) {
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
	var attempts []orm.PluginSessionStep
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
			if err := db.WithContext(ctx).Model(&orm.SubAgentArtifact{}).Where("task_id = ?", attempt.TaskID).Count(&artifactCount).Error; err != nil {
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
		var bindings []orm.PluginAttemptInputBinding
		if err := db.WithContext(ctx).Where("attempt_id = ?", attempt.ID).Order("created_at ASC").Find(&bindings).Error; err != nil {
			return projectionResponse{}, err
		}
		for _, binding := range bindings {
			inputWitnesses[attempt.ID] = append(inputWitnesses[attempt.ID], graphengine.Witness{MaterialID: binding.MaterialID, RevisionID: binding.MaterialRevisionID, BindAs: binding.BindAs})
		}
	}
	return projectionResponse{
		SessionID: session.ID, StateVersion: session.StateVersion, GraphHash: graph.GraphHash, SchemaVersion: graph.SchemaVersion,
		Projection: graphengine.Project(graph, snapshot), Graph: graph, AttemptHistory: attemptHistory, InputWitnesses: inputWitnesses,
	}, nil
}

func GetSessionProjection(w http.ResponseWriter, r *http.Request) {
	var session orm.PluginSession
	if err := store.DB().Where("id = ? AND dismissed = false", common.PathVar(r, "session_id")).First(&session).Error; err != nil {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}
	projection, err := projectSession(r.Context(), store.DB(), &session)
	if err != nil {
		var changed *pluginDefinitionChangedError
		if errors.As(err, &changed) {
			common.ReplyErrWithData(w, changed.Error(), map[string]any{
				"code":    pluginDefinitionChangedCode,
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
	return db.WithContext(ctx).Create(&orm.PluginRouteDecision{ID: "prd_" + common.GenerateID(), SessionID: sessionID, FromStepID: from, SourceAttemptID: attemptID, ActivatedJSON: a, PrunedJSON: p, BypassedJSON: b, WitnessJSON: wi, Validity: "effective", StateVersion: stateVersion, CreatedAt: time.Now().UTC()}).Error
}

func freezeRouteDecision(ctx context.Context, db *gorm.DB, sessionID, from, attemptID string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session orm.PluginSession
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
		if err := tx.Model(&orm.PluginRouteDecision{}).Where("session_id = ? AND from_step_id = ? AND validity = ?", sessionID, from, "effective").Update("validity", "stale").Error; err != nil {
			return err
		}
		if err := persistRouteDecision(ctx, tx, sessionID, from, attemptID, decision.Activated, decision.Pruned, decision.Bypassed, decision.Witnesses, session.StateVersion); err != nil {
			return err
		}
		return reconcileSessionProjection(ctx, tx, &session)
	})
}

// reconcileSessionProjection derives terminal state from the same projection
// used for admission. Reaching one end edge is insufficient while another
// effective branch remains current, ready, or blocked.
func reconcileSessionProjection(ctx context.Context, db *gorm.DB, session *orm.PluginSession) error {
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
	return db.WithContext(ctx).Model(&orm.PluginSession{}).Where("id = ?", session.ID).Updates(updates).Error
}
