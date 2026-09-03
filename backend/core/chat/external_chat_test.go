package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/externalcontext"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/workflow/artifactfile"
)

func newExternalChatTestApplication(t *testing.T) (*externalChatApplication, *gorm.DB) {
	t.Helper()
	database := newPromptTestDB(t)
	db := database.DB
	if err := db.AutoMigrate(
		&orm.Conversation{}, &orm.ChatHistory{}, &orm.TaskCenterTask{},
		&orm.ExternalAgentBinding{}, &orm.ExternalAgentSession{}, &orm.ExternalChatRun{}, &orm.ExternalChatRunEvent{}, &orm.ExternalChatHost{}, &orm.AgentInvocation{},
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

func seedExternalConversationPolicy(t *testing.T, db *gorm.DB, userID string, enableWorkflow bool, workflowMode string, enableSubagent bool) {
	t.Helper()
	if err := db.Exec(`
INSERT INTO user_chat_settings (
  user_id, enable_workflow, plugin_mode, enable_subagent,
  quick_question_defaults, new_task_defaults, updated_at
) VALUES (?, ?, ?, ?, '{}', '{}', ?)
`, userID, enableWorkflow, workflowMode, enableSubagent, time.Now().UTC()).Error; err != nil {
		t.Fatalf("create user chat settings: %v", err)
	}
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
	var binding orm.ExternalAgentBinding
	if err := db.Where("provider = ? AND provider_thread_id = ?", ChatExecutorCodex, "private-thread").Take(&binding).Error; err != nil ||
		binding.ConversationID != "conversation-1" || binding.CreatedByUserID != "user-1" {
		t.Fatalf("managed thread binding: binding=%+v err=%v", binding, err)
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
	second, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
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
		if _, err := app.appendEvent(ctx, "user-1", second.RunID, "host-1", second.LeaseToken, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.reportHost(ctx, "user-1", ChatExecutorCodex, "host-1", true, true, ""); err != nil {
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

func TestExternalContinuationPreservesProviderOwnedConversation(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "external-binding", ConversationID: "conversation-1",
		Provider: ChatExecutorCodex, HostID: "host-1", ProviderThreadID: "external-thread",
		CreatedByUserID: "user-1",
		CreatedAt:       now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := app.createRun(context.Background(), &orm.ExternalChatRun{
		ID: "external-continuation", RequestID: "external-continuation",
		ConversationID: "conversation-1", HistoryID: "external-history",
		Provider: ChatExecutorCodex, HostID: "host-1", ProviderThreadID: "external-thread",
		ActorUserID: "user-1", Action: "resume", Prompt: "prompt",
		Query: "continue", Sequence: 1,
	}); err != nil {
		t.Fatal(err)
	}
	job, err := app.claim(context.Background(), "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim continuation: job=%#v err=%v", job, err)
	}
	if job.ProviderThreadID != "external-thread" || job.HostID != "host-1" {
		t.Fatalf("external Codex continuation=%#v", job)
	}
	if _, err := app.appendEvent(
		context.Background(), "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "continued-thread", Type: "thread_started", ProviderThreadID: "external-thread"},
	); err != nil {
		t.Fatal(err)
	}
	var binding orm.ExternalAgentBinding
	if err := db.First(&binding, "id = ?", "external-binding").Error; err != nil {
		t.Fatal(err)
	}
	if binding.ManagedByLazyMind {
		t.Fatal("continuing a provider-owned thread changed its ownership")
	}
}

func TestExternalConversationBindsOneNativeThreadPerProvider(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	service := externalcontext.New(db)
	ctx := context.Background()
	if err := service.BindManagedThread(ctx, "user-1", ChatExecutorCodex, "host-1", "codex-thread", "conversation-1"); err != nil {
		t.Fatal(err)
	}
	var managed orm.ExternalAgentBinding
	if err := db.First(&managed, "provider_thread_id = ?", "codex-thread").Error; err != nil {
		t.Fatal(err)
	}
	if !managed.ManagedByLazyMind {
		t.Fatal("LazyMind-created thread was not marked as managed")
	}
	if err := service.BindManagedThread(ctx, "user-1", ChatExecutorCursor, "host-1", "cursor-thread", "conversation-1"); err != nil {
		t.Fatalf("bind second provider in one conversation: %v", err)
	}
	var bindingCount int64
	if err := db.Model(&orm.ExternalAgentBinding{}).Where("conversation_id = ?", "conversation-1").Count(&bindingCount).Error; err != nil || bindingCount != 2 {
		t.Fatalf("provider bindings=%d err=%v", bindingCount, err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conversation-2", BaseModel: orm.BaseModel{
		CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.BindManagedThread(ctx, "user-1", ChatExecutorCursor, "host-1", "cursor-thread", "conversation-2"); !errors.Is(err, externalcontext.ErrThreadOwned) {
		t.Fatalf("rebind Cursor thread err=%v, want ErrThreadOwned", err)
	}
	if err := service.BindManagedThread(ctx, "user-1", ChatExecutorCodex, "host-1", "another-codex-thread", "conversation-1"); !errors.Is(err, externalcontext.ErrThreadOwned) {
		t.Fatalf("second Codex thread err=%v, want ErrThreadOwned", err)
	}
}

func TestExternalSessionCatalogSyncSeparatesDiscoveryFromImportedBindings(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	now := time.Now().UTC()
	for _, binding := range []orm.ExternalAgentBinding{
		{ID: "external", ConversationID: "conversation-1", Provider: ChatExecutorCodex,
			HostID: "host-1", ProviderThreadID: "external-thread",
			CreatedByUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&binding).Error; err != nil {
			t.Fatal(err)
		}
	}
	updated, err := externalcontext.New(db).SyncSessionCatalog(
		context.Background(), "user-1", ChatExecutorCodex, "host-1",
		[]externalcontext.NativeSession{
			{ThreadID: "external-thread", ProjectKey: "codex-project", ProjectName: "LazyRAG", DisplayName: "Existing"},
			{ThreadID: "unknown-thread", ProjectKey: "unknown-project", ProjectName: "Unknown", DisplayName: "Discovered"},
		}, true,
	)
	if err != nil || updated != 2 {
		t.Fatalf("updated=%d err=%v", updated, err)
	}
	var binding orm.ExternalAgentBinding
	if err := db.First(&binding, "id = ?", "external").Error; err != nil {
		t.Fatal(err)
	}
	var sessions []orm.ExternalAgentSession
	if err := db.Order("provider_thread_id ASC").Find(&sessions).Error; err != nil || len(sessions) != 2 {
		t.Fatalf("sessions=%#v err=%v", sessions, err)
	}
	if sessions[0].ProjectName != "LazyRAG" {
		t.Fatalf("unexpected catalog states=%#v", sessions)
	}
}

func TestExternalSessionCatalogImportsRealTurnsIdempotently(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	created := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	session := externalcontext.NativeSession{
		ThreadID: "workbuddy-thread", ProjectKey: "workbuddy-project", ProjectName: "LazyRAG",
		DisplayName: "修复\n项目", NativeUpdated: created.Add(time.Minute),
		Turns: []externalcontext.NativeTurn{
			{ID: "turn-1", User: "检查会话", Assistant: "检查完成", CreatedAt: created},
			{ID: "turn-2", User: "继续任务", Assistant: "继续完成", CreatedAt: created.Add(time.Minute)},
		},
	}
	for attempt := 0; attempt < 2; attempt++ {
		if updated, err := externalcontext.New(db).SyncSessionCatalog(
			context.Background(), "user-1", ChatExecutorWorkBuddy, "host-1", []externalcontext.NativeSession{session}, true,
		); err != nil || updated != 1 {
			t.Fatalf("attempt=%d updated=%d err=%v", attempt, updated, err)
		}
	}
	var binding orm.ExternalAgentBinding
	if err := db.First(&binding, "provider = ? AND provider_thread_id = ?", ChatExecutorWorkBuddy, "workbuddy-thread").Error; err != nil {
		t.Fatal(err)
	}
	var conversation orm.Conversation
	if err := db.First(&conversation, "id = ?", binding.ConversationID).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.ChatTimes != 2 || conversation.DisplayName != "修复 项目" {
		t.Fatalf("conversation=%#v", conversation)
	}
	var histories []orm.ChatHistory
	if err := db.Order("seq ASC").Find(&histories, "conversation_id = ?", binding.ConversationID).Error; err != nil || len(histories) != 2 {
		t.Fatalf("histories=%#v err=%v", histories, err)
	}
	if histories[0].Content != "检查会话" || histories[0].Result != "检查完成" || histories[1].Seq != 2 {
		t.Fatalf("histories=%#v", histories)
	}
	var catalog orm.ExternalAgentSession
	if err := db.First(&catalog, "provider = ? AND provider_thread_id = ?", ChatExecutorWorkBuddy, "workbuddy-thread").Error; err != nil {
		t.Fatal(err)
	}
	if catalog.TurnCount != 2 || !catalog.Active || catalog.ProjectName != "LazyRAG" {
		t.Fatalf("catalog=%#v", catalog)
	}
}

func TestExternalSessionCatalogAdoptsLegacyHostWithoutDuplicateTranscript(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "legacy-binding", ConversationID: "conversation-1", Provider: ChatExecutorWorkBuddy,
		HostID: "host-legacy", ProviderThreadID: "legacy-thread", CreatedByUserID: "user-1",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ExternalAgentSession{
		ID: "legacy-session", OwnerUserID: "user-1", Provider: ChatExecutorWorkBuddy,
		HostID: "host-legacy", ProviderThreadID: "legacy-thread", ProjectKey: "project-1",
		ProjectName: "LazyRAG", DisplayName: "Legacy", TurnCount: 1, Active: true,
		LastSeenAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{
		ID: "legacy-history", Seq: 1, ConversationID: "conversation-1",
		AlgorithmID: "external:" + ChatExecutorWorkBuddy, Content: "旧问题", Result: "旧回答",
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := externalcontext.New(db).SyncSessionCatalog(
		context.Background(), "user-1", ChatExecutorWorkBuddy, "host-current",
		[]externalcontext.NativeSession{{
			ThreadID: "legacy-thread", ProjectKey: "project-1", ProjectName: "LazyRAG", DisplayName: "Legacy",
			Turns: []externalcontext.NativeTurn{{ID: "turn-1", User: "旧问题", Assistant: "旧回答", CreatedAt: now}},
		}}, true,
	); err != nil {
		t.Fatal(err)
	}
	var binding orm.ExternalAgentBinding
	if err := db.First(&binding, "id = ?", "legacy-binding").Error; err != nil || binding.HostID != "host-current" {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	var histories int64
	if err := db.Model(&orm.ChatHistory{}).Where("conversation_id = ?", "conversation-1").Count(&histories).Error; err != nil || histories != 1 {
		t.Fatalf("histories=%d err=%v", histories, err)
	}
}

func TestDirectMCPInvocationDoesNotCreateSyntheticConversationTurn(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	service := externalcontext.New(db)
	source := externalcontext.Source{
		Provider: ChatExecutorWorkBuddy, HostID: "host-1",
		ThreadID: "workbuddy-pending-thread", TurnID: "turn-pending",
		ProjectKey: "workbuddy-project", ProjectName: "LazyRAG", Message: "检查最终回答",
	}
	link, err := service.ResolveInvocation(context.Background(), "user-1", "invocation-pending", source)
	if err != nil {
		t.Fatal(err)
	}
	if link.ConversationID == "" || link.ExternalRef != "" || link.HistoryID != "" {
		t.Fatalf("link=%#v", link)
	}
	var histories, runs int64
	if err := db.Model(&orm.ChatHistory{}).Where("conversation_id = ?", link.ConversationID).Count(&histories).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.ExternalChatRun{}).Where("conversation_id = ?", link.ConversationID).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if histories != 0 || runs != 0 {
		t.Fatalf("synthetic histories=%d runs=%d", histories, runs)
	}
}

func TestExternalConversationSnapshotsUserExecutionPolicy(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	seedExternalConversationPolicy(t, db, "snapshot-user", false, "auto", false)
	link, err := externalcontext.New(db).ResolveInvocation(
		context.Background(), "snapshot-user", "snapshot-invocation",
		externalcontext.Source{
			Provider: ChatExecutorCursor, HostID: "snapshot-host", ThreadID: "snapshot-thread",
			TurnID: "snapshot-turn", Message: "snapshot policy",
		},
	)
	if err != nil {
		t.Fatalf("resolve external invocation: %v", err)
	}
	var conversation orm.Conversation
	if err := db.Where("id = ?", link.ConversationID).Take(&conversation).Error; err != nil {
		t.Fatalf("load external conversation: %v", err)
	}
	if conversation.EnableWorkflow == nil || *conversation.EnableWorkflow ||
		conversation.WorkflowMode == nil || *conversation.WorkflowMode != "auto" ||
		conversation.EnableSubagent == nil || *conversation.EnableSubagent {
		t.Fatalf("external conversation did not snapshot user policy: %#v", conversation)
	}
	if err := db.Model(&orm.UserChatSettings{}).Where("user_id = ?", "snapshot-user").Updates(map[string]any{
		"enable_workflow": true,
		"plugin_mode":     "dynamic", // workflow-naming: persistence
		"enable_subagent": true,
	}).Error; err != nil {
		t.Fatalf("change user chat settings: %v", err)
	}
	config := loadUserAgentConfig(context.Background(), db, "snapshot-user", map[string]any{
		"conversation_id": link.ConversationID,
	})
	if config["enable_workflow"] != false || config["workflow_mode"] != "auto" || config["enable_subagent"] != false {
		t.Fatalf("external conversation policy changed with user defaults: %#v", config)
	}
}

func TestExistingExternalConversationFillsOnlyNullPolicyFields(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	now := time.Now().UTC()
	seedExternalConversationPolicy(t, db, "user-1", false, "auto", false)
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "conversation-1").
		Update("enable_plugin", true).Error; err != nil {
		t.Fatalf("seed non-NULL conversation policy: %v", err)
	}
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "existing-policy-binding", ConversationID: "conversation-1",
		Provider: ChatExecutorCursor, HostID: "existing-policy-host",
		ProviderThreadID: "existing-policy-thread", CreatedByUserID: "user-1",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create external binding: %v", err)
	}
	if _, err := externalcontext.New(db).ResolveInvocation(
		context.Background(), "user-1", "existing-policy-invocation",
		externalcontext.Source{
			Provider: ChatExecutorCursor, HostID: "existing-policy-host",
			ThreadID: "existing-policy-thread", TurnID: "existing-policy-turn",
		},
	); err != nil {
		t.Fatalf("resolve existing external conversation: %v", err)
	}
	var conversation orm.Conversation
	if err := db.Where("id = ?", "conversation-1").Take(&conversation).Error; err != nil {
		t.Fatalf("reload existing conversation: %v", err)
	}
	if conversation.EnableWorkflow == nil || !*conversation.EnableWorkflow ||
		conversation.WorkflowMode == nil || *conversation.WorkflowMode != "auto" ||
		conversation.EnableSubagent == nil || *conversation.EnableSubagent {
		t.Fatalf("existing external policy fill overwrote non-NULL data or missed NULL data: %#v", conversation)
	}
}

func TestNullConversationPolicyUsesHistoricalHardDefaults(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	seedExternalConversationPolicy(t, db, "user-1", false, "auto", false)
	config := loadUserAgentConfig(context.Background(), db, "user-1", map[string]any{
		"conversation_id": "conversation-1",
	})
	if config["enable_workflow"] != true || config["workflow_mode"] != "dynamic" || config["enable_subagent"] != true {
		t.Fatalf("NULL conversation policy followed mutable user defaults: %#v", config)
	}
}

func TestNativeSessionSyncBindsImmediatelyAndImportsIncrementally(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	service := externalcontext.New(db)
	session := externalcontext.NativeSession{
		ThreadID: "cursor-lazy-bind", ProjectKey: "cursor-project", ProjectName: "LazyRAG",
		DisplayName: "Cursor CLI 会话",
	}
	if _, err := service.SyncSessionCatalog(
		context.Background(), "user-1", ChatExecutorCursor, "host-1", []externalcontext.NativeSession{session}, true,
	); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := db.Model(&orm.ExternalAgentBinding{}).Where("provider = ?", ChatExecutorCursor).Count(&before).Error; err != nil || before != 1 {
		t.Fatalf("bindings after sync=%d err=%v", before, err)
	}
	binding, err := service.BindNativeSession(context.Background(), "user-1", ChatExecutorCursor, "host-1", session.ThreadID)
	if err != nil || binding.ConversationID == "" {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	session.Turns = []externalcontext.NativeTurn{{
		ID: "cursor-turn", User: "继续 Cursor 任务", Assistant: "Cursor 任务已恢复",
	}}
	if _, err := service.SyncSessionCatalog(
		context.Background(), "user-1", ChatExecutorCursor, "host-1", []externalcontext.NativeSession{session}, false,
	); err != nil {
		t.Fatal(err)
	}
	rebound, err := service.BindNativeSession(context.Background(), "user-1", ChatExecutorCursor, "host-1", session.ThreadID)
	if err != nil || rebound.ConversationID != binding.ConversationID {
		t.Fatalf("rebound=%#v err=%v", rebound, err)
	}
	var history orm.ChatHistory
	if err := db.Where("conversation_id = ?", binding.ConversationID).Take(&history).Error; err != nil ||
		history.Content != "继续 Cursor 任务" || history.Result != "Cursor 任务已恢复" {
		t.Fatalf("history=%#v err=%v", history, err)
	}
}

func TestExternalSessionCatalogAPIExposesBoundAndUnboundSessions(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	sessions := []externalcontext.NativeSession{
		{
			ThreadID: "real-thread", ProjectKey: "project-real", ProjectName: "LazyRAG",
			DisplayName: "真实会话",
			Turns:       []externalcontext.NativeTurn{{ID: "turn-real", User: "真实问题", Assistant: "真实回答"}},
		},
		{
			ThreadID: "empty-thread", ProjectKey: "project-empty", ProjectName: "LazyRAG",
			DisplayName: "空会话",
		},
	}
	if _, err := externalcontext.New(db).SyncSessionCatalog(
		context.Background(), "user-1", ChatExecutorCodex, "host-1", sessions, true,
	); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/external-chat/providers/codex/sessions?page_size=100", nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"provider": ChatExecutorCodex})
	recorder := httptest.NewRecorder()
	ListExternalAgentSessions(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Sessions []struct {
				ThreadID       string `json:"provider_thread_id"`
				ConversationID string `json:"conversation_id"`
				Bound          bool   `json:"bound"`
				TurnCount      int    `json:"turn_count"`
			} `json:"sessions"`
			Total int64 `json:"total_size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 2 || len(response.Data.Sessions) != 2 {
		t.Fatalf("response=%#v", response)
	}
	byThread := map[string]struct {
		ConversationID string
		Bound          bool
		TurnCount      int
	}{}
	for _, session := range response.Data.Sessions {
		byThread[session.ThreadID] = struct {
			ConversationID string
			Bound          bool
			TurnCount      int
		}{session.ConversationID, session.Bound, session.TurnCount}
	}
	if real := byThread["real-thread"]; !real.Bound || real.ConversationID == "" || real.TurnCount != 1 {
		t.Fatalf("real session=%#v", real)
	}
	if empty := byThread["empty-thread"]; !empty.Bound || empty.ConversationID == "" {
		t.Fatalf("linked empty session=%#v", empty)
	}
}

func TestDisablingSessionAccessHidesEveryImportedProviderConversation(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	service := externalcontext.New(db)
	for index, hostID := range []string{"host-old", "host-current"} {
		session := externalcontext.NativeSession{
			ThreadID: fmt.Sprintf("thread-%d", index), ProjectKey: fmt.Sprintf("project-%d", index),
			ProjectName: "LazyRAG", DisplayName: fmt.Sprintf("Imported %d", index),
			Turns: []externalcontext.NativeTurn{{
				ID: fmt.Sprintf("turn-%d", index), User: "private question", Assistant: "private answer",
			}},
		}
		if _, err := service.SyncSessionCatalog(
			context.Background(), "user-1", ChatExecutorCodex, hostID,
			[]externalcontext.NativeSession{session}, false,
		); err != nil {
			t.Fatal(err)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/external-chat/providers/codex/sessions:sync", strings.NewReader(
		`{"host_id":"host-current","sessions":[],"reset":true,"session_access_enabled":false}`,
	))
	request.Header.Set("X-User-Id", "user-1")
	request = mux.SetURLVars(request, map[string]string{"provider": ChatExecutorCodex})
	response := httptest.NewRecorder()
	SyncExternalAgentSessions(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
	}
	var active int64
	if err := db.Model(&orm.ExternalAgentSession{}).
		Where("owner_user_id = ? AND provider = ? AND active = ?", "user-1", ChatExecutorCodex, true).
		Count(&active).Error; err != nil || active != 0 {
		t.Fatalf("active sessions=%d err=%v", active, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/conversations", nil)
	request.Header.Set("X-User-Id", "user-1")
	response = httptest.NewRecorder()
	ListConversations(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var list struct {
		Data struct {
			Conversations []any `json:"conversations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data.Conversations) != 0 {
		t.Fatalf("disabled imported conversations remain visible: %#v", list.Data.Conversations)
	}
}

func TestExternalConversationThreadPrefersBindingOverRunHistory(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "authoritative-binding", ConversationID: "conversation-1",
		Provider: ChatExecutorCodex, HostID: "host-1", ProviderThreadID: "bound-thread",
		CreatedByUserID: "user-1",
		CreatedAt:       now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := app.createRun(context.Background(), &orm.ExternalChatRun{
		ID: "legacy-run", RequestID: "legacy-run", ConversationID: "conversation-1",
		HistoryID: "legacy-history", Provider: ChatExecutorCodex,
		ActorUserID: "user-1", Action: "resume", Prompt: "prompt",
		Query: "query", Sequence: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.ExternalChatRun{}).Where("id = ?", "legacy-run").Updates(map[string]any{
		"status": "completed", "provider_thread_id": "stale-run-thread",
	}).Error; err != nil {
		t.Fatal(err)
	}

	threadID, hostID, resume, err := externalConversationThread(
		context.Background(), db, "user-1", "conversation-1", ChatExecutorCodex,
	)
	if err != nil || !resume || threadID != "bound-thread" || hostID != "host-1" {
		t.Fatalf("binding lookup thread=%q host=%q resume=%v err=%v", threadID, hostID, resume, err)
	}
	if err := db.Delete(&orm.ExternalAgentBinding{}, "id = ?", "authoritative-binding").Error; err != nil {
		t.Fatal(err)
	}
	threadID, hostID, resume, err = externalConversationThread(
		context.Background(), db, "user-1", "conversation-1", ChatExecutorCodex,
	)
	if err != nil || !resume || threadID != "stale-run-thread" {
		t.Fatalf("legacy fallback thread=%q host=%q resume=%v err=%v", threadID, hostID, resume, err)
	}
}

func TestExternalAgentFailureTextPreservesBoundedProviderReason(t *testing.T) {
	message := externalAgentFailureText("429 Credits exhausted")
	if !strings.Contains(message, "429 Credits exhausted") || !strings.HasPrefix(message, "外部助理执行失败：") {
		t.Fatalf("message=%q", message)
	}
	if len([]rune(externalAgentFailureText(strings.Repeat("x", 600)))) > 510 {
		t.Fatal("provider failure text was not bounded")
	}
}

func TestFailedExternalRunStreamsProviderReasonBeforeTerminal(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-provider-failure")
	job, err := app.claim(context.Background(), "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim=%#v err=%v", job, err)
	}
	if _, err := app.appendEvent(
		context.Background(), "user-1", job.RunID, job.HostID, job.LeaseToken,
		externalChatEvent{EventID: "provider-failed", Type: "failed", Error: "429 Credits exhausted"},
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var text string
	var terminal *ChatRuntimeEvent
	for chunk := range streamExistingExternalChat(ctx, db, "user-1", job.RunID, job.RunID) {
		text += chunk.Text
		if chunk.RuntimeEvent != nil {
			terminal = chunk.RuntimeEvent
		}
	}
	var terminalData RunTerminal
	if terminal != nil {
		_ = json.Unmarshal(terminal.Data, &terminalData)
	}
	if !strings.Contains(text, "429 Credits exhausted") || terminal == nil || terminalData.Status != "failed" {
		t.Fatalf("text=%q terminal=%#v", text, terminal)
	}
}

func TestCompletedExternalRunStreamsFullExecutionProjection(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-terminal-projection")
	job, err := app.claim(context.Background(), "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim=%#v err=%v", job, err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.AgentInvocation{
		ID: "inv-terminal-projection", OwnerUserID: "user-1", ClientName: "cursor",
		ConnectorName: "lazymind-mcp", ConnectorInstanceID: "connector-1",
		Transport: "stdio", ToolName: "skill.list", Status: "succeeded",
		RequestHash: strings.Repeat("c", 64), RequestSummary: json.RawMessage(`{}`),
		ResultSummary: json.RawMessage(`{}`), ExternalRef: job.RunID,
		StartedAt: now, FinishedAt: &now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := app.appendEvent(
		context.Background(), "user-1", job.RunID, job.HostID, job.LeaseToken,
		externalChatEvent{EventID: "terminal-completed", Type: "completed"},
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var terminal UpstreamStreamChunk
	for chunk := range streamExistingExternalChat(ctx, db, "user-1", job.RunID, job.RunID) {
		if chunk.RuntimeEvent != nil {
			terminal = chunk
		}
	}
	if terminal.Execution == nil || terminal.Execution.Status != "completed" ||
		terminal.Execution.Invocation.Total != 1 || terminal.Execution.Invocation.Succeeded != 1 {
		t.Fatalf("terminal execution=%#v", terminal.Execution)
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

func TestExternalChatSemanticOutputUsesPersistedMessageEvents(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-semantic-output")
	now := time.Now().UTC()
	if err := db.Create(&orm.ExternalChatRunEvent{
		ID: "lifecycle-event", RunID: "run-semantic-output", Sequence: 1, Type: "thread_started", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	hasOutput, err := app.hasSemanticOutput(context.Background(), "run-semantic-output")
	if err != nil || hasOutput {
		t.Fatalf("lifecycle-only output=%v err=%v, want false/nil", hasOutput, err)
	}
	if err := db.Create(&orm.ExternalChatRunEvent{
		ID: "message-event", RunID: "run-semantic-output", Sequence: 2, Type: "message", Text: "answer", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	hasOutput, err = app.hasSemanticOutput(context.Background(), "run-semantic-output")
	if err != nil || !hasOutput {
		t.Fatalf("message output=%v err=%v, want true/nil", hasOutput, err)
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
	}, "make an image", true)

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
	}, "next turn", false)
	if strings.Contains(resumed, "do-not-replay") {
		t.Fatalf("resumed provider thread received duplicate history: %s", resumed)
	}
}

func TestWorkBuddyAlwaysReceivesLazyMindConversationHistory(t *testing.T) {
	if !externalAgentIncludesHistory(ChatExecutorWorkBuddy, true) {
		t.Fatal("WorkBuddy has no isolated provider session, so resumed turns must include LazyMind history")
	}
	if externalAgentIncludesHistory(ChatExecutorCodex, true) {
		t.Fatal("native resumable providers must not receive duplicate history")
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

func TestExternalChatAttachmentIsManagedAndPersistedInHistory(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
	app, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	createExternalChatTestRun(t, app, "run-image")
	job, err := app.claim(context.Background(), "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim image run: job=%#v err=%v", job, err)
	}
	content := []byte("\x89PNG\r\n\x1a\nfixture")
	body, _ := json.Marshal(map[string]any{
		"host_id": "host-1", "lease_token": job.LeaseToken, "event_id": "evt-image-1",
		"filename": "generated.png", "media_type": "image/png",
		"content_base64": base64.StdEncoding.EncodeToString(content),
	})
	publish := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/external-chat/runs/run-image:attachment", strings.NewReader(string(body)))
		request.Header.Set("X-User-Id", "user-1")
		request = mux.SetURLVars(request, map[string]string{"run_id": "run-image"})
		response := httptest.NewRecorder()
		PublishExternalChatAttachment(response, request)
		return response
	}
	for attempt := 0; attempt < 2; attempt++ {
		if response := publish(); response.Code != http.StatusOK {
			t.Fatalf("publish attempt %d: status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
	}
	var events []orm.ExternalChatRunEvent
	if err := db.Where("run_id = ? AND type = ?", job.RunID, "message").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || strings.Contains(events[0].Text, "content_base64") ||
		!strings.Contains(events[0].Text, "![Generated image](/static-files/external-agent-attachments/") {
		t.Fatalf("image events=%#v", events)
	}
	if _, err := app.appendEvent(context.Background(), "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "evt-image-completed", Type: "completed"}); err != nil {
		t.Fatal(err)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", "history-run-image").Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history.Result, "![Generated image](/static-files/external-agent-attachments/") {
		t.Fatalf("history result=%q", history.Result)
	}
	files := 0
	if err := filepath.Walk(uploadRoot, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			files++
		}
		return err
	}); err != nil || files != 1 {
		t.Fatalf("managed image files=%d err=%v", files, err)
	}
}

func TestExternalChatAttachmentRejectsInvalidImageBeforeWriting(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
	app, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	createExternalChatTestRun(t, app, "run-invalid-image")
	job, err := app.claim(context.Background(), "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim image run: job=%#v err=%v", job, err)
	}
	body, _ := json.Marshal(map[string]any{
		"host_id": "host-1", "lease_token": job.LeaseToken, "event_id": "evt-invalid-image",
		"filename": "not-an-image.png", "media_type": "image/png",
		"content_base64": base64.StdEncoding.EncodeToString([]byte("plain text")),
	})
	request := httptest.NewRequest(http.MethodPost, "/external-chat/runs/run-invalid-image:attachment", strings.NewReader(string(body)))
	request.Header.Set("X-User-Id", "user-1")
	request = mux.SetURLVars(request, map[string]string{"run_id": "run-invalid-image"})
	response := httptest.NewRecorder()
	PublishExternalChatAttachment(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	files := 0
	if err := filepath.Walk(uploadRoot, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			files++
		}
		return err
	}); err != nil || files != 0 {
		t.Fatalf("managed files=%d err=%v", files, err)
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
	second, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
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
	if _, err := app.appendEvent(ctx, "user-1", second.RunID, "host-1", second.LeaseToken,
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
	second, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || second == nil || second.Action != "finalize" || second.ProviderThreadID != "thread-final" {
		t.Fatalf("checkpoint reclaim: run=%#v err=%v", second, err)
	}
	if _, err := app.appendEvent(ctx, "user-1", second.RunID, "host-1", second.LeaseToken,
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
	if _, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "late-failure", Type: "failed", Error: "late failure"}); !errors.Is(err, errExternalChatLeaseLost) {
		t.Fatalf("late failure after stop err=%v, want lease lost", err)
	}
	stop, err := app.heartbeat(ctx, "user-1", job.RunID, "host-1", job.LeaseToken)
	if err != nil || !stop {
		t.Fatalf("stopped heartbeat: stop=%v err=%v", stop, err)
	}
	var run orm.ExternalChatRun
	if err := db.First(&run, "id = ?", job.RunID).Error; err != nil || run.Status != "stopped" || !run.StopRequested {
		t.Fatalf("persisted stop: run=%#v err=%v", run, err)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", job.HistoryID).Error; err != nil || history.RunStatus != "cancelled" {
		t.Fatalf("persisted stopped history: history=%#v err=%v", history, err)
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

func TestWorkBuddyExecutorUsesUnifiedProductName(t *testing.T) {
	for _, definition := range chatExecutorDefinitions {
		if definition.ID == ChatExecutorWorkBuddy {
			if definition.DisplayName != "WorkBuddy" {
				t.Fatalf("display name=%q", definition.DisplayName)
			}
			return
		}
	}
	t.Fatal("CodeBuddy executor definition is missing")
}
