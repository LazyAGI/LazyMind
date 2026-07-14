package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/plugin/graphengine"
	"lazymind/core/store"
)

type transitionCommandRequest struct {
	CommandID            string              `json:"command_id"`
	Operation            string              `json:"operation"`
	TargetStepID         string              `json:"target_step_id"`
	ExpectedStateVersion int64               `json:"expected_state_version"`
	GraphHash            string              `json:"graph_hash"`
	TaskID               string              `json:"task_id"`
	Objective            string              `json:"objective"`
	UserInput            string              `json:"user_input"`
	RuntimeInstruction   string              `json:"runtime_instruction"`
	PartialIndices       map[string][]int    `json:"partial_indices"`
	HandOff              bool                `json:"hand_off"`
	PluginMode           string              `json:"plugin_mode"`
	ChatSessionID        string              `json:"chat_session_id"`
	HistoryFilesPerTurn  map[string][]string `json:"history_files_per_turn"`
	Filters              map[string]any      `json:"filters"`
	LLMConfig            map[string]any      `json:"llm_config"`
	ToolConfig           map[string]any      `json:"tool_config"`
	PluginID             string              `json:"plugin_id"`
	PluginRef            string              `json:"plugin_ref"`
	PluginRevisionID     string              `json:"plugin_revision_id"`
	PluginRevisionNo     int64               `json:"plugin_revision_no"`
	PluginTreeHash       string              `json:"plugin_tree_hash"`
	PluginRemoteRoot     string              `json:"plugin_remote_root"`
	ConversationID       string              `json:"conversation_id"`
	TriggerHistoryID     string              `json:"trigger_history_id"`
	UserID               string              `json:"user_id"`
	PreflightID          string              `json:"preflight_id"`
	ExternalMaterials    map[string]any      `json:"external_materials"`
}

// PlanPluginSessionStart returns the same authoritative projection used by
// StartPluginSession without creating a session or attempt. Python uses this
// to present only genuinely Ready entry steps to the model.
func PlanPluginSessionStart(w http.ResponseWriter, r *http.Request) {
	var req transitionCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid start plan", http.StatusBadRequest)
		return
	}
	if req.PluginID == "" {
		common.ReplyErr(w, "plugin_id is required", http.StatusUnprocessableEntity)
		return
	}
	probe := &orm.PluginSession{PluginID: req.PluginID, PluginRevisionID: req.PluginRevisionID}
	graph, err := loadSessionGraph(r.Context(), store.DB(), probe)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	materials := externalMaterialFacts(graph, req.ExternalMaterials)
	common.ReplyOK(w, map[string]any{
		"graph_hash":     graph.GraphHash,
		"schema_version": graph.SchemaVersion,
		"projection":     graphengine.Project(graph, graphengine.RuntimeSnapshot{Materials: materials}),
	})
}

func externalMaterialFacts(graph *graphengine.CompiledStateGraph, supplied map[string]any) []graphengine.MaterialValue {
	materials := make([]graphengine.MaterialValue, 0)
	for materialID, producer := range graph.MaterialProducers {
		if _, ok := supplied[materialID]; producer.Kind == "external" && ok {
			materials = append(materials, graphengine.MaterialValue{MaterialID: materialID, RevisionID: "external:" + materialID, Valid: true})
		}
	}
	return materials
}

