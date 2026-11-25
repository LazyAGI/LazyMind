package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func TestApplyApprovalPreferencesOverridesOnlyStoredSteps(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowApprovalPreference{}); err != nil {
		t.Fatalf("migrate approval preferences: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowApprovalPreference{
		UserID: "user-a", WorkflowID: "workflow-a", StepID: "review-a",
		ApprovalRequired: false, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create approval preference: %v", err)
	}
	projection := graphengine.Projection{Nodes: map[string]graphengine.NodeProjection{
		"review-a": {ID: "review-a", RequiresApproval: true},
		"review-b": {ID: "review-b", RequiresApproval: true},
	}}

	got := applyApprovalPreferences(db.DB, "user-a", "workflow-a", projection)

	if got.Nodes["review-a"].RequiresApproval {
		t.Fatal("stored step should no longer require approval")
	}
	if !got.Nodes["review-b"].RequiresApproval {
		t.Fatal("missing preference must inherit the workflow default")
	}
}

func TestSetWorkflowApprovalPreferencePersistsForFutureProjections(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(
		&orm.WorkflowApprovalPreference{},
		&orm.WorkflowRevision{},
	); err != nil {
		t.Fatalf("migrate approval preference handler tables: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	graph := &graphengine.CompiledStateGraph{
		SchemaVersion: graphengine.SchemaVersion,
		GraphHash:     "approval-preference-graph",
		StartRoute:    "review",
		Nodes: map[string]graphengine.CompiledNode{
			"review": {ID: "review", Mode: "human"},
			"next":   {ID: "next", Mode: "human"},
		},
		ControlEdges: []graphengine.CompiledEdge{
			{From: "__start__", To: "review"},
			{From: "review", To: "next"},
			{From: "next", To: "__end__"},
		},
	}
	if err := db.Create(&orm.WorkflowRevision{
		ID: "approval-revision", WorkflowResourceID: "approval-resource", RevisionNo: 1,
		CompiledGraph: graph.JSON(), GraphHash: graph.GraphHash,
		GraphSchemaVersion: graph.SchemaVersion, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create workflow revision: %v", err)
	}
	if _, err := CreateSession(context.Background(), db.DB, CreateSessionInput{
		SessionID: "approval-session", ConversationID: "approval-conversation",
		WorkflowID: "approval-workflow", WorkflowRevisionID: "approval-revision",
		GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion,
		CreateUserID: "user-a",
	}); err != nil {
		t.Fatalf("create workflow session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/workflow-sessions/approval-session:approval-preference", strings.NewReader(`{
		"step_id":"review","scope":"step","approval_required":false
	}`))
	req = mux.SetURLVars(req, map[string]string{"session_id": "approval-session"})
	req.Header.Set("X-User-Id", "user-a")
	req.Header.Set("Origin", "http://localhost")
	rec := httptest.NewRecorder()
	SetWorkflowApprovalPreference(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set approval preference status=%d body=%s", rec.Code, rec.Body.String())
	}

	var stored orm.WorkflowApprovalPreference
	if err := db.Where(
		"user_id = ? AND workflow_id = ? AND step_id = ?",
		"user-a", "approval-workflow", "review",
	).First(&stored).Error; err != nil {
		t.Fatalf("load stored approval preference: %v", err)
	}
	if stored.ApprovalRequired {
		t.Fatal("stored approval preference must permanently disable this checkpoint")
	}

	future := graphengine.Project(graph, graphengine.RuntimeSnapshot{})
	future = applyApprovalPreferences(db.DB, "user-a", "approval-workflow", future)
	if future.Nodes["review"].RequiresApproval {
		t.Fatal("a future projection for the same user and workflow must skip review approval")
	}
	if !future.Nodes["next"].RequiresApproval {
		t.Fatal("step-scoped preference must not disable a different checkpoint")
	}
}

func TestFollowingApprovalPreferenceDisablesWholeWorkflowInFutureRuns(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(
		&orm.WorkflowApprovalPreference{},
		&orm.WorkflowRevision{},
	); err != nil {
		t.Fatalf("migrate approval preference handler tables: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	graph := &graphengine.CompiledStateGraph{
		SchemaVersion: graphengine.SchemaVersion,
		GraphHash:     "workflow-wide-approval-preference-graph",
		StartRoute:    "before",
		Nodes: map[string]graphengine.CompiledNode{
			"before":  {ID: "before", Mode: "human"},
			"current": {ID: "current", Mode: "human"},
			"after":   {ID: "after", Mode: "human"},
		},
		ControlEdges: []graphengine.CompiledEdge{
			{From: "__start__", To: "before"},
			{From: "before", To: "current"},
			{From: "current", To: "after"},
			{From: "after", To: "__end__"},
		},
	}
	if err := db.Create(&orm.WorkflowRevision{
		ID: "workflow-wide-approval-revision", WorkflowResourceID: "approval-resource", RevisionNo: 1,
		CompiledGraph: graph.JSON(), GraphHash: graph.GraphHash,
		GraphSchemaVersion: graph.SchemaVersion, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create workflow revision: %v", err)
	}
	if _, err := CreateSession(context.Background(), db.DB, CreateSessionInput{
		SessionID: "workflow-wide-approval-session", ConversationID: "approval-conversation",
		WorkflowID: "approval-workflow", WorkflowRevisionID: "workflow-wide-approval-revision",
		GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion,
		CreateUserID: "user-a",
	}); err != nil {
		t.Fatalf("create workflow session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/workflow-sessions/workflow-wide-approval-session:approval-preference", strings.NewReader(`{
		"step_id":"current","scope":"following","approval_required":false
	}`))
	req = mux.SetURLVars(req, map[string]string{"session_id": "workflow-wide-approval-session"})
	req.Header.Set("X-User-Id", "user-a")
	req.Header.Set("Origin", "http://localhost")
	rec := httptest.NewRecorder()
	SetWorkflowApprovalPreference(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set approval preference status=%d body=%s", rec.Code, rec.Body.String())
	}

	var stored orm.WorkflowApprovalPreference
	if err := db.Where(
		"user_id = ? AND workflow_id = ? AND step_id = ?",
		"user-a", "approval-workflow", workflowApprovalPreferenceAllSteps,
	).First(&stored).Error; err != nil {
		t.Fatalf("load workflow-wide approval preference: %v", err)
	}
	if stored.ApprovalRequired {
		t.Fatal("workflow-wide preference must permanently disable approval")
	}

	future := applyApprovalPreferences(
		db.DB,
		"user-a",
		"approval-workflow",
		graphengine.Project(graph, graphengine.RuntimeSnapshot{}),
	)
	for _, stepID := range []string{"before", "current", "after"} {
		if future.Nodes[stepID].RequiresApproval {
			t.Fatalf("future runs must skip approval for %s", stepID)
		}
	}
	if got := future.Nodes["current"].RequiresApproval; got {
		t.Fatal("the checkpoint where the preference was selected must also be disabled")
	}
}

func TestSessionApprovalSettingsAreScopedAndReversible(t *testing.T) {
	for _, host := range []string{"lazymind", "external-agent"} {
		t.Run(host, func(t *testing.T) {
			db := newTestDB(t)
			if err := db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowHumanArtifact{}); err != nil {
				t.Fatal(err)
			}
			graph := &graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, GraphHash: "mode-graph", StartRoute: "review", Nodes: map[string]graphengine.CompiledNode{
				"review": {ID: "review", Mode: "human"}, "automatic": {ID: "automatic", Mode: "auto"},
			}, ControlEdges: []graphengine.CompiledEdge{{From: "__start__", To: "review"}, {From: "review", To: "automatic"}, {From: "automatic", To: "__end__"}}}
			if err := db.Create(&orm.WorkflowRevision{ID: "revision", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON(), GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion}).Error; err != nil {
				t.Fatal(err)
			}
			session, err := CreateSession(context.Background(), db.DB, CreateSessionInput{SessionID: "run", ConversationID: "chat", WorkflowID: "workflow", WorkflowRevisionID: "revision", CreateUserID: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			session.ControllerHost = host
			if err := db.Model(session).Update("controller_host", host).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "review", TaskID: "task", Attempt: 1, Status: "running", Validity: "effective", ReviewRequired: true}).Error; err != nil {
				t.Fatal(err)
			}
			change := func(scope string, required bool) {
				t.Helper()
				version := session.StateVersion
				if err := setSessionApprovalMode(context.Background(), db.DB, session.ID, "owner", approvalPreferenceRequest{Scope: scope, ApprovalRequired: required, ExpectedStateVersion: &version}); err != nil {
					t.Fatal(err)
				}
				if err := db.First(session, "id = ?", session.ID).Error; err != nil {
					t.Fatal(err)
				}
				p := projectSessionWithApprovalPreferences(db.DB, *session, graph, graphengine.RuntimeSnapshot{})
				if p.Nodes["review"].RequiresApproval != required || p.Nodes["automatic"].RequiresApproval {
					t.Fatalf("wrong effective policy: %+v", p.Nodes)
				}
				var attempt orm.WorkflowSessionStep
				if err := db.First(&attempt, "id = ?", "attempt").Error; err != nil {
					t.Fatal(err)
				}
				if attempt.ReviewRequired != required || attempt.Status != "running" {
					t.Fatalf("inflight attempt changed incorrectly: %+v", attempt)
				}
			}
			change("session", false)
			inherited := projectWithApprovalPreferences(db.DB, "owner", "workflow", graph, graphengine.RuntimeSnapshot{})
			if !inherited.Nodes["review"].RequiresApproval {
				t.Fatal("run-only setting leaked to future runs")
			}
			change("session", true)
			change("workflow", false)
			future, err := CreateSession(context.Background(), db.DB, CreateSessionInput{SessionID: "future", ConversationID: "other-chat", WorkflowID: "workflow", CreateUserID: "owner"})
			if err != nil || future.WorkflowMode != "auto" {
				t.Fatalf("future run mode: %+v, %v", future, err)
			}
			// A per-run opt-in overrides the saved automatic preference without changing it.
			change("session", true)
			inherited = projectWithApprovalPreferences(db.DB, "owner", "workflow", graph, graphengine.RuntimeSnapshot{})
			if inherited.Nodes["review"].RequiresApproval {
				t.Fatal("run-only restoration leaked to future runs")
			}
			change("workflow", true)
			inherited = projectWithApprovalPreferences(db.DB, "owner", "workflow", graph, graphengine.RuntimeSnapshot{})
			if !inherited.Nodes["review"].RequiresApproval || inherited.Nodes["automatic"].RequiresApproval {
				t.Fatal("restoration must recover package checkpoints only")
			}
			if host == "lazymind" {
				if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("status", "succeeded").Error; err != nil {
					t.Fatal(err)
				}
				version := session.StateVersion
				if err := setSessionApprovalMode(context.Background(), db.DB, session.ID, "owner", approvalPreferenceRequest{Scope: "session", ApprovalRequired: false, ExpectedStateVersion: &version}); err != nil {
					t.Fatal(err)
				}
				if err := db.First(session, "id = ?", session.ID).Error; err != nil {
					t.Fatal(err)
				}
				view, err := displaystate.Read(db.DB, *session)
				if err != nil || !view.ApprovalRequired() {
					t.Fatalf("changing preference released a pending review: %+v %v", view, err)
				}
			}
			stale := int64(0)
			if err := setSessionApprovalMode(context.Background(), db.DB, session.ID, "owner", approvalPreferenceRequest{Scope: "session", ExpectedStateVersion: &stale}); err == nil {
				t.Fatal("stale policy write accepted")
			}
			if err := setSessionApprovalMode(context.Background(), db.DB, session.ID, "other-user", approvalPreferenceRequest{Scope: "session", ExpectedStateVersion: &session.StateVersion}); err == nil {
				t.Fatal("another user changed approval policy")
			}
		})
	}
}

func TestNativeContinueAutomaticallyForThisRunDoesNotChangeFuturePolicy(t *testing.T) {
	db, _ := humanCheckpoint(t, false)
	session, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, session.CreateUserID, session.ConversationID, NativeControlCommand{SessionID: session.ID, Kind: "approve", StepID: "branch_b", PreferenceScope: "session", ExpectedStateVersion: session.StateVersion}); err != nil {
		t.Fatal(err)
	}
	session, err = GetSession(t.Context(), db.DB, session.ID)
	if err != nil || session.ApprovalMode != "auto" || session.WorkflowMode != "auto" {
		t.Fatalf("current run: %+v %v", session, err)
	}
	var count int64
	if err := db.Model(&orm.WorkflowApprovalPreference{}).Where("user_id = ? AND workflow_id = ?", session.CreateUserID, session.WorkflowID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("future policy changed: %d %v", count, err)
	}
}

func TestRestoringApprovalDoesNotReopenCompletedRun(t *testing.T) {
	db, _ := humanCheckpoint(t, true)
	// The final human-capable node actually ran automatically, so its successful
	// attempt has no review requirement or pending checkpoint.
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{
		"status": "completed", "workflow_mode": "auto", "approval_mode": "auto",
	}).Error; err != nil {
		t.Fatal(err)
	}
	session, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []bool{true, false, true} {
		if err := setSessionApprovalMode(t.Context(), db.DB, session.ID, session.CreateUserID, approvalPreferenceRequest{
			Scope: "session", ApprovalRequired: required, ExpectedStateVersion: &session.StateVersion,
		}); err != nil {
			t.Fatal(err)
		}
		session, err = GetSession(t.Context(), db.DB, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		view, err := displaystate.Read(db.DB, *session)
		if err != nil || view.Status != "completed" || view.ApprovalRequired() {
			t.Fatalf("restoring approval reopened completed run: %+v %v", view, err)
		}
	}
	var reviews int64
	if err := db.Model(&orm.WorkflowReviewCheckpoint{}).Where("session_id = ?", session.ID).Count(&reviews).Error; err != nil || reviews != 0 {
		t.Fatalf("preference change manufactured a review: %d %v", reviews, err)
	}
}

func TestFutureApprovalSettingsDoNotChangeExistingRun(t *testing.T) {
	db, _ := humanCheckpoint(t, false)
	existing, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	other, err := CreateSession(t.Context(), db.DB, CreateSessionInput{SessionID: "other-run", ConversationID: "other-chat", WorkflowID: existing.WorkflowID, WorkflowRevisionID: existing.WorkflowRevisionID, CreateUserID: existing.CreateUserID})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []bool{false, true} {
		if err := setSessionApprovalMode(t.Context(), db.DB, other.ID, other.CreateUserID, approvalPreferenceRequest{Scope: "workflow", ApprovalRequired: required, ExpectedStateVersion: &other.StateVersion}); err != nil {
			t.Fatal(err)
		}
		other, err = GetSession(t.Context(), db.DB, other.ID)
		if err != nil {
			t.Fatal(err)
		}
		untouched, err := GetSession(t.Context(), db.DB, existing.ID)
		if err != nil {
			t.Fatal(err)
		}
		view, err := projectSession(t.Context(), db.DB, untouched)
		if err != nil || !view.Display.ApprovalRequired() || !view.Projection.Nodes["branch_b"].RequiresApproval || untouched.StateVersion != existing.StateVersion {
			t.Fatalf("future default changed another run: %+v %v", view, err)
		}
		id := "future-auto"
		if required {
			id = "future-dynamic"
		}
		future, err := CreateSession(t.Context(), db.DB, CreateSessionInput{SessionID: id, ConversationID: id, WorkflowID: existing.WorkflowID, WorkflowRevisionID: existing.WorkflowRevisionID, CreateUserID: existing.CreateUserID})
		if err != nil {
			t.Fatal(err)
		}
		graph, err := loadSessionGraph(t.Context(), db.DB, future)
		if err != nil {
			t.Fatal(err)
		}
		projection := projectSessionWithApprovalPreferences(db.DB, *future, graph, graphengine.RuntimeSnapshot{})
		if projection.Nodes["branch_b"].RequiresApproval != required {
			t.Fatalf("future run did not inherit policy: %+v", projection)
		}
	}
}

func TestNativeColdStartSnapshotsSavedApprovalPolicy(t *testing.T) {
	for _, required := range []bool{false, true} {
		mode := "auto"
		if required {
			mode = "dynamic"
		}
		t.Run(mode, func(t *testing.T) {
			db, _ := setupBatchTransitionSession(t)
			var revision orm.WorkflowRevision
			if err := db.First(&revision, "id = ?", "batch-revision").Error; err != nil {
				t.Fatal(err)
			}
			var graph graphengine.CompiledStateGraph
			if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
				t.Fatal(err)
			}
			node := graph.Nodes["branch_b"]
			node.Mode = "human"
			graph.Nodes["branch_b"] = node
			if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.Conversation{ID: "cold-policy-chat", BaseModel: orm.BaseModel{CreateUserID: "batch-user"}}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowApprovalPreference{UserID: "batch-user", WorkflowID: "batch-plugin", StepID: "*", ApprovalRequired: required}).Error; err != nil {
				t.Fatal(err)
			}
			_, taskID, _, err := launchWorkflowAttempt(t.Context(), db.DB, nil, "cold-policy-chat", "history", "batch-user", "cold-policy-task", "run", "run", WorkflowStepParams{WorkflowID: "batch-plugin", RevisionID: revision.ID, StepID: "branch_b", IsColdStart: true, WorkflowMode: "dynamic"}, nil, nil, nil, nil, false, false)
			if err != nil {
				t.Fatal(err)
			}
			var step orm.WorkflowSessionStep
			if err := db.Where("task_id = ?", taskID).First(&step).Error; err != nil {
				t.Fatal(err)
			}
			if step.ReviewRequired != required {
				t.Fatalf("cold start approval=%v want=%v", step.ReviewRequired, required)
			}
			var task orm.SubAgentTask
			if err := db.First(&task, "id = ?", taskID).Error; err != nil {
				t.Fatal(err)
			}
			var params WorkflowStepParams
			if err := json.Unmarshal(task.Params, &params); err != nil {
				t.Fatal(err)
			}
			if params.WorkflowMode != mode {
				t.Fatalf("executor policy=%s want=%s", params.WorkflowMode, mode)
			}
		})
	}
}

func TestUnrememberApprovalKeepsCurrentModeAndClearsFutureOverrides(t *testing.T) {
	db, _ := humanCheckpoint(t, false)
	session, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	change := func(scope string, required bool, remember *bool) {
		t.Helper()
		if err := setSessionApprovalMode(t.Context(), db.DB, session.ID, session.CreateUserID, approvalPreferenceRequest{
			Scope: scope, ApprovalRequired: required, Remember: remember, ExpectedStateVersion: &session.StateVersion,
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.First(session, "id = ?", session.ID).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, required := range []bool{false, true, false} {
		change("workflow", required, nil)
		dto := toSessionDTO(session)
		if err := enrichApprovalPreference(db.DB, session, &dto); err != nil {
			t.Fatal(err)
		}
		if dto.SavedApprovalMode != session.ApprovalMode {
			t.Fatalf("saved mode not reflected: %+v", dto)
		}
	}
	remember := false
	change("session", false, &remember)
	if session.ApprovalMode != "auto" || session.WorkflowMode != "auto" {
		t.Fatal("clearing default changed this run")
	}
	dto := toSessionDTO(session)
	if err := enrichApprovalPreference(db.DB, session, &dto); err != nil || dto.SavedApprovalMode != "" {
		t.Fatalf("remember still set: %+v %v", dto, err)
	}
	future, err := CreateSession(t.Context(), db.DB, CreateSessionInput{SessionID: "future-default", ConversationID: "future-chat", WorkflowID: session.WorkflowID, CreateUserID: session.CreateUserID})
	if err != nil || future.WorkflowMode != "dynamic" || len(future.ApprovalPreferences) != 0 {
		t.Fatalf("future default: %+v %v", future, err)
	}
}

func TestApplyAutomaticSettingsThenAdmitNativeContinuation(t *testing.T) {
	db, _ := humanCheckpoint(t, false)
	session, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := setSessionApprovalMode(t.Context(), db.DB, session.ID, session.CreateUserID, approvalPreferenceRequest{
		Scope: "workflow", ApprovalRequired: false, ExpectedStateVersion: &session.StateVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, session.CreateUserID, session.ConversationID, NativeControlCommand{
		SessionID: session.ID, Kind: "approve", StepID: "branch_b", ExpectedStateVersion: session.StateVersion + 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(session, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db.DB, *session)
	if err != nil || view.ApprovalRequired() || session.ApprovalMode != "auto" {
		t.Fatalf("checkpoint not released: %+v %v", view, err)
	}
	var pending int64
	if err := db.Model(&orm.WorkflowReviewCheckpoint{}).Where("session_id = ? AND status = ?", session.ID, "pending").Count(&pending).Error; err != nil || pending != 0 {
		t.Fatalf("pending reviews: %d %v", pending, err)
	}
}

func TestBottomRememberedContinuationIsReflectedInApprovalMenu(t *testing.T) {
	db, _ := humanCheckpoint(t, false)
	session, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, session.CreateUserID, session.ConversationID, NativeControlCommand{
		SessionID: session.ID, Kind: "approve", StepID: "branch_b", PreferenceScope: "following", ExpectedStateVersion: session.StateVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(session, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	dto := toSessionDTO(session)
	if err := enrichApprovalPreference(db.DB, session, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.ApprovalMode != "auto" || dto.SavedApprovalMode != "auto" {
		t.Fatalf("bottom action not reflected: %+v", dto)
	}
}
