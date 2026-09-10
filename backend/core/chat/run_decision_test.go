package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
	"lazymind/core/state"
	corestore "lazymind/core/store"
	"lazymind/core/subagent"
)

func newRunDecisionTestStore(t *testing.T) state.Store {
	t.Helper()
	store, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestRunDecisionUserCancelWinsWhileRunIsActive(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1")
	if err != nil || !won {
		t.Fatalf("claim cancellation: won=%v err=%v", won, err)
	}

	terminal := resolveRunTerminal(ctx, store, "conv", "history", "run-1", &RunTerminal{
		Status: "failed", Reason: "runtime_failure", Code: "upstream_stream_failed",
		PartialOutput: true,
	}, "upstream_terminal")

	if terminal.Status != "cancelled" || terminal.Reason != "user_cancelled" || !terminal.PartialOutput || terminal.Code != "" {
		t.Fatalf("unexpected cancellation terminal: %#v", terminal)
	}
}

func TestRunDecisionAcceptedFailureIsNotOverwrittenByStop(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	failure := &RunTerminal{
		Status: "failed", Reason: "model_failure", Code: "rate_limited",
		PartialOutput: false,
	}
	accepted := resolveRunTerminal(
		ctx, store, "conv", "history", "run-1", failure, "upstream_terminal",
	)
	if accepted.Status != "failed" || accepted.Reason != "model_failure" {
		t.Fatalf("unexpected accepted terminal: %#v", accepted)
	}

	won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1")
	if err != nil {
		t.Fatalf("claim late cancellation: %v", err)
	}
	if won {
		t.Fatal("late cancellation overwrote accepted failure")
	}

	resolved := resolveRunTerminal(ctx, store, "conv", "history", "run-1", &RunTerminal{
		Status: "cancelled", Reason: "user_cancelled", PartialOutput: true,
	}, "late_terminal")
	if *resolved != *failure {
		t.Fatalf("accepted failure changed: got %#v want %#v", resolved, failure)
	}
}

func TestRunDecisionRepeatedUserCancelIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	for attempt := 0; attempt < 2; attempt++ {
		won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1")
		if err != nil || !won {
			t.Fatalf("claim cancellation attempt %d: won=%v err=%v", attempt+1, won, err)
		}
	}

	terminal := resolveRunTerminal(ctx, store, "conv", "history", "run-1", &RunTerminal{
		Status: "completed", Reason: "normal", PartialOutput: true,
	}, "late_terminal")
	if terminal.Status != "cancelled" || terminal.Reason != "user_cancelled" {
		t.Fatalf("repeated cancellation changed the winning decision: %#v", terminal)
	}
}

func TestRunDecisionIsIsolatedByRunID(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	if won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1"); err != nil || !won {
		t.Fatalf("claim first run cancellation: won=%v err=%v", won, err)
	}

	second := resolveRunTerminal(ctx, store, "conv", "history", "run-2", &RunTerminal{
		Status: "failed", Reason: "runtime_failure", Code: "upstream_stream_failed",
		PartialOutput: false,
	}, "upstream_terminal")
	if second.Status != "failed" || second.Reason != "runtime_failure" {
		t.Fatalf("first run cancellation leaked into second run: %#v", second)
	}
}

func TestResolveRuntimeChunkDecisionRewritesEventToWinningCancellation(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	if won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1"); err != nil || !won {
		t.Fatalf("claim cancellation: won=%v err=%v", won, err)
	}
	candidateEvent := failedRunEvent("run-1", "upstream_stream_failed", true)
	candidate, _ := candidateEvent.Terminal()

	decision := resolveRuntimeChunkDecision(
		ctx, store, "conv", "history", "run-1",
		runtimeChunkDecision{Event: candidateEvent, Terminal: candidate, Stop: true}, true, true,
	)
	terminal, err := decision.Event.Terminal()
	if err != nil {
		t.Fatalf("parse resolved event: %v", err)
	}
	if terminal.Status != "cancelled" || decision.Terminal.Status != "cancelled" || !decision.Stop {
		t.Fatalf("unexpected resolved decision: %#v terminal=%#v", decision, terminal)
	}
}

func TestExternalRuntimeChunkUsesDurableTerminalWithoutSharedDecision(t *testing.T) {
	ctx := context.Background()
	stateStore := newRunDecisionTestStore(t)
	if cancelIsWinner, err := claimUserCancelDecision(ctx, stateStore, "conv", "history", "external-run"); err != nil || !cancelIsWinner {
		t.Fatalf("seed unrelated shared cancellation: winner=%v err=%v", cancelIsWinner, err)
	}
	event := failedRunEvent("external-run", "external_agent_failed", false)
	terminal, _ := event.Terminal()
	decision := resolveRuntimeChunkDecision(
		ctx, stateStore, "conv", "history", "external-run",
		runtimeChunkDecision{Event: event, Terminal: terminal, Stop: true}, false, false,
	)
	if decision.Terminal.Status != "failed" || decision.Terminal.Reason != "runtime_failure" {
		t.Fatalf("shared decision changed external terminal: %#v", decision.Terminal)
	}
}