// StartPluginSession validates the first target with the same graph projector,
// then creates the session and task synchronously.
func StartPluginSession(w http.ResponseWriter, r *http.Request) {
	var req transitionCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid start command", http.StatusBadRequest)
		return
	}
	if req.CommandID == "" {
		req.CommandID = uuid.NewString()
	}
	req.Operation = "start"
	if existing, ok := loadExistingTransition(store.DB(), req.CommandID); ok {
		status := http.StatusOK
		if !existing.Accepted {
			status = http.StatusConflict
		}
		writeTransitionResponse(w, *existing, status)
		return
	}
	if req.PluginID == "" || req.TargetStepID == "" || req.ConversationID == "" {
		response := transitionCommandResponse{Accepted: false, CommandID: req.CommandID, Error: &transitionError{Code: "INVALID_TARGET", Message: "plugin_id, conversation_id, and target_step_id are required"}}
		_ = persistTransitionCommand(store.DB(), req, response, "rejected")
		writeTransitionResponse(w, response, http.StatusUnprocessableEntity)
		return
	}
	reserved, reserveErr := reserveTransitionCommand(store.DB(), req)
	if reserveErr != nil {
		common.ReplyErr(w, "reserve transition command failed", http.StatusServiceUnavailable)
		return
	}
	if !reserved {
		if existing, ok := loadExistingTransition(store.DB(), req.CommandID); ok {
			writeTransitionResponse(w, *existing, http.StatusConflict)
			return
		}
	}
	probe := &orm.PluginSession{PluginID: req.PluginID, PluginRevisionID: req.PluginRevisionID}
	graph, err := loadSessionGraph(r.Context(), store.DB(), probe)
	if err != nil {
		response := transitionCommandResponse{Accepted: false, CommandID: req.CommandID, Error: &transitionError{Code: "GRAPH_REVISION_MISMATCH", Message: err.Error()}}
		_ = persistTransitionCommand(store.DB(), req, response, "rejected")
		writeTransitionResponse(w, response, http.StatusUnprocessableEntity)
		return
	}
	externalMaterials := externalMaterialFacts(graph, req.ExternalMaterials)
	projection := graphengine.Project(graph, graphengine.RuntimeSnapshot{Materials: externalMaterials})
	node, exists := projection.Nodes[req.TargetStepID]
	if !exists || node.Reachability != "reachable" || node.Readiness != "ready" {
		code := "STEP_NOT_REACHABLE"
		message := "first step is not reachable from __start__"
		details := map[string]any{"ready": projection.Ready, "blocked": projection.Blocked}
		if exists && node.Reachability == "reachable" {
			code = "STEP_NOT_READY"
			message = "first step input expression is not satisfied"
			details["missing_groups"] = node.Evaluation.MissingGroups
		}
		response := transitionCommandResponse{Accepted: false, CommandID: req.CommandID, Projection: projection, Error: &transitionError{Code: code, Message: message, Details: details}}
		_ = persistTransitionCommand(store.DB(), req, response, "rejected")
		writeTransitionResponse(w, response, http.StatusConflict)
		return
	}
	if req.TaskID == "" {
		req.TaskID = uuid.NewString()
	}
	handOff := req.HandOff
	params := PluginStepParams{PluginID: req.PluginID, PluginRef: req.PluginRef, RevisionID: req.PluginRevisionID, RevisionNo: req.PluginRevisionNo, TreeHash: req.PluginTreeHash, RemoteRoot: req.PluginRemoteRoot, StepID: req.TargetStepID, UserInput: req.UserInput, IsColdStart: true, HandOff: &handOff, PreflightID: req.PreflightID, ChatSessionID: req.ChatSessionID, PluginMode: req.PluginMode, UserID: req.UserID, HistoryFilesPerTurn: req.HistoryFilesPerTurn, Filters: req.Filters}
	nodeDef := graph.Nodes[req.TargetStepID]
	sessionID, taskID, _, launchErr := launchPluginAttempt(r.Context(), store.DB(), store.State(), req.ConversationID, req.TriggerHistoryID, req.UserID, req.TaskID, req.PluginID+":"+req.TargetStepID, req.Objective, params, graphengine.Materials(nodeDef.Input), nodeDef.Outputs, req.LLMConfig, req.ToolConfig, false)
	if launchErr != nil {
		response := transitionCommandResponse{Accepted: false, CommandID: req.CommandID, Error: &transitionError{Code: "TRANSITION_LAUNCH_FAILED", Message: launchErr.Error(), Retryable: true}}
		_ = persistTransitionCommand(store.DB(), req, response, "rejected")
		writeTransitionResponse(w, response, http.StatusServiceUnavailable)
		return
	}
	_ = store.DB().Model(&orm.PluginSession{}).Where("id = ?", sessionID).Updates(map[string]any{"state_version": 1, "graph_hash": graph.GraphHash, "graph_schema_version": graph.SchemaVersion}).Error
	now := time.Now().UTC()
	revisionIDs := map[string]string{}
	for _, material := range externalMaterials {
		revisionID := "psr_" + common.GenerateID()
		revisionIDs[material.MaterialID] = revisionID
		content, _ := json.Marshal(map[string]any{"value": req.ExternalMaterials[material.MaterialID], "source": "external"})
		_ = store.DB().Create(&orm.PluginSlotRevision{ID: revisionID, SessionID: sessionID, SlotID: material.MaterialID, Revision: 1, Selected: true, ContentSnapshot: content, ChangeSource: "human", Slot: material.MaterialID, StepID: "__start__", Attempt: 0, Validity: "effective", CreatedAt: now}).Error
	}
	materialFacts := make([]graphengine.MaterialValue, 0, len(revisionIDs))
	for materialID, revisionID := range revisionIDs {
		materialFacts = append(materialFacts, graphengine.MaterialValue{MaterialID: materialID, RevisionID: revisionID, Valid: true})
	}
	startDecision := graphengine.DecideRoute(graph, "__start__", materialFacts)
	_ = persistRouteDecision(r.Context(), store.DB(), sessionID, "__start__", "", startDecision.Activated, startDecision.Pruned, startDecision.Bypassed, startDecision.Witnesses, 1)
	var attempt orm.PluginSessionStep
	if store.DB().Where("task_id = ?", taskID).First(&attempt).Error == nil {
		for _, witness := range node.Evaluation.Witnesses {
			revisionID := revisionIDs[witness.MaterialID]
			if revisionID == "" {
				continue
			}
			_ = store.DB().Create(&orm.PluginAttemptInputBinding{ID: "paib_" + common.GenerateID(), SessionID: sessionID, AttemptID: attempt.ID, MaterialID: witness.MaterialID, MaterialRevisionID: revisionID, BindAs: witness.BindAs, CreatedAt: now}).Error
		}
	}
	var session orm.PluginSession
	_ = store.DB().Where("id = ?", sessionID).First(&session).Error
	projected, _ := projectSession(r.Context(), store.DB(), &session)
	response := transitionCommandResponse{Accepted: true, CommandID: req.CommandID, SessionID: sessionID, TaskID: taskID, StateVersion: 1, StepState: "pending", Projection: projected.Projection}
	_ = persistTransitionCommand(store.DB(), req, response, "accepted")
	writeTransitionResponse(w, response, http.StatusOK)
}

