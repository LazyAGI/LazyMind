package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/plugin/graphengine"
	"lazymind/core/store"
)

func loadSessionGraph(ctx context.Context, db *gorm.DB, session *orm.PluginSession) (*graphengine.CompiledStateGraph, error) {
	if session.PluginRevisionID != "" {
		var revision orm.PluginRevision
		if err := db.WithContext(ctx).Where("id = ?", session.PluginRevisionID).First(&revision).Error; err == nil && len(revision.CompiledGraph) > 0 {
			var graph graphengine.CompiledStateGraph
			if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
				return nil, fmt.Errorf("decode compiled graph: %w", err)
			}
			return &graph, nil
		}
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
	compiled := graphengine.Compile(body.PluginYAML, body.StateYAML, body.Scenario, graphengine.ProfileEditor)
	if compiled.Graph == nil {
		return nil, fmt.Errorf("legacy plugin cannot be compiled: %v", compiled.Diagnostics)
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
	SessionID     string                          `json:"session_id"`
	StateVersion  int64                           `json:"state_version"`
	GraphHash     string                          `json:"graph_hash"`
	SchemaVersion string                          `json:"schema_version"`
	Projection    graphengine.Projection          `json:"projection"`
	Graph         *graphengine.CompiledStateGraph `json:"graph"`
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
	return projectionResponse{SessionID: session.ID, StateVersion: session.StateVersion, GraphHash: graph.GraphHash, SchemaVersion: graph.SchemaVersion, Projection: graphengine.Project(graph, snapshot), Graph: graph}, nil
}

func GetSessionProjection(w http.ResponseWriter, r *http.Request) {
	var session orm.PluginSession
	if err := store.DB().Where("id = ? AND dismissed = false", common.PathVar(r, "session_id")).First(&session).Error; err != nil {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}
	projection, err := projectSession(r.Context(), store.DB(), &session)
	if err != nil {
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
	var session orm.PluginSession
	if err := db.WithContext(ctx).Where("id = ?", sessionID).First(&session).Error; err != nil {
		return err
	}
	graph, err := loadSessionGraph(ctx, db, &session)
	if err != nil {
		return err
	}
	snapshot, err := loadRuntimeSnapshot(ctx, db, sessionID)
	if err != nil {
		return err
	}
	decision := graphengine.DecideRoute(graph, from, snapshot.Materials)
	if err := db.WithContext(ctx).Model(&orm.PluginRouteDecision{}).Where("session_id = ? AND from_step_id = ? AND validity = ?", sessionID, from, "effective").Update("validity", "stale").Error; err != nil {
		return err
	}
	if err := persistRouteDecision(ctx, db, sessionID, from, attemptID, decision.Activated, decision.Pruned, decision.Bypassed, decision.Witnesses, session.StateVersion); err != nil {
		return err
	}
	return reconcileSessionProjection(ctx, db, &session)
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