func TestExternalTerminalProjectionIgnoresSharedRunDecision(t *testing.T) {
	ctx := context.Background()
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-external-decision")
	job, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim external run: job=%#v err=%v", job, err)
	}
	if _, err := app.appendEvent(
		ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "failed-1", Type: "failed", Error: "provider failed"},
	); err != nil {
		t.Fatalf("append external failure: %v", err)
	}

	store := newRunDecisionTestStore(t)
	if won, err := claimUserCancelDecision(
		ctx, store, "conversation-1", "history-run-external-decision", job.RunID,
	); err != nil || !won {
		t.Fatalf("claim cancellation: won=%v err=%v", won, err)
	}
	if err := projectExternalChatRunCache(ctx, db, store, "user-1", job.RunID); err != nil {
		t.Fatalf("project external terminal: %v", err)
	}

	var history orm.ChatHistory
	if err := db.Where("id = ?", "history-run-external-decision").Take(&history).Error; err != nil {
		t.Fatalf("load projected history: %v", err)
	}
	var terminal RunTerminal
	if err := json.Unmarshal(history.RunTerminal, &terminal); err != nil {
		t.Fatalf("decode projected terminal: %v", err)
	}
	if history.RunStatus != "failed" || terminal.Status != "failed" || terminal.Reason != "runtime_failure" {
		t.Fatalf("external history did not preserve durable terminal: history=%#v terminal=%#v", history, terminal)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close cache store: %v", err)
	}
	if err := projectExternalChatRunCache(ctx, db, store, "user-1", job.RunID); err == nil {
		t.Fatal("expected closed cache projection to fail")
	}
	var durable orm.ChatHistory
	if err := db.Where("id = ?", history.ID).Take(&durable).Error; err != nil {
		t.Fatalf("reload durable history: %v", err)
	}
	if durable.RunStatus != "failed" || string(durable.RunTerminal) != string(history.RunTerminal) {
		t.Fatalf("cache failure changed durable terminal: before=%#v after=%#v", history, durable)
	}
}

func TestRunDecisionConcurrentCandidatesHaveOneWinner(t *testing.T) {
	assertConcurrentRunDecisionWinner(t, newRunDecisionTestStore(t), "sqlite-concurrent")
}

func assertConcurrentRunDecisionWinner(t *testing.T, store state.Store, runID string) {
	t.Helper()
	ctx := context.Background()
	candidates := []runDecision{
		{Kind: runDecisionUserCancel, Source: "user_stop"},
		{Kind: runDecisionTerminal, Source: "model", Terminal: &RunTerminal{Status: "failed", Reason: "model_failure", Code: "rate_limited"}},
		{Kind: runDecisionTerminal, Source: "runtime", Terminal: &RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "transport_error"}},
	}
	start := make(chan struct{})
	accepted := make(chan bool, len(candidates))
	errs := make(chan error, len(candidates))
	var wait sync.WaitGroup
	for _, candidate := range candidates {
		candidate := candidate
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, won, err := claimRunDecision(ctx, store, "conv", "history", runID, candidate)
			accepted <- won
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(accepted)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent decision: %v", err)
		}
	}
	winners := 0
	for won := range accepted {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("accepted winners=%d, want 1", winners)
	}
	payload, err := store.Get(ctx, runDecisionKey("conv", "history", runID))
	if err != nil {
		t.Fatalf("load winner: %v", err)
	}
	var winner runDecision
	if err := json.Unmarshal(payload, &winner); err != nil {
		t.Fatalf("decode winner: %v", err)
	}
	if winner.Kind != runDecisionUserCancel && winner.Kind != runDecisionTerminal {
		t.Fatalf("invalid winner: %#v", winner)
	}
}

func TestRunDecisionTTLRejectsLateCandidatesForOneDay(t *testing.T) {
	if runDecisionTTL != 24*time.Hour {
		t.Fatalf("runDecisionTTL=%v, want 24h", runDecisionTTL)
	}
}