type transitionError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type transitionCommandResponse struct {
	Accepted     bool                   `json:"accepted"`
	CommandID    string                 `json:"command_id"`
	SessionID    string                 `json:"session_id,omitempty"`
	TaskID       string                 `json:"task_id,omitempty"`
	StateVersion int64                  `json:"state_version"`
	StepState    string                 `json:"step_state,omitempty"`
	Error        *transitionError       `json:"error,omitempty"`
	Projection   graphengine.Projection `json:"projection"`
}

type transitionRejection struct {
	status   int
	response transitionCommandResponse
}

func (e *transitionRejection) Error() string { return e.response.Error.Message }

func writeTransitionResponse(w http.ResponseWriter, response transitionCommandResponse, status int) {
	if status >= 400 {
		common.ReplyErrWithData(w, response.Error.Message, response, status)
		return
	}
	common.ReplyOK(w, response)
}

func rejectTransition(commandID string, session *orm.PluginSession, projection graphengine.Projection, status int, code, message string, retryable bool, details map[string]any) *transitionRejection {
	return &transitionRejection{status: status, response: transitionCommandResponse{Accepted: false, CommandID: commandID, SessionID: session.ID, StateVersion: session.StateVersion, Projection: projection, Error: &transitionError{Code: code, Message: message, Retryable: retryable, Details: details}}}
}

func persistTransitionCommand(db *gorm.DB, req transitionCommandRequest, response transitionCommandResponse, status string) error {
	body, _ := json.Marshal(response)
	now := time.Now().UTC()
	row := orm.PluginTransitionCommand{CommandID: req.CommandID, SessionID: response.SessionID, Operation: req.Operation, TargetStepID: req.TargetStepID, Status: status, TaskID: response.TaskID, ExpectedStateVersion: req.ExpectedStateVersion, ResultingStateVersion: response.StateVersion, ResponseJSON: body, CreatedAt: now, UpdatedAt: now}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "command_id"}}, DoUpdates: clause.AssignmentColumns([]string{"session_id", "status", "task_id", "resulting_state_version", "response_json", "updated_at"})}).Create(&row).Error
}

func reserveTransitionCommand(db *gorm.DB, req transitionCommandRequest) (bool, error) {
	pending := transitionCommandResponse{Accepted: false, CommandID: req.CommandID, Error: &transitionError{Code: "TRANSITION_RESULT_UNKNOWN", Message: "transition command is still being processed", Retryable: true}}
	body, _ := json.Marshal(pending)
	now := time.Now().UTC()
	row := orm.PluginTransitionCommand{CommandID: req.CommandID, Operation: req.Operation, TargetStepID: req.TargetStepID, Status: "processing", ExpectedStateVersion: req.ExpectedStateVersion, ResponseJSON: body, CreatedAt: now, UpdatedAt: now}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	return result.RowsAffected == 1, result.Error
}

