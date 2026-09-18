package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
)

func TestExternalAdvancePersistsOwnerAndTaskThroughCoreTransition(t *testing.T) {
	db, graphHash := setupBatchTransitionSession(t)
	oldDB, oldState := store.DB(), store.State()
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(oldDB, oldDB, oldState) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	var session orm.WorkflowSession
	if err := db.First(&session, "id=?", "batch-session").Error; err != nil {
		t.Fatal(err)
	}
	task := orm.ExternalAgentWorkflowTask{ID: "external-task", TaskDescription: "Find PDF text extraction and table skills; do not install candidates"}
	projection := projectionResponse{StateVersion: 4, GraphHash: graphHash}
	for i := 0; i < 2; i++ {
		if err := advanceExternalStep(t.Context(), db.DB, task, session, projection, "branch_b"); err != nil {
			t.Fatal(err)
		}
	}
	var attempts []orm.SubAgentTask
	if err := db.Where("conversation_id=?", session.ConversationID).Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected one idempotent attempt, got %d", len(attempts))
	}
	var params WorkflowStepParams
	if err := json.Unmarshal(attempts[0].Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.HostedTaskID != task.ID || params.UserInput != task.TaskDescription {
		t.Fatalf("lost owner or task intent: %+v", params)
	}
	loaded := loadWorkflowChatContextFromDB(t.Context(), db.DB, attempts[0].ID)
	if loaded == nil || loaded.HostedTaskID != task.ID {
		t.Fatalf("lost owner at hook recovery: %+v", loaded)
	}
}

func TestHostedStepsDoNotInvokeNativeDriver(t *testing.T) {
	for _, status := range []string{subagent.StatusSucceeded, subagent.StatusFailed, subagent.StatusInterrupted} {
		t.Run(status, func(t *testing.T) {
			db := newTestDB(t)
			ctx := context.Background()
			hash, version := seedEventLoopRevision(t, db, "hosted-revision")
			if _, err := CreateSession(ctx, db.DB, CreateSessionInput{SessionID: "hosted-session", ConversationID: "hosted-conv", WorkflowID: "image-workflow", WorkflowRevisionID: "hosted-revision", GraphHash: hash, GraphSchemaVersion: version}); err != nil {
				t.Fatal(err)
			}
			if _, err := CreateSessionStep(ctx, db.DB, "hosted-session", "analyze_subject", "hosted-step", 1); err != nil {
				t.Fatal(err)
			}
			driverCalls := make(chan struct{}, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/workflow/driver" {
					driverCalls <- struct{}{}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":"done"}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			handOff := false
			events := make(chan string, 16)
			OnSubAgentDone(ctx, db.DB, nil, "hosted-step", status, "step result", func(kind string, payload map[string]any) { events <- kind }, &WorkflowChatContext{SessionID: "hosted-session", ConvID: "hosted-conv", StepID: "analyze_subject", WorkflowID: "image-workflow", WorkflowMode: "auto", HostedTaskID: "external-task", HandOff: &handOff})
			select {
			case <-driverCalls:
				t.Fatal("external scheduler also invoked native DriverAgent")
			case <-time.After(150 * time.Millisecond):
			}
			session, err := GetSession(ctx, db.DB, "hosted-session")
			if err != nil {
				t.Fatal(err)
			}
			want := SessionStatusWaiting
			if status == subagent.StatusFailed {
				want = SessionStatusFailed
			}
			if session.Status != want {
				t.Fatalf("status=%s want=%s", session.Status, want)
			}
			if first := <-events; first != "workflow_step_feedback" {
				t.Fatalf("missing native feedback: %s", first)
			}
		})
	}
}

func TestExternalSkillSourceDoesNotMatchAnotherPublisherOrOwner(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "owner", "installed", "# Finder")
	addSkillRevisionFileForWorkflowConversion(t, db, "installed-rev", "_meta.json", `{"slug":"finder","ownerId":"author-a"}`, "text")
	sourceA := "https://skillhub.cn/skills/author-a/finder"
	if err := db.Model(&orm.SkillV2Revision{}).Where("id=?", "installed-rev").Updates(map[string]any{"source_ref_type": "url", "source_ref_id": sourceA}).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ owner, url, want string }{
		{"owner", sourceA, "installed"},
		{"owner", "https://skillhub.cn/skills/author-b/finder", ""},
		{"another-owner", sourceA, ""},
	} {
		source, err := normalizeExternalAgentWorkflowSkillSource(context.Background(), externalAgentWorkflowSkillInput{Name: "finder", URL: tc.url})
		if err != nil {
			t.Fatal(err)
		}
		if got := findExternalAgentSkillBySource(context.Background(), db.DB, tc.owner, source); got != tc.want {
			t.Fatalf("owner=%s source=%s: got %q want %q", tc.owner, tc.url, got, tc.want)
		}
	}
}

