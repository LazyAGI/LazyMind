package chat

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/workflow/artifactfile"
)

func newExternalChatTestApplication(t *testing.T) (*externalChatApplication, *gorm.DB) {
	t.Helper()
	database := newPromptTestDB(t)
	db := database.DB
	if err := db.AutoMigrate(
		&orm.Conversation{}, &orm.ChatHistory{}, &orm.TaskCenterTask{},
		&orm.ExternalChatRun{}, &orm.ExternalChatRunEvent{}, &orm.ExternalChatHost{}, &orm.AgentInvocation{},
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{}, &orm.ConversationArtifact{},
	); err != nil {
		t.Fatalf("migrate External Chat test store: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID:        "conversation-1",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return newExternalChatApplication(db), db
}

func TestExternalExecutionProjectionJoinsAuthoritiesWithoutOwningState(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	clock := time.Now().UTC()
	app.now = func() time.Time { return clock }
	app.leaseTTL = time.Second
	createExternalChatTestRun(t, app, "run-projection")
	ctx := context.Background()
	first, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || first == nil {
		t.Fatalf("first claim: run=%#v err=%v", first, err)
	}
	if _, err := app.appendEvent(ctx, "user-1", first.RunID, "host-1", first.LeaseToken,
		externalChatEvent{EventID: "projection-thread", Type: "thread_started", ProviderThreadID: "private-thread"}); err != nil {
		t.Fatal(err)
	}
	now := clock
	if err := db.Create(&orm.AgentInvocation{
		ID: "inv-interrupted", OwnerUserID: "user-1", ClientName: "codex", ConnectorName: "lazymind-mcp",
		ConnectorInstanceID: "connector-1", Transport: "stdio", ToolName: "knowledge.search",
		Status: "running", RequestHash: strings.Repeat("a", 64), RequestSummary: json.RawMessage(`{}`),
		ResultSummary: json.RawMessage(`{}`), ExternalRef: first.RunID,
		StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Second)
	second, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-2")
	if err != nil || second == nil || second.Action != "recover" {
		t.Fatalf("recovery claim: run=%#v err=%v", second, err)
	}
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-1", ConversationID: "conversation-1", OriginHost: "external-agent",
		ControllerHost: "external-agent", WorkflowID: "image", Status: "completed",
		StateVersion: 4, CurrentStepID: "publish", CreateUserID: "user-1",
		CreatedAt: clock, UpdatedAt: clock,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AgentInvocation{
		ID: "inv-succeeded", OwnerUserID: "user-1", ClientName: "codex", ConnectorName: "lazymind-mcp",
		ConnectorInstanceID: "connector-1", Transport: "stdio", ToolName: "workflow.submit_step",
		Status: "succeeded", RequestHash: strings.Repeat("b", 64), RequestSummary: json.RawMessage(`{}`),
		ResultSummary: json.RawMessage(`{}`), ExternalRef: second.RunID, WorkflowID: "image", SessionID: "session-1",
		StartedAt: clock, FinishedAt: &clock, CreatedAt: clock, UpdatedAt: clock,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, revision := range []orm.WorkflowSlotRevision{
		{ID: "revision-1", SessionID: "session-1", SlotID: "image", Revision: 1, Selected: false, Slot: "image", StepID: "publish", Attempt: 1, CreatedAt: clock},
		{ID: "revision-2", SessionID: "session-1", SlotID: "image", Revision: 2, Selected: true, Slot: "image", StepID: "publish", Attempt: 1, CreatedAt: clock},
	} {
		if err := db.Create(&revision).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "revision-1").Update("selected", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationArtifact{
		ID: "artifact-1", ConversationID: "conversation-1", HistoryID: second.HistoryID,
		Filename: "answer.txt", Slot: "answer", ContentType: "text", Value: json.RawMessage(`{"text":"answer"}`),
		CreateUserID: "user-1", CreatedAt: clock,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, event := range []externalChatEvent{
		{EventID: "projection-message", Type: "message", Text: "answer"},
		{EventID: "projection-completed", Type: "completed"},
	} {
		if _, err := app.appendEvent(ctx, "user-1", second.RunID, "host-2", second.LeaseToken, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.reportHost(ctx, "user-1", ChatExecutorCodex, "host-2", true, true, ""); err != nil {
		t.Fatal(err)
	}

	projections, err := app.executionProjections(ctx, "user-1", []string{second.HistoryID})
	if err != nil {
		t.Fatal(err)
	}
	projection, ok := projections[second.HistoryID]
	if !ok || projection.Status != "completed" || projection.Provider != ChatExecutorCodex || !projection.HostOnline ||
		projection.ClaimCount != 2 || projection.RecoveryCount != 1 || projection.EventCount != 3 {
		t.Fatalf("unexpected run projection: %#v", projection)
	}
	if projection.Invocation.Total != 2 || projection.Invocation.Succeeded != 1 ||
		projection.Invocation.Interrupted != 1 || strings.Join(projection.Invocation.Tools, ",") != "knowledge.search,workflow.submit_step" {
		t.Fatalf("unexpected invocation projection: %#v", projection.Invocation)
	}
	if len(projection.Workflows) != 1 || projection.Workflows[0].SessionID != "session-1" ||
		projection.Workflows[0].ArtifactCount != 1 || projection.Workflows[0].ArtifactRevisionCount != 2 ||
		projection.ArtifactCount != 2 || projection.ArtifactRevisionCount != 3 {
		t.Fatalf("unexpected Workflow/artifact projection: %#v", projection)
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-thread") || strings.Contains(string(encoded), "prompt") {
		t.Fatalf("projection leaked private execution input: %s", encoded)
	}
	foreign, err := app.executionProjections(ctx, "user-2", []string{second.HistoryID})
	if err != nil || len(foreign) != 0 {
		t.Fatalf("projection crossed owner boundary: %#v err=%v", foreign, err)
	}
}

func createExternalChatTestRun(t *testing.T, app *externalChatApplication, id string) {
	t.Helper()
	if err := app.createRun(context.Background(), &orm.ExternalChatRun{
		ID: id, RequestID: id, ConversationID: "conversation-1", HistoryID: "history-" + id,
		Provider: ChatExecutorCodex, ActorUserID: "user-1", Action: "start",
		Prompt: "prompt", Query: "question", Sequence: 1,
	}); err != nil {
		t.Fatalf("create External Chat run: %v", err)
	}
}

func TestExternalChatRewritesHostLocalWorkflowArtifactLink(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-artifact-link")
	job, err := app.claim(context.Background(), "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim run: job=%#v err=%v", job, err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-artifact-link", ConversationID: "conversation-1", OriginHost: "external-agent",
		OriginRef: job.RunID, ControllerHost: "external-agent", WorkflowID: "image-workflow",
		Status: "completed", CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	inline := json.RawMessage(`{"storage":"inline_base64","name":"kitten.png","mime_type":"image/png","size":5,"content_base64":"aW1hZ2U="}`)
	managed, _, err := artifactfile.Materialize("session-artifact-link", "human-artifact-link", inline)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: "human-artifact-link", SessionID: "session-artifact-link", Slot: "generated_image_output",
		ContentType: "image/png", Value: managed, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	humanID := "human-artifact-link"
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-artifact-link", SessionID: "session-artifact-link", SlotID: "generated_image_output",
		Slot: "generated_image_output", StepID: "generate_image", Attempt: 1, Revision: 1,
		Selected: true, Validity: "effective", HumanArtifactID: &humanID, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	message := externalChatEvent{EventID: "artifact-message", Type: "message", Text: "[下载原图](/Users/agent/workspace/kitten.png)"}
	if _, err := app.appendEvent(context.Background(), "user-1", job.RunID, "host-1", job.LeaseToken, message); err != nil {
		t.Fatal(err)
	}
	if _, err := app.appendEvent(context.Background(), "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "artifact-completed", Type: "completed"}); err != nil {
		t.Fatal(err)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", job.HistoryID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(history.Result, "/Users/") || !strings.Contains(history.Result, "/static-files/workflow-artifacts/") {
		t.Fatalf("history did not use LazyMind artifact reference: %s", history.Result)
	}
}

func TestExternalAgentPromptCarriesOnlySafeLazyMindContext(t *testing.T) {
	prompt := externalAgentPrompt(map[string]any{
		"workflow_context": map[string]any{
			"session_id": "session-1", "workflow_id": "image", "current_step": "prompt",
			"workflow_mode": "dynamic", "remote_root": "/private/runtime", "tree_hash": "secret-hash",
		},
		"explicit_resource_bindings": map[string]any{
			"skill_names": []string{"image-generation"}, "knowledge_base_ids": []string{"kb-1"},
			"workflow_refs": []string{"builtin:image"},
		},
		"filters":     map[string]any{"kb_id": []string{"kb-configured"}},
		"history":     []map[string]string{{"role": "user", "content": "earlier turn"}},
		"llm_config":  map[string]any{"api_key": "must-not-leak"},
		"tool_config": map[string]any{"token": "must-not-leak"},
	}, "make an image", false)

	for _, required := range []string{
		"session_id: session-1", "workflow_id: image", "current_step: prompt",
		"skills: image-generation", "knowledge_base_ids: kb-1", "workflow_refs: builtin:image",
		"knowledge_base_ids: kb-configured",
		"earlier turn", "make an image",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt does not contain %q: %s", required, prompt)
		}
	}
	for _, forbidden := range []string{"must-not-leak", "/private/runtime", "secret-hash", "llm_config", "tool_config"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt leaked %q: %s", forbidden, prompt)
		}
	}
	resumed := externalAgentPrompt(map[string]any{
		"history": []map[string]string{{"role": "user", "content": "do-not-replay"}},
	}, "next turn", true)
	if strings.Contains(resumed, "do-not-replay") {
		t.Fatalf("resumed provider thread received duplicate history: %s", resumed)
	}
}

func TestExternalConversationKnowledgeBaseIDsRespectsExplicitEmptyScope(t *testing.T) {
	ids := externalConversationKnowledgeBaseIDs(
		context.Background(),
		nil,
		map[string]any{"filters": map[string]any{"kb_id": []string{}}},
		"conversation-1",
	)
	if len(ids) != 0 {
		t.Fatalf("explicit empty knowledge scope = %v, want none", ids)
	}
}

func TestExternalChatRequestIdentityIsStableAndOwnerScoped(t *testing.T) {
	first := externalChatRequestKey("user-1", "channel-message-1")
	if first == "" || first != externalChatRequestKey("user-1", "channel-message-1") {
		t.Fatalf("request key is not stable: %q", first)
	}
	if first == externalChatRequestKey("user-2", "channel-message-1") {
		t.Fatal("request key is not isolated by owner")
	}
	runID, historyID := externalChatIdentity(first)
	if len(runID) != 36 || len(historyID) != 34 {
		t.Fatalf("invalid deterministic identities: run=%q history=%q", runID, historyID)
	}
}

func TestExternalChatRunClaimEventAndHistoryAreDurable(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-1")
	ctx := context.Background()
	if other, err := app.claim(ctx, "user-2", ChatExecutorCodex, "other"); err != nil || other != nil {
		t.Fatalf("another user claimed run: run=%#v err=%v", other, err)
	}
	job, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil || job.RunID != "run-1" || job.LeaseToken == "" || job.HostID != "host-1" {
		t.Fatalf("claim run: job=%#v err=%v", job, err)
	}
	if second, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-2"); err != nil || second != nil {
		t.Fatalf("active lease was claimed twice: run=%#v err=%v", second, err)
	}

	thread := externalChatEvent{EventID: "event-thread", Type: "thread_started", ProviderThreadID: "thread-1"}
	firstSequence, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken, thread)
	if err != nil {
		t.Fatalf("append thread event: %v", err)
	}
	retrySequence, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken, thread)
	if err != nil || retrySequence != firstSequence {
		t.Fatalf("idempotent retry: first=%d retry=%d err=%v", firstSequence, retrySequence, err)
	}
	if _, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "event-message", Type: "message", Text: "answer"}); err != nil {
		t.Fatalf("append message: %v", err)
	}
	completed := externalChatEvent{EventID: "event-completed", Type: "completed"}
	completedSequence, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken, completed)
	if err != nil {
		t.Fatalf("append completion: %v", err)
	}
	if retry, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken, completed); err != nil || retry != completedSequence {
		t.Fatalf("retry accepted terminal event: sequence=%d err=%v", retry, err)
	}
	if _, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: completed.EventID, Type: "failed", Error: "different"}); err == nil {
		t.Fatal("conflicting terminal event replay was accepted")
	}
	if _, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "event-late", Type: "message", Text: "late"}); !errors.Is(err, errExternalChatLeaseLost) {
		t.Fatalf("late event error=%v, want lost lease", err)
	}

	var run orm.ExternalChatRun
	if err := db.First(&run, "id = ?", job.RunID).Error; err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if run.Status != "completed" || run.ProviderThreadID != "thread-1" || run.NextEventSequence != 3 {
		t.Fatalf("unexpected persisted run: %#v", run)
	}
	var eventCount int64
	if err := db.Model(&orm.ExternalChatRunEvent{}).Where("run_id = ?", job.RunID).Count(&eventCount).Error; err != nil || eventCount != 3 {
		t.Fatalf("event count=%d err=%v", eventCount, err)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", "history-run-1").Error; err != nil {
		t.Fatalf("history not finalized: %v", err)
	}
	if history.Result != "answer" || history.AlgorithmID != "external:codex" {
		t.Fatalf("unexpected finalized history: %#v", history)
	}
}