func loadExistingTransition(db *gorm.DB, commandID string) (*transitionCommandResponse, bool) {
	var row orm.PluginTransitionCommand
	if err := db.Where("command_id = ?", commandID).First(&row).Error; err != nil {
		return nil, false
	}
	var response transitionCommandResponse
	if json.Unmarshal(row.ResponseJSON, &response) != nil {
		return nil, false
	}
	return &response, true
}

// TransitionPluginSession is the synchronous, idempotent Python -> Go admission
// boundary. A rejected command is returned immediately and never starts a task.
func TransitionPluginSession(w http.ResponseWriter, r *http.Request) {
	var req transitionCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid transition command", http.StatusBadRequest)
		return
	}
	if req.CommandID == "" {
		req.CommandID = uuid.NewString()
	}
	if existing, ok := loadExistingTransition(store.DB(), req.CommandID); ok {
		status := http.StatusOK
		if !existing.Accepted {
			status = http.StatusConflict
		}
		writeTransitionResponse(w, *existing, status)
		return
	}
	if req.Operation == "" {
		req.Operation = "execute"
	}
	if req.Operation != "execute" && req.Operation != "retry" && req.Operation != "rewind" {
		common.ReplyErr(w, "operation must be execute, retry, or rewind", http.StatusUnprocessableEntity)
		return
	}
	reserved, reserveErr := reserveTransitionCommand(store.DB(), req)
	if reserveErr != nil {
		common.ReplyErr(w, "reserve transition command failed", http.StatusServiceUnavailable)
		return
	}
	if !reserved {
		if existing, ok := loadExistingTransition(store.DB(), req.CommandID); ok {
			writeTransitionResponse(w, *existing, http.StatusConflict)
			return
		}
	}

	var session orm.PluginSession
	var graph *graphengine.CompiledStateGraph
	var evaluation graphengine.Evaluation
	var reservedVersion int64
	var rejection *transitionRejection
	err := store.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND dismissed = false", common.PathVar(r, "session_id")).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &transitionRejection{status: http.StatusNotFound, response: transitionCommandResponse{Accepted: false, CommandID: req.CommandID, Error: &transitionError{Code: "SESSION_NOT_FOUND", Message: "plugin session not found"}}}
			}
			return err
		}
		graphErr := error(nil)
		graph, graphErr = loadSessionGraph(r.Context(), tx, &session)
		if graphErr != nil {
			return graphErr
		}
		snapshot, snapshotErr := loadRuntimeSnapshot(r.Context(), tx, session.ID)
		if snapshotErr != nil {
			return snapshotErr
		}
		projection := graphengine.Project(graph, snapshot)
		if session.Status == SessionStatusCompleted {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "SESSION_TERMINAL", "plugin session is already completed", false, nil)
		}
		if req.ExpectedStateVersion != session.StateVersion {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "STATE_VERSION_CONFLICT", "plugin session state changed; use the returned projection", true, map[string]any{"expected": req.ExpectedStateVersion, "actual": session.StateVersion})
		}
		if req.GraphHash != "" && graph.GraphHash != "" && req.GraphHash != graph.GraphHash {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "GRAPH_REVISION_MISMATCH", "session graph revision does not match the command", false, map[string]any{"expected": req.GraphHash, "actual": graph.GraphHash})
		}
		if _, ok := graph.Nodes[req.TargetStepID]; !ok {
			return rejectTransition(req.CommandID, &session, projection, http.StatusUnprocessableEntity, "INVALID_TARGET", "target step is not defined in the session graph", false, nil)
		}
		if req.Operation == "retry" || req.Operation == "rewind" {
			if invalidErr := invalidateForOperation(r.Context(), tx, &session, graph, req.CommandID, req.Operation, req.TargetStepID); invalidErr != nil {
				return invalidErr
			}
			snapshot, _ = loadRuntimeSnapshot(r.Context(), tx, session.ID)
			projection = graphengine.Project(graph, snapshot)
		}
		node := projection.Nodes[req.TargetStepID]
		if node.Reachability != "reachable" {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "STEP_NOT_REACHABLE", "target step is not currently reachable", false, map[string]any{"ready": projection.Ready, "blocked": projection.Blocked})
		}
		if node.Readiness != "ready" {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "STEP_NOT_READY", "target step input expression is not satisfied", false, map[string]any{"missing_groups": node.Evaluation.MissingGroups, "ready": projection.Ready})
		}
		evaluation = node.Evaluation
		update := tx.Model(&orm.PluginSession{}).Where("id = ? AND state_version = ?", session.ID, session.StateVersion).Updates(map[string]any{"state_version": gorm.Expr("state_version + 1"), "updated_at": time.Now().UTC()})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "STATE_VERSION_CONFLICT", "plugin session state changed during transition", true, nil)
		}
		reservedVersion = session.StateVersion + 1
		return nil
	})
	if err != nil {
		if errors.As(err, &rejection) {
			_ = persistTransitionCommand(store.DB(), req, rejection.response, "rejected")
			writeTransitionResponse(w, rejection.response, rejection.status)
			return
		}
		common.ReplyErr(w, "transition validation failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if req.TaskID == "" {
		req.TaskID = uuid.NewString()
	}
	handOff := req.HandOff
	node := graph.Nodes[req.TargetStepID]
	inputKeys := graphengine.Materials(node.Input)
	for _, optional := range node.OptionalInputs {
		inputKeys = append(inputKeys, optional.Material)
	}
	params := PluginStepParams{PluginID: session.PluginID, PluginRef: session.PluginRef, RevisionID: session.PluginRevisionID, RevisionNo: session.PluginRevisionNo, TreeHash: session.PluginTreeHash, RemoteRoot: session.PluginRemoteRoot, StepID: req.TargetStepID, SessionID: session.ID, UserInput: req.UserInput, HandOff: &handOff, ChatSessionID: req.ChatSessionID, PluginMode: req.PluginMode, RetryHint: req.RuntimeInstruction, PartialIndices: req.PartialIndices, HistoryFilesPerTurn: req.HistoryFilesPerTurn, Filters: req.Filters, UserID: session.CreateUserID}
	_, taskID, _, launchErr := launchPluginAttempt(r.Context(), store.DB(), store.State(), session.ConversationID, session.TriggerHistoryID, session.CreateUserID, req.TaskID, session.PluginID+":"+req.TargetStepID, req.Objective, params, inputKeys, node.Outputs, req.LLMConfig, req.ToolConfig, false)
	if launchErr != nil {
		response := transitionCommandResponse{Accepted: false, CommandID: req.CommandID, SessionID: session.ID, StateVersion: reservedVersion, Error: &transitionError{Code: "TRANSITION_LAUNCH_FAILED", Message: launchErr.Error(), Retryable: true}}
		_ = persistTransitionCommand(store.DB(), req, response, "rejected")
		writeTransitionResponse(w, response, http.StatusServiceUnavailable)
		return
	}
	var attempt orm.PluginSessionStep
	if store.DB().Where("task_id = ?", taskID).First(&attempt).Error == nil {
		now := time.Now().UTC()
		for _, witness := range evaluation.Witnesses {
			_ = store.DB().Create(&orm.PluginAttemptInputBinding{ID: "paib_" + common.GenerateID(), SessionID: session.ID, AttemptID: attempt.ID, MaterialID: witness.MaterialID, MaterialRevisionID: witness.RevisionID, BindAs: witness.BindAs, CreatedAt: now}).Error
		}
	}
	session.StateVersion = reservedVersion
	projection, _ := projectSession(r.Context(), store.DB(), &session)
	response := transitionCommandResponse{Accepted: true, CommandID: req.CommandID, SessionID: session.ID, TaskID: taskID, StateVersion: reservedVersion, StepState: "pending", Projection: projection.Projection}
	_ = persistTransitionCommand(store.DB(), req, response, "accepted")
	writeTransitionResponse(w, response, http.StatusOK)
}