// This fixture installs the production dispatch callback with real DB/state.
// It deliberately creates no ChatHistory: fresh runs authorize before history.
func workspaceIdentityFixture(t *testing.T) (*gorm.DB, state.Store, localworkspace.OperationRequest) {
	t.Helper()
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	// TEST_DB_DRIVER/TEST_DB_DSN already select an isolated PostgreSQL schema.
	// An explicitly supplied Redis URL selects a disposable integration server;
	// unique conversation IDs avoid clearing or depending on existing Redis keys.
	var stateStore state.Store
	if raw := os.Getenv("TEST_WORKSPACE_REDIS_URL"); raw != "" {
		redisStore, err := state.NewRedisStoreFromURL(raw)
		if err != nil {
			t.Fatal(err)
		}
		stateStore = redisStore
		t.Cleanup(func() { _ = stateStore.Close() })
	} else {
		stateStore = newRunDecisionTestStore(t)
	}
	conversationID := fmt.Sprintf("identity-%d", time.Now().UnixNano())
	corestore.Init(db.DB, nil, stateStore)
	localworkspace.SetValidateOperationRunFunc(func(ctx context.Context, db *gorm.DB, ss state.Store, request localworkspace.OperationRequest) error {
		if request.TaskID != "" {
			return subagent.ValidateWorkspaceRun(ctx, db, ss, request)
		}
		return ValidateWorkspaceRun(ctx, ss, request)
	})
	t.Cleanup(func() { corestore.Init(nil, nil, nil); localworkspace.SetValidateOperationRunFunc(nil) })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "read.txt"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	grant, err := localworkspace.Register(t.Context(), db.DB, "owner", localworkspace.RegisterInput{DisplayName: "project", CanonicalPath: root, Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Conversation{ID: conversationID, IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, PermissionMode: localworkspace.PermissionAllowAll, PermissionVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	return db.DB, stateStore, localworkspace.OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: localworkspace.OperationRead, Path: "read.txt", CallID: fmt.Sprintf("%d/call", time.Now().UnixMilli())}
}

func TestWorkspaceChatEntrypointsRegisterAndFinishRuns(t *testing.T) {
	for _, mode := range []string{"nonstream", "stream", "dual"} {
		t.Run(mode, func(t *testing.T) {
			db, ss, base := workspaceIdentityFixture(t)
			mockEmptyChatScan(t)
			seedAvailableChatModel(t, db, base.UserID, "entry-provider", "entry-group", "entry-model", "Test", "Test", "test-chat", "llm", true, "test-key")
			seedSelectedChatModel(t, db, base.UserID, "entry-model", false)
			if err := db.Model(&orm.Conversation{}).Where("id = ?", base.ConversationID).Update("chat_model_mode", chatModelModeAuto).Error; err != nil {
				t.Fatal(err)
			}
			type observedRun struct {
				request                localworkspace.OperationRequest
				completedID, pendingID string
			}
			observed := make(chan observedRun, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				identity := request.Conversation
				if identity.RunID == "" || identity.HistoryID == "" || identity.ConversationID != base.ConversationID || identity.UserID != base.UserID {
					t.Errorf("upstream missing Core identity: %+v", identity)
					return
				}
				req := base
				req.HistoryID, req.RunID = identity.HistoryID, identity.RunID
				req.CallID = fmt.Sprintf("%d/active-%s", time.Now().UnixMilli(), identity.RunID)
				prepared, err := localworkspace.PrepareOperation(r.Context(), db, ss, req)
				if err != nil {
					t.Errorf("upstream received an unregistered run: %v", err)
					return
				}
				result, err := localworkspace.ExecuteOperation(r.Context(), db, ss, prepared.OperationID, req)
				if err != nil || result.Content != "content" {
					t.Errorf("active upstream workspace read: %+v, %v", result, err)
					return
				}
				req.CallID += "-pending"
				pending, err := localworkspace.PrepareOperation(r.Context(), db, ss, req)
				if err != nil {
					t.Error(err)
					return
				}
				observed <- observedRun{request: req, completedID: prepared.OperationID, pendingID: pending.OperationID}
				w.Header().Set("Content-Type", "text/event-stream")
				encoder := json.NewEncoder(w)
				_ = encoder.Encode(map[string]any{"code": 200, "msg": "success", "data": map[string]any{"think": nil, "text": "answer", "sources": []any{}}, "cost": 0})
				_ = encoder.Encode(map[string]any{"code": 200, "msg": "success", "data": map[string]any{"think": nil, "text": nil, "sources": []any{}, "runtime_event": runFinishedEvent(identity.RunID, RunTerminal{Status: "completed", Reason: "normal", PartialOutput: true})}, "cost": 0})
			}))
			defer server.Close()
			body := map[string]any{"conversation_id": base.ConversationID, "user_id": base.UserID, "query": "hello"}
			if err := applyConversationChatModelConfig(t.Context(), db, base.UserID, body); err != nil {
				t.Fatal(err)
			}
			ext := mergeChatModelRouteIntoExt(nil, body)
			target := chatPersistTarget{Seq: 1, HistoryID: "entry-history"}
			recorder := httptest.NewRecorder()
			if mode == "nonstream" {
				handleNonStreamChat(recorder, t.Context(), db, ss, server.URL, body, base.ConversationID, "hello", target, ext)
			} else {
				r := httptest.NewRequest(http.MethodPost, "/chat", nil).WithContext(t.Context())
				handleStreamChat(recorder, r, db, ss, server.URL, body, base.ConversationID, "hello", target, mode == "dual", ext)
			}
			wantRuns := 1
			if mode == "dual" {
				wantRuns = 2
			}
			if recorder.Code != http.StatusOK || len(observed) != wantRuns {
				t.Fatalf("entrypoint status=%d observed=%d want=%d response=%s", recorder.Code, len(observed), wantRuns, recorder.Body.String())
			}
			seenHistories := map[string]bool{}
			for range wantRuns {
				run := <-observed
				req := run.request
				if seenHistories[req.HistoryID] {
					t.Fatalf("dual reply reused identity: %+v", req)
				}
				seenHistories[req.HistoryID] = true
				status, err := getChatStatus(t.Context(), ss, req.ConversationID, req.HistoryID)
				if err != nil || status.Status != "completed" || status.RunID != req.RunID {
					t.Fatalf("finished run status=%+v, err=%v", status, err)
				}
				if _, err := localworkspace.ExecuteOperation(t.Context(), db, ss, run.pendingID, req); !workspaceConflict(err) {
					t.Fatalf("finished run executed prepared operation: %v", err)
				}
				req.CallID = strings.TrimSuffix(req.CallID, "-pending")
				if _, err := localworkspace.ExecuteOperation(t.Context(), db, ss, run.completedID, req); !workspaceConflict(err) {
					t.Fatalf("finished run replayed completed operation: %v", err)
				}
				req.CallID += "-after-finish"
				if _, err := localworkspace.PrepareOperation(t.Context(), db, ss, req); !workspaceConflict(err) {
					t.Fatalf("finished run prepared new operation: %v", err)
				}
			}
		})
	}
}

