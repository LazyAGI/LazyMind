package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/plugin/graphengine"
	"lazymind/core/store"
	"lazymind/core/subagent"
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
	ParentAgenticConfig  map[string]any      `json:"parent_agentic_config"`
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
	Targets              []transitionTarget  `json:"targets,omitempty"`
}

type transitionTarget struct {
	TargetStepID       string           `json:"target_step_id"`
	TaskID             string           `json:"task_id"`
	Objective          string           `json:"objective"`
	UserInput          string           `json:"user_input"`
	RuntimeInstruction string           `json:"runtime_instruction"`
	PartialIndices     map[string][]int `json:"partial_indices"`
}

// plugin_attempt_input_bindings.id is varchar(36). Keep the semantic prefix,
// but unlike the historical "paib_" prefix, fit the 32-character generated ID.
func newAttemptInputBindingID() string {
	return "pib_" + common.GenerateID()
}

type transitionTaskResponse struct {
	StepID    string `json:"step_id"`
	TaskID    string `json:"task_id"`
	StepState string `json:"step_state"`
}

func normalizedTransitionTargets(req *transitionCommandRequest) ([]transitionTarget, error) {
	targets := append([]transitionTarget(nil), req.Targets...)
	if len(targets) == 0 && req.TargetStepID != "" {
		targets = []transitionTarget{{
			TargetStepID: req.TargetStepID, TaskID: req.TaskID, Objective: req.Objective,
			UserInput: req.UserInput, RuntimeInstruction: req.RuntimeInstruction,
			PartialIndices: req.PartialIndices,
		}}
	}
	if len(targets) == 0 {
		return nil, errors.New("at least one transition target is required")
	}
	seenSteps := make(map[string]bool, len(targets))
	seenTasks := make(map[string]bool, len(targets))
	for i := range targets {
		targets[i].TargetStepID = strings.TrimSpace(targets[i].TargetStepID)
		if targets[i].TargetStepID == "" {
			return nil, errors.New("target_step_id is required for every target")
		}
		if seenSteps[targets[i].TargetStepID] {
			return nil, fmt.Errorf("duplicate target step %q", targets[i].TargetStepID)
		}
		seenSteps[targets[i].TargetStepID] = true
		if targets[i].TaskID == "" {
			targets[i].TaskID = uuid.NewString()
		}
		if seenTasks[targets[i].TaskID] {
			return nil, fmt.Errorf("duplicate task id %q", targets[i].TaskID)
		}
		seenTasks[targets[i].TaskID] = true
	}
	return targets, nil
}