func invalidateForOperation(ctx context.Context, tx *gorm.DB, session *orm.PluginSession, graph *graphengine.CompiledStateGraph, commandID, operation, target string) error {
	var attempt orm.PluginSessionStep
	q := tx.Where("session_id = ? AND step_id = ? AND validity = ?", session.ID, target, "effective").Order("attempt DESC").First(&attempt)
	if q.Error != nil {
		code := "INVALID_REWIND"
		if operation == "retry" {
			code = "INVALID_RETRY"
		}
		return rejectTransition(commandID, session, graphengine.Projection{}, http.StatusConflict, code, "target has no effective attempt to invalidate", false, nil)
	}
	if operation == "retry" && attempt.Status != "failed" && attempt.Status != "interrupted" {
		return rejectTransition(commandID, session, graphengine.Projection{}, http.StatusConflict, "INVALID_RETRY", "only failed or interrupted attempts can be retried", false, nil)
	}
	if operation == "rewind" && attempt.Status != "succeeded" {
		return rejectTransition(commandID, session, graphengine.Projection{}, http.StatusConflict, "INVALID_REWIND", "only succeeded attempts can be rewound", false, nil)
	}
	queue := []orm.PluginSessionStep{attempt}
	seen := map[string]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current.ID] {
			continue
		}
		seen[current.ID] = true
		if err := tx.Model(&orm.PluginSessionStep{}).Where("id = ?", current.ID).Update("validity", "stale").Error; err != nil {
			return err
		}
		var outputs []orm.PluginSlotRevision
		tx.Where("session_id = ? AND ((producer_attempt_id = ? AND producer_attempt_id != '') OR (step_id = ? AND attempt = ?))", session.ID, current.ID, current.StepID, current.Attempt).Find(&outputs)
		for _, output := range outputs {
			_ = tx.Model(&orm.PluginSlotRevision{}).Where("id = ?", output.ID).Updates(map[string]any{"validity": "stale", "selected": false}).Error
			var bindings []orm.PluginAttemptInputBinding
			tx.Where("material_revision_id = ?", output.ID).Find(&bindings)
			for _, binding := range bindings {
				var consumer orm.PluginSessionStep
				if tx.Where("id = ? AND validity = ?", binding.AttemptID, "effective").First(&consumer).Error == nil {
					queue = append(queue, consumer)
				}
			}
			var decisions []orm.PluginRouteDecision
			tx.Where("session_id = ? AND validity = ?", session.ID, "effective").Find(&decisions)
			for _, decision := range decisions {
				var witnesses []graphengine.Witness
				_ = json.Unmarshal(decision.WitnessJSON, &witnesses)
				usesRevision := false
				for _, witness := range witnesses {
					if witness.RevisionID == output.ID {
						usesRevision = true
						break
					}
				}
				if usesRevision {
					enqueueExclusiveRouteAttempts(tx, session.ID, decision, &queue)
					_ = tx.Model(&orm.PluginRouteDecision{}).Where("id = ?", decision.ID).Update("validity", "stale").Error
				}
			}
		}
		var sourceDecisions []orm.PluginRouteDecision
		tx.Where("session_id = ? AND source_attempt_id IN ? AND validity = ?", session.ID, []string{current.ID, current.TaskID}, "effective").Find(&sourceDecisions)
		for _, decision := range sourceDecisions {
			enqueueExclusiveRouteAttempts(tx, session.ID, decision, &queue)
			_ = tx.Model(&orm.PluginRouteDecision{}).Where("id = ?", decision.ID).Update("validity", "stale").Error
		}
	}
	return nil
}