func TestExternalRunWakeupDoesNotMissNotification(t *testing.T) {
	wakeup := newExternalRunWakeup()
	available := wakeup.subscribe()
	wakeup.notify()
	select {
	case <-available:
	case <-time.After(time.Second):
		t.Fatal("run creation notification was missed")
	}
}

func TestExternalChatTerminalRunHealsGeneratingCache(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-heal")
	ctx := context.Background()
	job, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim: run=%#v err=%v", job, err)
	}
	if _, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "heal-message", Type: "message", Text: "durable answer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "heal-completed", Type: "completed"}); err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	if err := setChatStatus(ctx, stateStore, "conversation-1", job.HistoryID, "generating", ""); err != nil {
		t.Fatal(err)
	}
	remaining, err := reconcileGeneratingExternalChatStatuses(ctx, db, stateStore, "user-1", "conversation-1")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("reconcile: remaining=%v err=%v", remaining, err)
	}
	status, err := getChatStatus(ctx, stateStore, "conversation-1", job.HistoryID)
	if err != nil || status.Status != "completed" || status.CurrentResult != "durable answer" {
		t.Fatalf("healed status=%#v err=%v", status, err)
	}
	resumed, err := app.findRunForResume(ctx, "user-1", "conversation-1", "")
	if err != nil || resumed.ID != job.RunID {
		t.Fatalf("terminal run was not resumable: run=%#v err=%v", resumed, err)
	}
}