// selectLLMChoiceRoutes freezes an N-select-1 route only when ChatAgent starts
// one of its Reachable candidates. The update shares the transition transaction,
// so a batch either selects every compatible route and starts every task or does
// nothing. Multiple targets from the same choice are rejected.
func selectLLMChoiceRoutes(ctx context.Context, tx *gorm.DB, sessionID string, graph *graphengine.CompiledStateGraph, targets []transitionTarget) error {
	targetSet := make(map[string]bool, len(targets))
	for _, target := range targets {
		targetSet[target.TargetStepID] = true
	}
	var decisions []orm.PluginRouteDecision
	if err := tx.WithContext(ctx).Where("session_id = ? AND validity = ?", sessionID, "effective").Find(&decisions).Error; err != nil {
		return err
	}
	for _, decision := range decisions {
		route := graph.StartRoute
		if node, ok := graph.Nodes[decision.FromStepID]; ok {
			route = node.Route
		}
		if route != "choice" {
			continue
		}
		hasLLMHint := false
		for _, edge := range graph.ControlEdges {
			if edge.From == decision.FromStepID && (edge.When != "" || edge.Legacy != "") {
				hasLLMHint = true
				break
			}
		}
		if !hasLLMHint {
			continue
		}
		var active, pruned []string
		if err := json.Unmarshal(decision.ActivatedJSON, &active); err != nil {
			return err
		}
		_ = json.Unmarshal(decision.PrunedJSON, &pruned)
		selected := ""
		for _, candidate := range active {
			if !targetSet[candidate] {
				continue
			}
			if selected != "" && selected != candidate {
				return fmt.Errorf("steps %s and %s belong to the same N-select-1 route from %s", selected, candidate, decision.FromStepID)
			}
			selected = candidate
		}
		if selected == "" {
			continue
		}
		for _, candidate := range active {
			if candidate == selected {
				continue
			}
			found := false
			for _, existing := range pruned {
				if existing == candidate {
					found = true
					break
				}
			}
			if !found {
				pruned = append(pruned, candidate)
			}
		}
		activeJSON, _ := json.Marshal([]string{selected})
		prunedJSON, _ := json.Marshal(pruned)
		if err := tx.Model(&orm.PluginRouteDecision{}).Where("id = ?", decision.ID).Updates(map[string]any{
			"activated_json": activeJSON,
			"pruned_json":    prunedJSON,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func commandTargetID(req transitionCommandRequest) string {
	if len(req.Targets) > 1 {
		return "__batch__"
	}
	if len(req.Targets) == 1 {
		return req.Targets[0].TargetStepID
	}
	return req.TargetStepID
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
	params := PluginStepParams{PluginID: req.PluginID, PluginRef: req.PluginRef, RevisionID: req.PluginRevisionID, RevisionNo: req.PluginRevisionNo, TreeHash: req.PluginTreeHash, RemoteRoot: req.PluginRemoteRoot, StepID: req.TargetStepID, UserInput: req.UserInput, IsColdStart: true, HandOff: &handOff, PreflightID: req.PreflightID, ChatSessionID: req.ChatSessionID, PluginMode: req.PluginMode, UserID: req.UserID, HistoryFilesPerTurn: req.HistoryFilesPerTurn, Filters: req.Filters, ParentAgenticConfig: req.ParentAgenticConfig, RequiredOutputs: graph.Nodes[req.TargetStepID].RequiredOutputs}
	nodeDef := graph.Nodes[req.TargetStepID]
	inputKeys := graphengine.Materials(nodeDef.Input)
	for _, optional := range nodeDef.OptionalInputs {
		inputKeys = append(inputKeys, optional.Material)
	}
	var sessionID, taskID string
	var response transitionCommandResponse
	launchErr := store.DB().Transaction(func(tx *gorm.DB) error {
		var err error
		sessionID, taskID, _, err = launchPluginAttempt(r.Context(), tx, store.State(), req.ConversationID, req.TriggerHistoryID, req.UserID, req.TaskID, req.PluginID+":"+req.TargetStepID, req.Objective, params, inputKeys, nodeDef.Outputs, req.LLMConfig, req.ToolConfig, false, false)
		if err != nil {
			return err
		}
		if err := tx.Model(&orm.PluginSession{}).Where("id = ?", sessionID).Updates(map[string]any{"state_version": 1, "graph_hash": graph.GraphHash, "graph_schema_version": graph.SchemaVersion}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		revisionIDs := map[string]string{}
		for _, material := range externalMaterials {
			revisionID := "psr_" + common.GenerateID()
			revisionIDs[material.MaterialID] = revisionID
			content, _ := json.Marshal(map[string]any{"value": req.ExternalMaterials[material.MaterialID], "source": "external"})
			if err := tx.Create(&orm.PluginSlotRevision{ID: revisionID, SessionID: sessionID, SlotID: material.MaterialID, Revision: 1, Selected: true, ContentSnapshot: content, ChangeSource: "human", Slot: material.MaterialID, StepID: "__start__", Attempt: 0, Validity: "effective", CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		materialFacts := make([]graphengine.MaterialValue, 0, len(revisionIDs))
		for materialID, revisionID := range revisionIDs {
			materialFacts = append(materialFacts, graphengine.MaterialValue{MaterialID: materialID, RevisionID: revisionID, Valid: true})
		}
		startDecision := graphengine.DecideRoute(graph, "__start__", materialFacts)
		startDecision = graphengine.SelectRouteTarget(graph, "__start__", req.TargetStepID, startDecision)
		if err := persistRouteDecision(r.Context(), tx, sessionID, "__start__", "", startDecision.Activated, startDecision.Pruned, startDecision.Bypassed, startDecision.Witnesses, 1); err != nil {
			return err
		}
		var attempt orm.PluginSessionStep
		if err := tx.Where("task_id = ?", taskID).First(&attempt).Error; err != nil {
			return err
		}
		witnesses := append([]graphengine.Witness{}, node.Evaluation.Witnesses...)
		witnesses = append(witnesses, graphengine.EvaluateOptional(nodeDef.OptionalInputs, materialFacts).Witnesses...)
		for _, witness := range witnesses {
			revisionID := revisionIDs[witness.MaterialID]
			if revisionID == "" {
				continue
			}
			if err := tx.Create(&orm.PluginAttemptInputBinding{ID: newAttemptInputBindingID(), SessionID: sessionID, AttemptID: attempt.ID, MaterialID: witness.MaterialID, MaterialRevisionID: revisionID, BindAs: witness.BindAs, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		var session orm.PluginSession
		if err := tx.Where("id = ?", sessionID).First(&session).Error; err != nil {
			return err
		}
		projected, err := projectSession(r.Context(), tx, &session)
		if err != nil {
			return err
		}
		response = transitionCommandResponse{Accepted: true, CommandID: req.CommandID, SessionID: sessionID, TaskID: taskID, StateVersion: 1, StepState: "pending", Projection: projected.Projection}
		return persistTransitionCommand(tx, req, response, "accepted")
	})
	if launchErr != nil {
		response = transitionCommandResponse{Accepted: false, CommandID: req.CommandID, Error: &transitionError{Code: "TRANSITION_LAUNCH_FAILED", Message: launchErr.Error(), Retryable: true}}
		_ = persistTransitionCommand(store.DB(), req, response, "rejected")
		writeTransitionResponse(w, response, http.StatusServiceUnavailable)
		return
	}
	dispatchPluginAttemptRunner(store.DB(), store.State(), taskID)
	emitTaskCreatedConvEvent(r.Context(), taskID, sessionID, req.ConversationID)
	writeTransitionResponse(w, response, http.StatusOK)
}

type transitionError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type transitionCommandResponse struct {
	Accepted     bool                     `json:"accepted"`
	CommandID    string                   `json:"command_id"`
	SessionID    string                   `json:"session_id,omitempty"`
	TaskID       string                   `json:"task_id,omitempty"`
	StateVersion int64                    `json:"state_version"`
	StepState    string                   `json:"step_state,omitempty"`
	Tasks        []transitionTaskResponse `json:"tasks,omitempty"`
	Error        *transitionError         `json:"error,omitempty"`
	Projection   graphengine.Projection   `json:"projection"`
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
	row := orm.PluginTransitionCommand{CommandID: req.CommandID, SessionID: response.SessionID, Operation: req.Operation, TargetStepID: commandTargetID(req), Status: status, TaskID: response.TaskID, ExpectedStateVersion: req.ExpectedStateVersion, ResultingStateVersion: response.StateVersion, ResponseJSON: body, CreatedAt: now, UpdatedAt: now}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "command_id"}}, DoUpdates: clause.AssignmentColumns([]string{"session_id", "operation", "status", "task_id", "resulting_state_version", "response_json", "updated_at"})}).Create(&row).Error
}

func reserveTransitionCommand(db *gorm.DB, req transitionCommandRequest) (bool, error) {
	pending := transitionCommandResponse{Accepted: false, CommandID: req.CommandID, Error: &transitionError{Code: "TRANSITION_RESULT_UNKNOWN", Message: "transition command is still being processed", Retryable: true}}
	body, _ := json.Marshal(pending)
	now := time.Now().UTC()
	row := orm.PluginTransitionCommand{CommandID: req.CommandID, Operation: req.Operation, TargetStepID: commandTargetID(req), Status: "processing", ExpectedStateVersion: req.ExpectedStateVersion, ResponseJSON: body, CreatedAt: now, UpdatedAt: now}
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
	targets, targetErr := normalizedTransitionTargets(&req)
	if targetErr != nil {
		common.ReplyErr(w, targetErr.Error(), http.StatusUnprocessableEntity)
		return
	}
	req.Targets = targets
	req.TargetStepID = targets[0].TargetStepID
	req.TaskID = targets[0].TaskID
	if req.Operation == "" || (req.Operation == "execute" && len(targets) > 1) {
		if len(targets) > 1 {
			req.Operation = "execute_batch"
		} else {
			req.Operation = "execute"
		}
	}
	if req.Operation != "advance" && req.Operation != "execute" && req.Operation != "execute_batch" && req.Operation != "retry" && req.Operation != "rewind" {
		common.ReplyErr(w, "operation must be advance, execute, execute_batch, retry, or rewind", http.StatusUnprocessableEntity)
		return
	}
	if (req.Operation == "advance" || req.Operation == "retry" || req.Operation == "rewind") && len(targets) != 1 {
		common.ReplyErr(w, "advance, retry, and rewind require exactly one target", http.StatusUnprocessableEntity)
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
	var reservedVersion int64
	taskIDs := make([]string, 0, len(targets))
	var response transitionCommandResponse
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
			var changed *pluginDefinitionChangedError
			if errors.As(graphErr, &changed) {
				fmt.Printf("[plugin.transition] rejected code=%s command=%s session=%s target=%s expected_hash=%s actual_hash=%s\n",
					pluginDefinitionChangedCode, req.CommandID, session.ID, req.TargetStepID, changed.expected, changed.actual)
				return rejectTransition(req.CommandID, &session, graphengine.Projection{}, http.StatusConflict,
					pluginDefinitionChangedCode, changed.Error(), false,
					map[string]any{"expected": changed.expected, "actual": changed.actual})
			}
			return graphErr
		}
		snapshot, snapshotErr := loadRuntimeSnapshot(r.Context(), tx, session.ID)
		if snapshotErr != nil {
			return snapshotErr
		}
		projection := graphengine.Project(graph, snapshot)
		if req.ExpectedStateVersion != session.StateVersion {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "STATE_VERSION_CONFLICT", "plugin session state changed; use the returned projection", true, map[string]any{"expected": req.ExpectedStateVersion, "actual": session.StateVersion})
		}
		if req.GraphHash != "" && graph.GraphHash != "" && req.GraphHash != graph.GraphHash {
			fmt.Printf("[plugin.transition] rejected code=GRAPH_REVISION_MISMATCH command=%s session=%s target=%s operation=%s expected_hash=%s actual_hash=%s state_version=%d\n",
				req.CommandID, session.ID, req.TargetStepID, req.Operation, req.GraphHash, graph.GraphHash, session.StateVersion)
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "GRAPH_REVISION_MISMATCH", "session graph revision does not match the command", false, map[string]any{"expected": req.GraphHash, "actual": graph.GraphHash})
		}
		if req.Operation == "advance" {
			resolved, resolveErr := resolveAdvanceOperation(r.Context(), tx, session.ID, targets[0].TargetStepID)
			if resolveErr != nil {
				return resolveErr
			}
			req.Operation = resolved
		}
		if session.Status == SessionStatusCompleted && req.Operation != "retry" && req.Operation != "rewind" {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "SESSION_TERMINAL", "plugin session is already completed", false, nil)
		}
		if req.Operation == "retry" || req.Operation == "rewind" {
			if invalidErr := invalidateForOperation(r.Context(), tx, &session, graph, req.CommandID, req.Operation, targets[0].TargetStepID); invalidErr != nil {
				return invalidErr
			}
			var reloadErr error
			snapshot, reloadErr = loadRuntimeSnapshot(r.Context(), tx, session.ID)
			if reloadErr != nil {
				return reloadErr
			}
			projection = graphengine.Project(graph, snapshot)
		}
		evaluations := make(map[string]graphengine.Evaluation, len(targets))
		invalidTargets := make([]map[string]any, 0)
		for _, target := range targets {
			nodeDef, exists := graph.Nodes[target.TargetStepID]
			if !exists {
				invalidTargets = append(invalidTargets, map[string]any{"step_id": target.TargetStepID, "code": "INVALID_TARGET"})
				continue
			}
			node := projection.Nodes[target.TargetStepID]
			if node.Reachability != "reachable" {
				invalidTargets = append(invalidTargets, map[string]any{"step_id": target.TargetStepID, "code": "STEP_NOT_REACHABLE"})
				continue
			}
			if node.Readiness != "ready" {
				invalidTargets = append(invalidTargets, map[string]any{"step_id": target.TargetStepID, "code": "STEP_NOT_READY", "missing_groups": node.Evaluation.MissingGroups})
				continue
			}
			evaluation := node.Evaluation
			evaluation.Witnesses = append(evaluation.Witnesses, graphengine.EvaluateOptional(nodeDef.OptionalInputs, snapshot.Materials).Witnesses...)
			evaluations[target.TargetStepID] = evaluation
		}
		if len(invalidTargets) > 0 {
			if len(targets) == 1 {
				invalid := invalidTargets[0]
				code := invalid["code"].(string)
				message := "target step is not currently reachable"
				details := map[string]any{"ready": projection.Ready, "blocked": projection.Blocked}
				status := http.StatusConflict
				if code == "INVALID_TARGET" {
					message = "target step is not defined in the session graph"
					status = http.StatusUnprocessableEntity
				} else if code == "STEP_NOT_READY" {
					message = "target step input expression is not satisfied"
					details["missing_groups"] = invalid["missing_groups"]
				}
				return rejectTransition(req.CommandID, &session, projection, status, code, message, false, details)
			}
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict,
				"BATCH_TRANSITION_REJECTED", "one or more batch targets are not currently Ready; no target was started", false,
				map[string]any{"targets": invalidTargets, "ready": projection.Ready, "blocked": projection.Blocked})
		}
		if choiceErr := selectLLMChoiceRoutes(r.Context(), tx, session.ID, graph, targets); choiceErr != nil {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict,
				"BATCH_CHOICE_CONFLICT", choiceErr.Error(), false, map[string]any{"ready": projection.Ready})
		}
		update := tx.Model(&orm.PluginSession{}).Where("id = ? AND state_version = ?", session.ID, session.StateVersion).Updates(map[string]any{"state_version": gorm.Expr("state_version + 1"), "updated_at": time.Now().UTC()})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return rejectTransition(req.CommandID, &session, projection, http.StatusConflict, "STATE_VERSION_CONFLICT", "plugin session state changed during transition", true, nil)
		}
		reservedVersion = session.StateVersion + 1
		now := time.Now().UTC()
		responseTasks := make([]transitionTaskResponse, 0, len(targets))
		for _, target := range targets {
			handOff := req.HandOff
			nodeDef := graph.Nodes[target.TargetStepID]
			inputKeys := graphengine.Materials(nodeDef.Input)
			for _, optional := range nodeDef.OptionalInputs {
				inputKeys = append(inputKeys, optional.Material)
			}
			params := PluginStepParams{PluginID: session.PluginID, PluginRef: session.PluginRef, RevisionID: session.PluginRevisionID, RevisionNo: session.PluginRevisionNo, TreeHash: session.PluginTreeHash, RemoteRoot: session.PluginRemoteRoot, StepID: target.TargetStepID, SessionID: session.ID, UserInput: target.UserInput, HandOff: &handOff, ChatSessionID: req.ChatSessionID, PluginMode: req.PluginMode, RetryHint: target.RuntimeInstruction, PartialIndices: target.PartialIndices, HistoryFilesPerTurn: req.HistoryFilesPerTurn, Filters: req.Filters, ParentAgenticConfig: req.ParentAgenticConfig, UserID: session.CreateUserID, RequiredOutputs: nodeDef.RequiredOutputs}
			_, taskID, _, launchErr := launchPluginAttempt(r.Context(), tx, store.State(), session.ConversationID, session.TriggerHistoryID, session.CreateUserID, target.TaskID, session.PluginID+":"+target.TargetStepID, target.Objective, params, inputKeys, nodeDef.Outputs, req.LLMConfig, req.ToolConfig, false, false)
			if launchErr != nil {
				return launchErr
			}
			var attempt orm.PluginSessionStep
			if err := tx.Where("task_id = ?", taskID).First(&attempt).Error; err != nil {
				return err
			}
			for _, witness := range evaluations[target.TargetStepID].Witnesses {
				if err := tx.Create(&orm.PluginAttemptInputBinding{ID: newAttemptInputBindingID(), SessionID: session.ID, AttemptID: attempt.ID, MaterialID: witness.MaterialID, MaterialRevisionID: witness.RevisionID, BindAs: witness.BindAs, CreatedAt: now}).Error; err != nil {
					return err
				}
			}
			taskIDs = append(taskIDs, taskID)
			responseTasks = append(responseTasks, transitionTaskResponse{StepID: target.TargetStepID, TaskID: taskID, StepState: "pending"})
		}
		session.StateVersion = reservedVersion
		projected, err := projectSession(r.Context(), tx, &session)
		if err != nil {
			return err
		}
		response = transitionCommandResponse{Accepted: true, CommandID: req.CommandID, SessionID: session.ID, TaskID: taskIDs[0], StateVersion: reservedVersion, StepState: "pending", Tasks: responseTasks, Projection: projected.Projection}
		return persistTransitionCommand(tx, req, response, "accepted")
	})
	if err != nil {
		if errors.As(err, &rejection) {
			_ = persistTransitionCommand(store.DB(), req, rejection.response, "rejected")
			writeTransitionResponse(w, rejection.response, rejection.status)
			return
		}
		response = transitionCommandResponse{Accepted: false, CommandID: req.CommandID, SessionID: session.ID, StateVersion: session.StateVersion, Error: &transitionError{Code: "TRANSITION_LAUNCH_FAILED", Message: err.Error(), Retryable: true}}
		_ = persistTransitionCommand(store.DB(), req, response, "rejected")
		writeTransitionResponse(w, response, http.StatusServiceUnavailable)
		return
	}
	for _, taskID := range taskIDs {
		dispatchPluginAttemptRunner(store.DB(), store.State(), taskID)
		emitTaskCreatedConvEvent(r.Context(), taskID, session.ID, session.ConversationID)
	}
	writeTransitionResponse(w, response, http.StatusOK)
}