func TestExternalChatCheckpointsPreserveNativeFeedbackAndRollback(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	seedWorkflowModelSelection(t, db, "owner")
	task := orm.ExternalAgentWorkflowTask{ID: "external-task", OwnerUserID: "owner", AgentType: "codex", Status: externalTaskStatusRunning, Stage: "execute", RequestJSON: mustJSON(map[string]any{}), ResultSummaryJSON: mustJSON(map[string]any{}), ResultArtifactsJSON: mustJSON([]any{})}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	convID, err := ensureExternalWorkflowConversation(ctx, db.DB, task)
	if err != nil {
		t.Fatal(err)
	}
	task.ConversationID = convID
	historyID, err := ensureExternalWorkflowChatAnchor(ctx, db.DB, task, convID)
	if err != nil {
		t.Fatal(err)
	}
	handOff := false
	pctx := &WorkflowChatContext{ConvID: convID, TriggerHistoryID: historyID, StepID: "search", HostedTaskID: task.ID, HandOff: &handOff}
	for i := 0; i < 2; i++ {
		if _, err := appendWorkflowStepFeedback(ctx, db.DB, pctx, "search-task", subagent.StatusSucceeded, "PDF candidates found"); err != nil {
			t.Fatal(err)
		}
	}
	if err := attachExternalWorkflowChatAnchor(ctx, db.DB, task, convID, historyID, "session"); err != nil {
		t.Fatal(err)
	}
	task = persistExternalTask(db.DB, task, map[string]any{"session_id": "session", "stage": "collect_result"})
	if task.ErrorCode != "" {
		t.Fatalf("checkpoint: %+v", task)
	}
	const callback = "test:fail-external-workflow-chat-write"
	rejectChatWrite := true
	rejectedWrites := 0
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if rejectChatWrite && tx.Statement.Table == "chat_histories" {
			rejectedWrites++
			tx.AddError(errors.New("test chat write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Update().Remove(callback) })
	failed := persistExternalTask(db.DB, task, map[string]any{"status": externalTaskStatusSucceeded})
	if rejectedWrites != 1 {
		t.Fatalf("expected one rejected chat update, got %d", rejectedWrites)
	}
	if failed.ErrorCode != "TASK_STATE_WRITE_FAILED" {
		t.Fatalf("expected retryable failure: %+v", failed)
	}
	var stored orm.ExternalAgentWorkflowTask
	if err := db.First(&stored, "id=?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != externalTaskStatusRunning {
		t.Fatalf("task state must roll back with chat: %s", stored.Status)
	}
	rejectChatWrite = false
	task = persistExternalTask(db.DB, task, map[string]any{"status": externalTaskStatusSucceeded})
	task = persistExternalTask(db.DB, task, map[string]any{"status": externalTaskStatusRunning})
	if task.Status != externalTaskStatusSucceeded {
		t.Fatalf("stale checkpoint regressed terminal task: %+v", task)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id=?", historyID).Error; err != nil {
		t.Fatal(err)
	}
	if history.RunStatus != "completed" || strings.Count(history.Result, "<!-- workflow-step-feedback:search-task -->") != 1 || !strings.Contains(history.Result, "PDF candidates found") || strings.Count(history.Result, "<!-- external-workflow-result -->") != 1 {
		t.Fatalf("lost or duplicated feedback/result: %s", history.Result)
	}
}

func TestExternalChatMergePreservesFeedbackForEveryOutcome(t *testing.T) {
	for _, status := range []string{externalTaskStatusRunning, externalTaskStatusSucceeded, externalTaskStatusFailed, externalTaskStatusWaitingUserAction} {
		t.Run(status, func(t *testing.T) {
			task := orm.ExternalAgentWorkflowTask{Status: status, Stage: "execute"}
			feedback := "<!-- workflow-step-feedback:first -->\nFirst step completed"
			original := "<think>old progress</think>\n\n" + feedback
			result := mergeExternalWorkflowChatResult(original, task, "final result")
			result += "\n\n<!-- workflow-step-feedback:second -->\nSecond step completed"
			result = mergeExternalWorkflowChatResult(result, task, "updated result")
			if !strings.Contains(result, feedback) || !strings.Contains(result, "Second step completed") || strings.Contains(result, "final result") || strings.Count(result, "<think>") != 1 || strings.Count(result, "updated result") != 1 {
				t.Fatalf("bad merge: %s", result)
			}
		})
	}
}