func enqueueExclusiveRouteAttempts(tx *gorm.DB, sessionID string, decision orm.PluginRouteDecision, queue *[]orm.PluginSessionStep) {
	var targets []string
	_ = json.Unmarshal(decision.ActivatedJSON, &targets)
	for _, target := range targets {
		if target == "__end__" {
			continue
		}
		var other []orm.PluginRouteDecision
		tx.Where("session_id = ? AND validity = ? AND id != ?", sessionID, "effective", decision.ID).Find(&other)
		stillActivated := false
		for _, candidate := range other {
			var activated []string
			_ = json.Unmarshal(candidate.ActivatedJSON, &activated)
			for _, value := range activated {
				if value == target {
					stillActivated = true
					break
				}
			}
			if stillActivated {
				break
			}
		}
		if stillActivated {
			continue
		}
		var attempt orm.PluginSessionStep
		if tx.Where("session_id = ? AND step_id = ? AND validity = ?", sessionID, target, "effective").Order("attempt DESC").First(&attempt).Error == nil {
			*queue = append(*queue, attempt)
		}
	}
}

func GetTransitionCommand(w http.ResponseWriter, r *http.Request) {
	if response, ok := loadExistingTransition(store.DB(), common.PathVar(r, "command_id")); ok {
		writeTransitionResponse(w, *response, http.StatusOK)
		return
	}
	common.ReplyErr(w, "transition command not found", http.StatusNotFound)
}