// resolveAdvanceOperation keeps lifecycle vocabulary out of the model-facing
// tool. Selecting a target is sufficient; the authoritative effective attempt
// determines whether this is a forward execution, retry, or rewind.
func resolveAdvanceOperation(ctx context.Context, tx *gorm.DB, sessionID, target string) (string, error) {
	var attempt orm.PluginSessionStep
	err := tx.WithContext(ctx).Where("session_id = ? AND step_id = ? AND validity = ?", sessionID, target, "effective").Order("attempt DESC").First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "execute", nil
	}
	if err != nil {
		return "", err
	}
	switch attempt.Status {
	case "succeeded":
		return "rewind", nil
	case "failed", "interrupted":
		return "retry", nil
	default:
		return "execute", nil
	}
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
		if err := tx.Where("session_id = ? AND ((producer_attempt_id = ? AND producer_attempt_id != '') OR (step_id = ? AND attempt = ?))", session.ID, current.ID, current.StepID, current.Attempt).Find(&outputs).Error; err != nil {
			return err
		}
		for _, output := range outputs {
			if err := tx.Model(&orm.PluginSlotRevision{}).Where("id = ?", output.ID).Updates(map[string]any{"validity": "stale", "selected": false}).Error; err != nil {
				return err
			}
			var bindings []orm.PluginAttemptInputBinding
			if err := tx.Where("material_revision_id = ?", output.ID).Find(&bindings).Error; err != nil {
				return err
			}
			for _, binding := range bindings {
				var consumer orm.PluginSessionStep
				if tx.Where("id = ? AND validity = ?", binding.AttemptID, "effective").First(&consumer).Error == nil {
					queue = append(queue, consumer)
				}
			}
			var decisions []orm.PluginRouteDecision
			if err := tx.Where("session_id = ? AND validity = ?", session.ID, "effective").Find(&decisions).Error; err != nil {
				return err
			}
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
					if err := enqueueExclusiveRouteAttempts(tx, session.ID, decision, &queue); err != nil {
						return err
					}
					if err := tx.Model(&orm.PluginRouteDecision{}).Where("id = ?", decision.ID).Update("validity", "stale").Error; err != nil {
						return err
					}
				}
			}
		}
		var sourceDecisions []orm.PluginRouteDecision
		if err := tx.Where("session_id = ? AND source_attempt_id IN ? AND validity = ?", session.ID, []string{current.ID, current.TaskID}, "effective").Find(&sourceDecisions).Error; err != nil {
			return err
		}
		for _, decision := range sourceDecisions {
			if err := enqueueExclusiveRouteAttempts(tx, session.ID, decision, &queue); err != nil {
				return err
			}
			if err := tx.Model(&orm.PluginRouteDecision{}).Where("id = ?", decision.ID).Update("validity", "stale").Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func enqueueExclusiveRouteAttempts(tx *gorm.DB, sessionID string, decision orm.PluginRouteDecision, queue *[]orm.PluginSessionStep) error {
	var targets []string
	_ = json.Unmarshal(decision.ActivatedJSON, &targets)
	for _, target := range targets {
		if target == "__end__" {
			continue
		}
		var other []orm.PluginRouteDecision
		if err := tx.Where("session_id = ? AND validity = ? AND id != ?", sessionID, "effective", decision.ID).Find(&other).Error; err != nil {
			return err
		}
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
		query := tx.Where("session_id = ? AND step_id = ? AND validity = ?", sessionID, target, "effective").Order("attempt DESC").First(&attempt)
		if query.Error == nil {
			*queue = append(*queue, attempt)
		} else if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return query.Error
		}
	}
	return nil
}