func TestWorkspaceMainIdentityRequiresRegisteredLiveRun(t *testing.T) {
	db, ss, req := workspaceIdentityFixture(t)
	req.HistoryID, req.RunID = "fresh-history", "registered-run"
	if _, err := localworkspace.PrepareOperation(t.Context(), db, ss, req); err == nil {
		t.Fatal("unregistered run accepted")
	}
	if err := setChatRuntimeStatus(t.Context(), ss, req.ConversationID, req.HistoryID, "generating", "", req.RunID, nil); err != nil {
		t.Fatal(err)
	}
	prepared, err := localworkspace.PrepareOperation(t.Context(), db, ss, req)
	if err != nil {
		t.Fatalf("fresh registered run: %v", err)
	}
	if _, err := localworkspace.ExecuteOperation(t.Context(), db, ss, prepared.OperationID, req); err != nil {
		t.Fatal(err)
	}
	for i, change := range []func(*localworkspace.OperationRequest){
		func(r *localworkspace.OperationRequest) { r.RunID = "forged" },
		func(r *localworkspace.OperationRequest) { r.HistoryID = "other-history" },
		func(r *localworkspace.OperationRequest) { r.UserID = "other-owner" },
		func(r *localworkspace.OperationRequest) { r.ConversationID = "other-conversation" },
	} {
		bad := req
		bad.CallID = fmt.Sprintf("%d/identity-%d", time.Now().UnixMilli(), i)
		change(&bad)
		if _, err := localworkspace.PrepareOperation(t.Context(), db, ss, bad); err == nil {
			t.Fatalf("identity mismatch %d accepted", i)
		}
	}
	if won, err := claimUserCancelDecision(t.Context(), ss, req.ConversationID, req.HistoryID, req.RunID); err != nil || !won {
		t.Fatalf("cancel: %v %v", won, err)
	}
	req.CallID = fmt.Sprintf("%d/after-cancel", time.Now().UnixMilli())
	if _, err := localworkspace.PrepareOperation(t.Context(), db, ss, req); err == nil {
		t.Fatal("cancelled run accepted")
	}
	req.RunID = "next-run"
	if err := setChatRuntimeStatus(t.Context(), ss, req.ConversationID, req.HistoryID, "generating", "", req.RunID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := localworkspace.PrepareOperation(t.Context(), db, ss, req); err != nil {
		t.Fatalf("next run denied: %v", err)
	}
	finishRegisteredChatRun(t.Context(), ss, req.ConversationID, req.HistoryID, req.RunID)
	req.CallID = fmt.Sprintf("%d/after-finish", time.Now().UnixMilli())
	if _, err := localworkspace.PrepareOperation(t.Context(), db, ss, req); err == nil {
		t.Fatal("finished run accepted")
	}
	localworkspace.SetValidateOperationRunFunc(nil)
	if _, err := localworkspace.PrepareOperation(t.Context(), db, ss, req); err == nil {
		t.Fatal("missing validator accepted")
	}
}

func TestWorkspaceWorkflowIdentityRequiresCurrentOwnedLease(t *testing.T) {
	db, ss, req := workspaceIdentityFixture(t)
	expires := time.Now().UTC().Add(time.Hour)
	task := orm.SubAgentTask{ID: "workflow-task", ConversationID: req.ConversationID, CreateUserID: req.UserID, AgentType: "workflow_step", Status: "running", Mode: "auto", InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)}
	revision := orm.WorkflowRevision{ID: "workspace-revision", CompiledGraph: json.RawMessage(`{"nodes":{"step":{"legacy_tools":["local_fs"]}}}`)}
	session := orm.WorkflowSession{WorkflowRevisionID: revision.ID, ID: "workflow-session", ConversationID: req.ConversationID, CreateUserID: req.UserID, Status: "active"}
	step := orm.WorkflowSessionStep{ID: "workflow-attempt", SessionID: session.ID, TaskID: task.ID, StepID: "step", Status: "running", Validity: "effective", FencingGeneration: 2, LeaseToken: "current-lease", LeaseExpiresAt: &expires}
	for _, row := range []any{&revision, &task, &session, &step} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req.TaskID, req.AttemptID, req.Generation, req.LeaseToken = task.ID, step.ID, "2", step.LeaseToken
	prepared, err := localworkspace.PrepareOperation(t.Context(), db, ss, req)
	if err != nil {
		t.Fatalf("live workflow lease: %v", err)
	}
	if _, err := localworkspace.ExecuteOperation(t.Context(), db, ss, prepared.OperationID, req); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name             string
		model            any
		id, column       string
		invalid, restore any
	}{
		{"tool undeclared", &orm.WorkflowRevision{}, revision.ID, "compiled_graph", json.RawMessage(`{"nodes":{"step":{"legacy_tools":[]}}}`), revision.CompiledGraph},
		{"lease replaced", &orm.WorkflowSessionStep{}, step.ID, "lease_token", "new-lease", step.LeaseToken},
		{"generation replaced", &orm.WorkflowSessionStep{}, step.ID, "fencing_generation", 3, 2},
		{"lease expired", &orm.WorkflowSessionStep{}, step.ID, "lease_expires_at", time.Now().UTC().Add(-time.Second), expires},
		{"attempt terminal", &orm.WorkflowSessionStep{}, step.ID, "status", "succeeded", "running"},
		{"attempt stale", &orm.WorkflowSessionStep{}, step.ID, "validity", "stale", "effective"},
		{"task interrupted", &orm.SubAgentTask{}, task.ID, "status", "interrupted", "running"},
		{"task other owner", &orm.SubAgentTask{}, task.ID, "create_user_id", "other", req.UserID},
		{"session dismissed", &orm.WorkflowSession{}, session.ID, "dismissed", true, false},
		{"session other conversation", &orm.WorkflowSession{}, session.ID, "conversation_id", "other", req.ConversationID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.Model(tc.model).Where("id = ?", tc.id).UpdateColumn(tc.column, tc.invalid).Error; err != nil {
				t.Fatal(err)
			}
			defer db.Model(tc.model).Where("id = ?", tc.id).UpdateColumn(tc.column, tc.restore)
			bad := req
			bad.CallID = fmt.Sprintf("%d/%s", time.Now().UnixMilli(), tc.name)
			if _, err := localworkspace.PrepareOperation(t.Context(), db, ss, bad); err == nil {
				t.Fatal("invalid workflow identity accepted")
			}
		})
	}
}

