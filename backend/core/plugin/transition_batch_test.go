package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/plugin/graphengine"
	"lazymind/core/store"
)

func TestAttemptInputBindingIDFitsSchema(t *testing.T) {
	id := newAttemptInputBindingID()
	if len(id) > 36 {
		t.Fatalf("input binding ID length = %d, exceeds varchar(36): %q", len(id), id)
	}
	if !strings.HasPrefix(id, "pib_") {
		t.Fatalf("input binding ID has unexpected prefix: %q", id)
	}
}

func setupBatchTransitionSession(t *testing.T) (*orm.DB, string) {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(
		&orm.PluginRevision{},
		&orm.PluginAttemptInputBinding{},
		&orm.PluginRouteDecision{},
		&orm.PluginTransitionCommand{},
	); err != nil {
		t.Fatalf("migrate batch transition tables: %v", err)
	}
	graph := &graphengine.CompiledStateGraph{
		SchemaVersion: graphengine.SchemaVersion,
		GraphHash:     "batch-graph-hash",
		StartRoute:    "all",
		Nodes: map[string]graphengine.CompiledNode{
			"branch_b":  {ID: "branch_b", Route: "all"},
			"branch_c":  {ID: "branch_c", Route: "all"},
			"blocked_d": {ID: "blocked_d", Route: "all", Input: &graphengine.Expression{Material: "missing_material"}},
		},
		ControlEdges: []graphengine.CompiledEdge{
			{ID: "start-b", From: "__start__", To: "branch_b"},
			{ID: "start-c", From: "__start__", To: "branch_c"},
			{ID: "start-d", From: "__start__", To: "blocked_d"},
		},
		MaterialProducers: map[string]graphengine.ProducerRef{
			"missing_material": {Kind: "external"},
		},
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.PluginRevision{
		ID: "batch-revision", PluginResourceID: "batch-resource", RevisionNo: 1,
		TreeHash: "batch-tree", CompiledGraph: graph.JSON(), GraphHash: graph.GraphHash,
		GraphSchemaVersion: graph.SchemaVersion, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create compiled revision: %v", err)
	}
	if _, err := CreateSession(context.Background(), db.DB, CreateSessionInput{
		SessionID: "batch-session", ConversationID: "batch-conversation", PluginID: "batch-plugin",
		PluginRevisionID: "batch-revision", CreateUserID: "batch-user",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.Model(&orm.PluginSession{}).Where("id = ?", "batch-session").Updates(map[string]any{
		"state_version": 4, "graph_hash": graph.GraphHash, "graph_schema_version": graph.SchemaVersion,
	}).Error; err != nil {
		t.Fatalf("pin graph: %v", err)
	}
	return db, graph.GraphHash
}

func runBatchTransition(t *testing.T, db *orm.DB, graphHash, operation string, targets []map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	oldDB, oldState := store.DB(), store.State()
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(oldDB, oldDB, oldState) })

	algo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(algo.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algo.URL)

	body, _ := json.Marshal(map[string]any{
		"command_id": "batch-command-" + targets[0]["target_step_id"].(string),
		"operation":  operation, "expected_state_version": 4, "graph_hash": graphHash,
		"targets": targets, "plugin_mode": "dynamic",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/plugin-sessions/batch-session:transition", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"session_id": "batch-session"})
	w := httptest.NewRecorder()
	TransitionPluginSession(w, req)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var live int64
		_ = db.Model(&orm.PluginRunOutbox{}).
			Where("status IN ?", []string{"pending", "dispatching"}).Count(&live).Error
		if live == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var envelope map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	data, _ := envelope["data"].(map[string]any)
	return w, data
}

func TestBatchTransitionAcceptsAllReadyTargetsAtomically(t *testing.T) {
	db, graphHash := setupBatchTransitionSession(t)
	w, data := runBatchTransition(t, db, graphHash, "execute_batch", []map[string]any{
		{"target_step_id": "branch_b", "task_id": "batch-task-b", "objective": "run b", "user_input": "run b"},
		{"target_step_id": "branch_c", "task_id": "batch-task-c", "objective": "run c", "user_input": "run c"},
	})
	if w.Code != http.StatusOK || data["accepted"] != true {
		t.Fatalf("batch rejected: status=%d body=%s", w.Code, w.Body.String())
	}
	var session orm.PluginSession
	if err := db.Where("id = ?", "batch-session").First(&session).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.StateVersion != 5 {
		t.Fatalf("state version=%d, want one increment to 5", session.StateVersion)
	}
	var attempts int64
	if err := db.Model(&orm.PluginSessionStep{}).Where("session_id = ?", session.ID).Count(&attempts).Error; err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if tasks, ok := data["tasks"].([]any); !ok || len(tasks) != 2 {
		t.Fatalf("response tasks=%#v, want 2", data["tasks"])
	}
}

func TestBatchTransitionRejectsWholeBatchWhenOneTargetBlocked(t *testing.T) {
	db, graphHash := setupBatchTransitionSession(t)
	w, data := runBatchTransition(t, db, graphHash, "execute_batch", []map[string]any{
		{"target_step_id": "branch_b", "task_id": "rejected-task-b", "objective": "run b", "user_input": "run b"},
		{"target_step_id": "blocked_d", "task_id": "rejected-task-d", "objective": "run d", "user_input": "run d"},
	})
	if w.Code != http.StatusConflict || data["accepted"] != false {
		t.Fatalf("invalid batch response: status=%d body=%s", w.Code, w.Body.String())
	}
	errorData, _ := data["error"].(map[string]any)
	if errorData["code"] != "BATCH_TRANSITION_REJECTED" {
		t.Fatalf("error=%#v", errorData)
	}
	var attempts int64
	_ = db.Model(&orm.PluginSessionStep{}).Where("session_id = ?", "batch-session").Count(&attempts).Error
	if attempts != 0 {
		t.Fatalf("partial batch attempts persisted: %d", attempts)
	}
	var session orm.PluginSession
	_ = db.Where("id = ?", "batch-session").First(&session).Error
	if session.StateVersion != 4 {
		t.Fatalf("rejected batch changed state version to %d", session.StateVersion)
	}
}

func TestBatchTransitionDoesNotAllowRetryOrRewind(t *testing.T) {
	for _, operation := range []string{"retry", "rewind"} {
		t.Run(operation, func(t *testing.T) {
			db, graphHash := setupBatchTransitionSession(t)
			w, _ := runBatchTransition(t, db, graphHash, operation, []map[string]any{
				{"target_step_id": "branch_b", "task_id": operation + "-task-b"},
				{"target_step_id": "branch_c", "task_id": operation + "-task-c"},
			})
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s batch status=%d body=%s", operation, w.Code, w.Body.String())
			}
			var attempts int64
			_ = db.Model(&orm.PluginSessionStep{}).
				Where("session_id = ?", "batch-session").Count(&attempts).Error
			if attempts != 0 {
				t.Fatalf("%s batch persisted %d attempts", operation, attempts)
			}
		})
	}
}

func TestNormalizedTransitionTargetsRejectsDuplicates(t *testing.T) {
	_, err := normalizedTransitionTargets(&transitionCommandRequest{Targets: []transitionTarget{
		{TargetStepID: "same"}, {TargetStepID: "same"},
	}})
	if err == nil {
		t.Fatal("duplicate target must be rejected")
	}
}

func TestResolveAdvanceOperationFromEffectiveAttempt(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "advance-operation-session", ConversationID: "advance-operation-conversation",
		PluginID: "writer-plugin",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	statuses := map[string]string{
		"ready_step":       "",
		"succeeded_step":   StepStatusSucceeded,
		"failed_step":      StepStatusFailed,
		"interrupted_step": StepStatusInterrupted,
	}
	for stepID, status := range statuses {
		if status == "" {
			continue
		}
		step, err := CreateSessionStep(ctx, db.DB, "advance-operation-session", stepID, "task-"+stepID, 1)
		if err != nil {
			t.Fatalf("create %s: %v", stepID, err)
		}
		if err := db.Model(&orm.PluginSessionStep{}).Where("id = ?", step.ID).Update("status", status).Error; err != nil {
			t.Fatalf("set %s status: %v", stepID, err)
		}
	}
	wants := map[string]string{
		"ready_step": "execute", "succeeded_step": "rewind",
		"failed_step": "retry", "interrupted_step": "retry",
	}
	for stepID, want := range wants {
		got, err := resolveAdvanceOperation(ctx, db.DB, "advance-operation-session", stepID)
		if err != nil {
			t.Fatalf("resolve %s: %v", stepID, err)
		}
		if got != want {
			t.Errorf("resolve %s=%q, want %q", stepID, got, want)
		}
	}
}