func GetTransitionCommand(w http.ResponseWriter, r *http.Request) {
	if response, ok := loadExistingTransition(store.DB(), common.PathVar(r, "command_id")); ok {
		writeTransitionResponse(w, *response, http.StatusOK)
		return
	}
	common.ReplyErr(w, "transition command not found", http.StatusNotFound)
}

// emitTaskCreatedConvEvent pushes a task_created event to the conversation-level
// events channel so that the frontend TaskCenter panel receives the notification
// and subscribes to the task's SSE stream for real-time status updates.
// This is the graph-engine equivalent of the legacy handlePluginStepCreated path.
func emitTaskCreatedConvEvent(ctx context.Context, taskID, sessionID, conversationID string) {
	if subagent.EventHooks == nil || conversationID == "" || taskID == "" {
		return
	}
	task, err := subagent.GetTask(ctx, store.DB(), taskID)
	if err != nil || task == nil {
		fmt.Printf("[plugin] emitTaskCreatedConvEvent: task lookup failed taskID=%s err=%v\n", taskID, err)
		return
	}
	subagent.EventHooks.CallConversationEvent(ctx, store.State(), conversationID, "", "task_created", map[string]any{
		"task_id":             task.ID,
		"title":               task.Title,
		"agent_type":          task.AgentType,
		"mode":                task.Mode,
		"status":              task.Status,
		"seq_in_conversation": task.SeqInConversation,
		"plugin_session_id":   sessionID,
	})
}