func TestExternalChatExpiredLeaseCanBeReclaimedAndFencesOldHost(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	clock := time.Now().UTC()
	app.now = func() time.Time { return clock }
	app.leaseTTL = time.Second
	createExternalChatTestRun(t, app, "run-reclaim")
	ctx := context.Background()
	first, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || first == nil {
		t.Fatalf("first claim: run=%#v err=%v", first, err)
	}
	if _, err := app.appendEvent(ctx, "user-1", first.RunID, "host-1", first.LeaseToken,
		externalChatEvent{EventID: "reclaim-thread", Type: "thread_started", ProviderThreadID: "thread-1"}); err != nil {
		t.Fatalf("persist provider thread: %v", err)
	}
	now := clock
	if err := db.Create(&orm.AgentInvocation{
		ID: "inv-reclaim", OwnerUserID: "user-1", ClientName: "codex", ConnectorName: "lazymind-mcp",
		ConnectorInstanceID: "connector-1", Transport: "stdio", ToolName: "workflow.state",
		Status: "running", RequestHash: strings.Repeat("a", 64), RequestSummary: []byte(`{}`), ResultSummary: []byte(`{}`),
		ExternalRef: first.RunID, StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create running invocation: %v", err)
	}
	clock = clock.Add(2 * time.Second)
	second, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-2")
	if err != nil || second == nil || second.LeaseToken == first.LeaseToken || second.Action != "recover" || second.ProviderThreadID != "thread-1" {
		t.Fatalf("reclaim: first=%#v second=%#v err=%v", first, second, err)
	}
	var invocation orm.AgentInvocation
	if err := db.First(&invocation, "id = ?", "inv-reclaim").Error; err != nil || invocation.Status != "interrupted" ||
		invocation.ErrorCode != "EXTERNAL_RUN_RECLAIMED" || !invocation.Retryable {
		t.Fatalf("abandoned invocation was not closed: %#v err=%v", invocation, err)
	}
	if _, err := app.heartbeat(ctx, "user-1", first.RunID, "host-1", first.LeaseToken); !errors.Is(err, errExternalChatLeaseLost) {
		t.Fatalf("old heartbeat error=%v", err)
	}
	if _, err := app.appendEvent(ctx, "user-1", first.RunID, "host-1", first.LeaseToken,
		externalChatEvent{EventID: "stale", Type: "message", Text: "stale"}); !errors.Is(err, errExternalChatLeaseLost) {
		t.Fatalf("old event error=%v", err)
	}
	if _, err := app.appendEvent(ctx, "user-1", second.RunID, "host-2", second.LeaseToken,
		externalChatEvent{EventID: "current", Type: "message", Text: "current"}); err != nil {
		t.Fatalf("new Host event: %v", err)
	}
}

func TestExternalChatCompletedProviderCheckpointFinalizesWithoutRerun(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	clock := time.Now().UTC()
	app.now = func() time.Time { return clock }
	app.leaseTTL = time.Second
	createExternalChatTestRun(t, app, "run-finalize")
	ctx := context.Background()
	first, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || first == nil {
		t.Fatalf("first claim: run=%#v err=%v", first, err)
	}
	for _, event := range []externalChatEvent{
		{EventID: "finalize-thread", Type: "thread_started", ProviderThreadID: "thread-final"},
		{EventID: "finalize-message", Type: "message", Text: "one final answer"},
		{EventID: "finalize-checkpoint", Type: "turn_completed"},
	} {
		if _, err := app.appendEvent(ctx, "user-1", first.RunID, "host-1", first.LeaseToken, event); err != nil {
			t.Fatalf("append %s: %v", event.Type, err)
		}
	}
	clock = clock.Add(2 * time.Second)
	second, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-2")
	if err != nil || second == nil || second.Action != "finalize" || second.ProviderThreadID != "thread-final" {
		t.Fatalf("checkpoint reclaim: run=%#v err=%v", second, err)
	}
	if _, err := app.appendEvent(ctx, "user-1", second.RunID, "host-2", second.LeaseToken,
		externalChatEvent{EventID: "finalize-terminal", Type: "completed"}); err != nil {
		t.Fatalf("finalize reclaimed run: %v", err)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", second.HistoryID).Error; err != nil || history.Result != "one final answer" {
		t.Fatalf("checkpoint finalization duplicated result: %#v err=%v", history, err)
	}
}

func TestExternalChatConcurrentClaimHasSingleWinner(t *testing.T) {
	app, _ := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-concurrent")
	ctx := context.Background()
	var wait sync.WaitGroup
	winners := make(chan *externalChatJob, 2)
	errorsCh := make(chan error, 2)
	for _, host := range []string{"host-a", "host-b"} {
		wait.Add(1)
		go func(hostID string) {
			defer wait.Done()
			job, err := app.claim(ctx, "user-1", ChatExecutorCodex, hostID)
			if err != nil && !strings.Contains(strings.ToLower(err.Error()), "locked") {
				errorsCh <- err
				return
			}
			if job != nil {
				winners <- job
			}
		}(host)
	}
	wait.Wait()
	close(winners)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatalf("concurrent claim: %v", err)
	}
	count := 0
	for range winners {
		count++
	}
	if count != 1 {
		t.Fatalf("claim winners=%d, want 1", count)
	}
}