// Reproduce against dedicated disposable backends with TEST_DB_DRIVER=postgres,
// TEST_DB_DSN and TEST_WORKSPACE_REDIS_URL. No Redis database is flushed by tests.
func workspaceConflict(err error) bool {
	var app *common.AppError
	return errors.As(err, &app) && app.HTTPStatus == http.StatusConflict
}

func TestWorkspaceBackendIntegration(t *testing.T) {
	if os.Getenv("TEST_DB_DRIVER") != "postgres" && os.Getenv("TEST_WORKSPACE_REDIS_URL") == "" {
		t.Skip("requires an explicit PostgreSQL or Redis integration backend")
	}
	setup := func(t *testing.T) (*gorm.DB, state.Store, localworkspace.OperationRequest, string) {
		db, ss, req := workspaceIdentityFixture(t)
		req.HistoryID, req.RunID = "backend-history", "backend-run"
		if err := setChatRuntimeStatus(t.Context(), ss, req.ConversationID, req.HistoryID, "generating", "", req.RunID, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", req.ConversationID).Update("permission_mode", localworkspace.PermissionAlwaysAsk).Error; err != nil {
			t.Fatal(err)
		}
		var workspace orm.LocalWorkspace
		if err := db.Where("id = ?", req.WorkspaceID).First(&workspace).Error; err != nil {
			t.Fatal(err)
		}
		return db, ss, req, workspace.CanonicalPath
	}
	t.Run("concurrent prepare approve execute", func(t *testing.T) {
		db, ss, req, root := setup(t)
		req.Operation, req.Content = localworkspace.OperationAppend, "!"
		// Hash the exact observed content rather than inventing a file version.
		req.ExpectedVersion = fmt.Sprintf("%x", sha256.Sum256([]byte("content")))
		results := make(chan localworkspace.OperationResult, 12)
		errors := make(chan error, 12)
		var workers sync.WaitGroup
		for i := 0; i < 12; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				result, err := localworkspace.PrepareOperation(t.Context(), db, ss, req)
				results <- result
				errors <- err
			}()
		}
		workers.Wait()
		close(results)
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		id := ""
		for result := range results {
			if id == "" {
				id = result.OperationID
			}
			if result.OperationID != id {
				t.Fatal("concurrent prepare created different operations")
			}
		}
		ready, err := localworkspace.PrepareOperation(t.Context(), db, ss, req)
		if err != nil || ready.Status != "pending" {
			t.Fatalf("prepare: %+v %v", ready, err)
		}
		if _, err := localworkspace.DecideOperation(t.Context(), db, ss, id, "allow_once", "other-owner"); err == nil {
			t.Fatal("cross-owner approval accepted")
		}
		decisions := make(chan error, 12)
		for i := 0; i < 12; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, err := localworkspace.DecideOperation(t.Context(), db, ss, id, "allow_once", req.UserID)
				decisions <- err
			}()
		}
		workers.Wait()
		close(decisions)
		winners := 0
		for err := range decisions {
			if err != nil && !workspaceConflict(err) {
				t.Fatalf("concurrent approval: %v", err)
			}
			if err == nil {
				winners++
			}
		}
		if winners != 1 {
			t.Fatalf("approval winners=%d", winners)
		}
		completed := make(chan localworkspace.OperationResult, 12)
		executions := make(chan error, 12)
		for i := 0; i < 12; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				result, err := localworkspace.ExecuteOperation(t.Context(), db, ss, id, req)
				completed <- result
				executions <- err
			}()
		}
		workers.Wait()
		close(completed)
		close(executions)
		for err := range executions {
			if err != nil && !workspaceConflict(err) {
				t.Fatalf("concurrent execution: %v", err)
			}
		}
		winners = 0
		for result := range completed {
			if result.Status == "completed" {
				winners++
			}
		}
		data, err := os.ReadFile(filepath.Join(root, req.Path))
		if err != nil || string(data) != "content!" || winners < 1 {
			t.Fatalf("append=%q completed=%d err=%v", data, winners, err)
		}
		if _, err := localworkspace.ExecuteOperation(t.Context(), db, ss, id, req); err != nil {
			t.Fatal(err)
		}
		data, _ = os.ReadFile(filepath.Join(root, req.Path))
		if string(data) != "content!" {
			t.Fatalf("receipt retry replayed append: %q", data)
		}
	})
	t.Run("shared approval capacity", func(t *testing.T) {
		db, ss, base, _ := setup(t)
		base.Operation, base.Path, base.Content = localworkspace.OperationCreate, "new.txt", "new"
		results := make(chan localworkspace.OperationResult, 24)
		errors := make(chan error, 24)
		var workers sync.WaitGroup
		for i := 0; i < 24; i++ {
			workers.Add(1)
			go func(i int) {
				defer workers.Done()
				req := base
				req.CallID = fmt.Sprintf("%d/capacity-%d", time.Now().UnixMilli(), i)
				result, err := localworkspace.PrepareOperation(t.Context(), db, ss, req)
				results <- result
				errors <- err
			}(i)
		}
		workers.Wait()
		close(results)
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		pending, full := 0, 0
		for result := range results {
			if result.Status == "pending" {
				pending++
			}
			if result.Reason == "approval_capacity" {
				full++
			}
		}
		if pending != 16 || full != 8 {
			t.Fatalf("pending=%d full=%d", pending, full)
		}
	})
	t.Run("revoke fences approved run", func(t *testing.T) {
		db, ss, req, root := setup(t)
		notifications := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		t.Cleanup(notifications.Close)
		t.Setenv("LAZYMIND_CHAT_SERVICE_URL", notifications.URL)
		localworkspace.SetStopConversationFunc(func(ctx context.Context, owner, conversation string) error {
			return StopConversationExecution(ctx, db, ss, owner, conversation, "", "stopped by user")
		})
		t.Cleanup(func() { localworkspace.SetStopConversationFunc(nil) })
		req.Operation, req.Path, req.Content = localworkspace.OperationCreate, "revoked.txt", "never"
		prepared, err := localworkspace.PrepareOperation(t.Context(), db, ss, req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := localworkspace.DecideOperation(t.Context(), db, ss, prepared.OperationID, "allow_once", req.UserID); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/local-workspaces/"+req.WorkspaceID+":revoke", strings.NewReader(`{"version":1}`))
		r.Header.Set("X-User-Id", req.UserID)
		r = mux.SetURLVars(r, map[string]string{"workspace_id": req.WorkspaceID})
		w := httptest.NewRecorder()
		localworkspace.Revoke(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"stop_failed_count":0`) {
			t.Fatalf("revoke=%d %s", w.Code, w.Body.String())
		}
		if _, err := localworkspace.ExecuteOperation(t.Context(), db, ss, prepared.OperationID, req); err == nil {
			t.Fatal("approved operation survived revoke")
		}
		if _, err := os.Stat(filepath.Join(root, req.Path)); !os.IsNotExist(err) {
			t.Fatalf("revoked file exists: %v", err)
		}
		if err := ValidateWorkspaceRun(t.Context(), ss, req); err == nil {
			t.Fatal("run survived explicit revoke cancellation")
		}
	})
}

const workspaceCompletedCrashExit = 73

type workspaceExecutionWorkerSpec struct {
	Request                                           localworkspace.OperationRequest
	OperationID, Driver, StatePath, SyncDir, WorkerID string
	CrashBeforeCompleted                              bool
}

// Process tests share only this test's temporary SQLite files or isolated
// PostgreSQL schema and uniquely named Redis keys. The ready/start files keep
// connection setup out of the race and bound a stuck worker by one deadline.
func TestWorkspaceExecutionProcesses(t *testing.T) {
	for _, test := range []struct {
		name    string
		workers int
		crash   bool
	}{
		{name: "six workers consume once", workers: 6},
		{name: "crash before completed receipt", workers: 1, crash: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, ss, req := workspaceIdentityFixture(t)
			spec := workspaceExecutionWorkerSpec{Driver: db.Dialector.Name(), SyncDir: t.TempDir(), CrashBeforeCompleted: test.crash}
			if os.Getenv("TEST_WORKSPACE_REDIS_URL") == "" {
				spec.StatePath = filepath.Join(t.TempDir(), "shared-state.db")
				shared, err := state.NewSQLiteStore(spec.StatePath)
				if err != nil {
					t.Fatal(err)
				}
				ss = shared
				t.Cleanup(func() { _ = shared.Close() })
				corestore.Init(db, nil, ss)
			}
			var dsn string
			if spec.Driver == orm.DriverPostgres {
				var schema string
				if err := db.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
					t.Fatal(err)
				}
				u, err := url.Parse(os.Getenv("TEST_DB_DSN"))
				if err != nil {
					t.Fatal(err)
				}
				q := u.Query()
				q.Set("search_path", schema)
				u.RawQuery = q.Encode()
				dsn = u.String()
			} else {
				var databases []struct{ Name, File string }
				if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
					t.Fatal(err)
				}
				for _, database := range databases {
					if database.Name == "main" {
						dsn = database.File
					}
				}
				if dsn == "" {
					t.Fatal("missing temporary SQLite database path")
				}
			}
			req.HistoryID, req.RunID = "process-history", "process-run"
			if err := setChatRuntimeStatus(t.Context(), ss, req.ConversationID, req.HistoryID, "generating", "", req.RunID, nil); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", req.ConversationID).Update("permission_mode", localworkspace.PermissionAlwaysAsk).Error; err != nil {
				t.Fatal(err)
			}
			req.Operation, req.Content, req.ExpectedVersion = localworkspace.OperationAppend, "!", fmt.Sprintf("%x", sha256.Sum256([]byte("content")))
			prepared, err := localworkspace.PrepareOperation(t.Context(), db, ss, req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := localworkspace.DecideOperation(t.Context(), db, ss, prepared.OperationID, "allow_once", req.UserID); err != nil {
				t.Fatal(err)
			}
			spec.Request, spec.OperationID = req, prepared.OperationID
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			type outcome struct {
				output []byte
				err    error
			}
			outcomes := make(chan outcome, test.workers)
			for i := 0; i < test.workers; i++ {
				spec.WorkerID = fmt.Sprint(i)
				encoded, err := json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}
				command := exec.CommandContext(ctx, binary, "-test.run=^TestWorkspaceBackendExecutionWorker$", "-test.timeout=25s")
				command.Env = append(os.Environ(), "TEST_WORKSPACE_WORKER_SPEC="+string(encoded), "TEST_WORKSPACE_WORKER_DSN="+dsn)
				go func() {
					output, err := command.CombinedOutput()
					outcomes <- outcome{output: output, err: err}
				}()
			}
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for ready := 0; ready < test.workers; {
				if _, err := os.Stat(filepath.Join(spec.SyncDir, fmt.Sprintf("ready-%d", ready))); err == nil {
					ready++
					continue
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				select {
				case <-ctx.Done():
					t.Fatalf("workers did not reach start barrier: %v", ctx.Err())
				case result := <-outcomes:
					t.Fatalf("worker exited before start: %v: %s", result.err, result.output)
				case <-ticker.C:
				}
			}
			if err := os.WriteFile(filepath.Join(spec.SyncDir, "start"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < test.workers; i++ {
				select {
				case <-ctx.Done():
					t.Fatalf("workers exceeded deadline: %v", ctx.Err())
				case result := <-outcomes:
					if test.crash {
						var exit *exec.ExitError
						if !errors.As(result.err, &exit) || exit.ExitCode() != workspaceCompletedCrashExit {
							t.Fatalf("crash worker exit=%v, want %d: %s", result.err, workspaceCompletedCrashExit, result.output)
						}
					} else if result.err != nil {
						t.Fatalf("worker failed: %v: %s", result.err, result.output)
					}
				}
			}
			var workspace orm.LocalWorkspace
			if err := db.Where("id = ?", req.WorkspaceID).First(&workspace).Error; err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(workspace.CanonicalPath, req.Path)
			if data, err := os.ReadFile(path); err != nil || string(data) != "content!" {
				t.Fatalf("multiprocess append=%q err=%v", data, err)
			}
			want := "content!"
			if test.crash {
				// Restore the test file's observed version so version conflicts cannot
				// hide a broken one-shot claim or execution-state check on replay.
				want = "content"
				if err := os.WriteFile(path, []byte(want), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := localworkspace.ExecuteOperation(t.Context(), db, ss, prepared.OperationID, req)
				if test.crash {
					if !workspaceConflict(err) {
						t.Fatalf("crashed operation replay: result=%+v err=%v", result, err)
					}
				} else if err != nil || result.Status != "completed" {
					t.Fatalf("completed receipt=%+v err=%v", result, err)
				}
			}
			again, err := localworkspace.PrepareOperation(t.Context(), db, ss, req)
			if err != nil || again.OperationID != prepared.OperationID {
				t.Fatalf("same call issued a new operation: %+v err=%v", again, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != want {
				t.Fatalf("replay changed append=%q err=%v", data, err)
			}
		})
	}
}

type crashBeforeWorkspaceCompletedStore struct{ state.Store }

func (s crashBeforeWorkspaceCompletedStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	var operation struct {
		Status string `json:"status"`
	}
	if strings.HasPrefix(key, "local-workspace-operation:") && json.Unmarshal(value, &operation) == nil && operation.Status == "completed" {
		os.Exit(workspaceCompletedCrashExit)
	}
	return s.Store.Set(ctx, key, value, ttl)
}

func TestWorkspaceBackendExecutionWorker(t *testing.T) {
	raw := os.Getenv("TEST_WORKSPACE_WORKER_SPEC")
	if raw == "" {
		t.Skip("subprocess helper")
	}
	var spec workspaceExecutionWorkerSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatal(err)
	}
	db, err := orm.Connect(spec.Driver, os.Getenv("TEST_WORKSPACE_WORKER_DSN"))
	if err != nil {
		t.Fatal("worker database unavailable")
	}
	sqlDB, _ := db.DB.DB()
	defer sqlDB.Close()
	var ss state.Store
	if spec.StatePath != "" {
		ss, err = state.NewSQLiteStore(spec.StatePath)
	} else {
		ss, err = state.NewRedisStoreFromURL(os.Getenv("TEST_WORKSPACE_REDIS_URL"))
	}
	if err != nil {
		t.Fatal("worker state unavailable")
	}
	defer ss.Close()
	if spec.CrashBeforeCompleted {
		ss = crashBeforeWorkspaceCompletedStore{Store: ss}
	}
	corestore.Init(db.DB, nil, ss)
	localworkspace.SetValidateOperationRunFunc(func(ctx context.Context, _ *gorm.DB, ss state.Store, req localworkspace.OperationRequest) error {
		return ValidateWorkspaceRun(ctx, ss, req)
	})
	if err := os.WriteFile(filepath.Join(spec.SyncDir, "ready-"+spec.WorkerID), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(spec.SyncDir, "start")); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("worker start deadline: %v", ctx.Err())
		case <-ticker.C:
		}
	}
	if _, err := localworkspace.ExecuteOperation(ctx, db.DB, ss, spec.OperationID, spec.Request); err != nil && !workspaceConflict(err) {
		t.Fatal(err)
	}
}

// Opt-in cross-language HTTP integration uses only temporary DBs and file trees.
// It does not invoke a model, auth-service login, or the user's running Local stack.
func TestWorkspacePythonCoreHTTP(t *testing.T) {
	python := os.Getenv("LAZYMIND_WORKSPACE_E2E_PYTHON")
	if python == "" {
		t.Skip("set LAZYMIND_WORKSPACE_E2E_PYTHON to the existing test interpreter")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"main", "subagent", "workflow"} {
		t.Run(actor, func(t *testing.T) {
			db, ss, request := workspaceIdentityFixture(t)
			if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", request.ConversationID).Update("permission_mode", localworkspace.PermissionAlwaysAsk).Error; err != nil {
				t.Fatal(err)
			}
			identity := map[string]string{}
			if actor == "main" {
				identity = map[string]string{"history_id": "http-history", "run_id": "http-run"}
				if err := setChatRuntimeStatus(t.Context(), ss, request.ConversationID, identity["history_id"], "generating", "", identity["run_id"], nil); err != nil {
					t.Fatal(err)
				}
			} else {
				kind := "research"
				if actor == "workflow" {
					kind = "workflow_step"
				}
				task := orm.SubAgentTask{ID: request.ConversationID + "-child", ConversationID: request.ConversationID, CreateUserID: "owner", AgentType: kind, Status: "running", Mode: "auto", InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)}
				if err := db.Create(&task).Error; err != nil {
					t.Fatal(err)
				}
				identity = map[string]string{"task_id": task.ID, "generation": "1"}
				if actor == "subagent" {
					key := "rag/subagent/execution:" + task.ID
					if err := ss.Set(t.Context(), key, []byte("1"), time.Hour); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = ss.Del(context.Background(), key) })
				} else {
					expiry := time.Now().Add(time.Hour)
					revision := orm.WorkflowRevision{ID: request.ConversationID + "-revision", CompiledGraph: json.RawMessage(`{"nodes":{"step":{"legacy_tools":["local_fs"]}}}`)}
					session := orm.WorkflowSession{ID: request.ConversationID + "-session", WorkflowRevisionID: revision.ID, ConversationID: request.ConversationID, CreateUserID: "owner", Status: "active"}
					step := orm.WorkflowSessionStep{ID: request.ConversationID + "-attempt", SessionID: session.ID, TaskID: task.ID, StepID: "step", Status: "running", Validity: "effective", FencingGeneration: 1, LeaseToken: "http-lease", LeaseExpiresAt: &expiry}
					for _, row := range []any{&revision, &session, &step} {
						if err := db.Create(row).Error; err != nil {
							t.Fatal(err)
						}
					}
					identity["attempt_id"], identity["lease_token"] = step.ID, step.LeaseToken
				}
			}
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "isolated-http-test")
			router := mux.NewRouter()
			router.HandleFunc("/internal/conversations/{conversation_id}/workspace-operations:prepare", localworkspace.InternalPrepareOperation).Methods("POST")
			router.HandleFunc("/internal/conversations/{conversation_id}/workspace-operations/{operation_id}", localworkspace.InternalOperationStatus).Methods("GET")
			router.HandleFunc("/internal/conversations/{conversation_id}/workspace-operations/{operation_id}:execute", localworkspace.InternalExecuteOperation).Methods("POST")
			router.HandleFunc("/conversations/{conversation_id}:workspace-approvals", localworkspace.ListOperationApprovals).Methods("GET")
			router.HandleFunc("/conversations/{conversation_id}/workspace-approvals/{operation_id}:decide", localworkspace.DecideOperationHandler).Methods("POST")
			server := httptest.NewServer(router)
			defer server.Close()
			snapshot, err := localworkspace.ResolveForConversation(t.Context(), db, "owner", request.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			fixture, _ := json.Marshal(map[string]any{"url": server.URL, "token": "isolated-http-test", "conversation": request.ConversationID, "workspace": request.WorkspaceID, "root": snapshot.Root, "identity": identity})
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "-m", "pytest", "tests/algorithm/chat/test_workspace_authorization_contract.py::test_workspace_real_core_http_roundtrip", "-q")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(root, "algorithm")+string(os.PathListSeparator)+filepath.Join(root, "algorithm/lazyllm"), "LAZYMIND_WORKSPACE_HTTP_FIXTURE="+string(fixture))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s HTTP integration: %v\n%s", actor, err, output)
			}
			if !strings.Contains(string(output), "1 passed") {
				t.Fatalf("Python test not executed: %s", output)
			}
		})
	}
}