func TestExternalChatStopIsDurableAndPropagatesToLeaseOwner(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-stop")
	ctx := context.Background()
	job, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim: run=%#v err=%v", job, err)
	}
	if err := app.requestStop(ctx, "user-1", "conversation-1", job.HistoryID); err != nil {
		t.Fatalf("request stop: %v", err)
	}
	stop, err := app.heartbeat(ctx, "user-1", job.RunID, "host-1", job.LeaseToken)
	if err != nil || !stop {
		t.Fatalf("stopped heartbeat: stop=%v err=%v", stop, err)
	}
	var run orm.ExternalChatRun
	if err := db.First(&run, "id = ?", job.RunID).Error; err != nil || run.Status != "stopped" || !run.StopRequested {
		t.Fatalf("persisted stop: run=%#v err=%v", run, err)
	}
}

func TestExternalChatHostStatusUsesDurableTTLProjection(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	clock := time.Now().UTC()
	app.now = func() time.Time { return clock }
	ctx := context.Background()
	if err := app.reportHost(ctx, "user-1", ChatExecutorCursor, "missing", false, false, "cursor-agent not found"); err != nil {
		t.Fatalf("report unavailable Host: %v", err)
	}
	status, err := app.hostStatus(ctx, "user-1", ChatExecutorCursor)
	if err != nil || !status.HostOnline || status.Installed || status.Available || !strings.Contains(status.UnavailableReason, "not found") {
		t.Fatalf("unavailable status=%#v err=%v", status, err)
	}
	if err := app.reportHost(ctx, "user-1", ChatExecutorCursor, "ready", true, true, ""); err != nil {
		t.Fatalf("report ready Host: %v", err)
	}
	status, err = app.hostStatus(ctx, "user-1", ChatExecutorCursor)
	if err != nil || !status.Available || !status.Installed {
		t.Fatalf("ready status=%#v err=%v", status, err)
	}
	clock = clock.Add(app.hostTTL + time.Second)
	status, err = app.hostStatus(ctx, "user-1", ChatExecutorCursor)
	if err != nil || status.HostOnline || status.Available {
		t.Fatalf("expired status=%#v err=%v", status, err)
	}
	clock = clock.Add(4*app.hostTTL + time.Second)
	if err := app.reportHost(ctx, "user-1", ChatExecutorCursor, "replacement", true, true, ""); err != nil {
		t.Fatalf("report replacement Host: %v", err)
	}
	var hostCount int64
	if err := db.Model(&orm.ExternalChatHost{}).Count(&hostCount).Error; err != nil || hostCount != 1 {
		t.Fatalf("stale Host projections were not pruned: count=%d err=%v", hostCount, err)
	}
}

func TestNormalizeChatExecutorSupportsAllHostedProviders(t *testing.T) {
	for _, provider := range []string{ChatExecutorLazyMind, ChatExecutorCodex, ChatExecutorCursor, ChatExecutorWorkBuddy} {
		normalized, valid := normalizeChatExecutor("  " + strings.ToUpper(provider) + " ")
		if !valid || normalized != provider {
			t.Fatalf("normalize %q = %q, %v", provider, normalized, valid)
		}
	}
	if _, valid := normalizeChatExecutor("unknown"); valid {
		t.Fatal("unknown provider was accepted")
	}
}
