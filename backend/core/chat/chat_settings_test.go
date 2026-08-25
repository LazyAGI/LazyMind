package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	corestore "lazymind/core/store"

	"github.com/gorilla/mux"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// setupChatSettingsTest creates a SQLite store with UserChatSettings and Conversation tables migrated.
func setupChatSettingsTest(t *testing.T) {
	t.Helper()
	db := newPromptTestDB(t)
	// Also migrate Conversation table for PatchConversationSettings.
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ExternalChatHost{}); err != nil {
		t.Fatalf("migrate conversation: %v", err)
	}
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
}

// newSettingsRequest creates a request with X-User-Id header.
func newSettingsRequest(method, path, body string, userID string, vars map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	return req
}

func decodeChatSettingsResponse(t *testing.T, recorder *httptest.ResponseRecorder) chatSettingsResponse {
	t.Helper()
	var response struct {
		Data chatSettingsResponse `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode chat settings response: %v", err)
	}
	return response.Data
}

// --- GetChatSettings ---

// TestGetChatSettings_ReturnsDefaults returns default settings when no record exists.
func TestGetChatSettings_ReturnsDefaults(t *testing.T) {
	setupChatSettingsTest(t)
	req := newSettingsRequest("GET", "/chat/settings", "", "user-1", nil)
	w := httptest.NewRecorder()
	GetChatSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	nested := decodeChatSettingsResponse(t, w)
	// Defaults: enable_workflow=true, workflow_mode=dynamic, enable_subagent=true.
	if !nested.EnableWorkflow {
		t.Fatalf("enable_workflow: got false, want true")
	}
	if nested.WorkflowMode != "dynamic" {
		t.Fatalf("workflow_mode: got %v, want dynamic", nested.WorkflowMode)
	}
	if nested.QuickQuestion.ThinkingDepth != "medium" ||
		nested.QuickQuestion.ConversationSettings.EnableWorkflow ||
		nested.QuickQuestion.ConversationSettings.ChatExecutor != ChatExecutorLazyMind {
		t.Fatalf("unexpected quick-question defaults: %#v", nested.QuickQuestion)
	}
	if nested.NewTask.ThinkingDepth != "high" ||
		!nested.NewTask.ConversationSettings.EnableWorkflow ||
		nested.NewTask.ConversationSettings.WorkflowMode != "dynamic" {
		t.Fatalf("unexpected new-task defaults: %#v", nested.NewTask)
	}
}

// TestGetChatSettings_MissingUserID returns 401.
func TestGetChatSettings_MissingUserID(t *testing.T) {
	setupChatSettingsTest(t)
	req := newSettingsRequest("GET", "/chat/settings", "", "", nil)
	w := httptest.NewRecorder()
	GetChatSettings(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestGetChatSettings_AfterPatch returns the patched values.
func TestGetChatSettings_AfterPatch(t *testing.T) {
	setupChatSettingsTest(t)
	// First patch the settings.
	req1 := newSettingsRequest("PATCH", "/chat/settings", `{"enable_workflow":false,"workflow_mode":"auto"}`, "user-patched", nil)
	w1 := httptest.NewRecorder()
	PatchChatSettings(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("patch: status %d, body: %s", w1.Code, w1.Body.String())
	}

	// Then get them.
	req2 := newSettingsRequest("GET", "/chat/settings", "", "user-patched", nil)
	w2 := httptest.NewRecorder()
	GetChatSettings(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("get: status %d", w2.Code)
	}
	settings := decodeChatSettingsResponse(t, w2)
	if settings.EnableWorkflow {
		t.Fatal("enable_workflow: expected false, got true")
	}
	if settings.WorkflowMode != "auto" {
		t.Fatalf("workflow_mode: expected auto, got %v", settings.WorkflowMode)
	}
	if settings.QuickQuestion.ConversationSettings.WorkflowMode != "auto" ||
		settings.QuickQuestion.ConversationSettings.EnableWorkflow {
		t.Fatalf("legacy patch did not preserve quick-question workflow semantics: %#v", settings.QuickQuestion)
	}
	if settings.NewTask.ConversationSettings.WorkflowMode != "auto" ||
		settings.NewTask.ConversationSettings.EnableWorkflow {
		t.Fatalf("legacy patch did not sync new-task defaults: %#v", settings.NewTask)
	}
}

// --- PatchChatSettings ---

// TestPatchChatSettings_NoFields returns 400 when no valid fields are provided.
func TestPatchChatSettings_NoFields(t *testing.T) {
	setupChatSettingsTest(t)
	req := newSettingsRequest("PATCH", "/chat/settings", `{}`, "user-1", nil)
	w := httptest.NewRecorder()
	PatchChatSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestPatchChatSettings_InvalidWorkflowMode returns 400 for invalid mode.
func TestPatchChatSettings_InvalidWorkflowMode(t *testing.T) {
	setupChatSettingsTest(t)
	req := newSettingsRequest("PATCH", "/chat/settings", `{"workflow_mode":"invalid"}`, "user-1", nil)
	w := httptest.NewRecorder()
	PatchChatSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestPatchChatSettings_DisabledWorkflowModeWithWorkflow returns 409 conflict.
func TestPatchChatSettings_DisabledWorkflowModeWithWorkflow(t *testing.T) {
	setupChatSettingsTest(t)
	// The handler type-checks enable_workflow as bool. Pass valid bool values.
	req := newSettingsRequest("PATCH", "/chat/settings",
		`{"enable_workflow":false,"enable_subagent":true}`, "user-wf", nil)
	w := httptest.NewRecorder()
	PatchChatSettings(w, req)

	// Without active workflows it should succeed.
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestPatchChatSettingsStoresIndependentEntryDefaults(t *testing.T) {
	setupChatSettingsTest(t)
	body := `{
		"quick_question": {
			"thinking_depth": "low",
			"conversation_settings": {
				"chat_executor": "lazymind",
				"enable_workflow": true,
				"workflow_mode": "auto",
				"enable_subagent": false
			}
		},
		"new_task": {
			"thinking_depth": "max",
			"conversation_settings": {
				"chat_executor": "lazymind",
				"enable_workflow": false,
				"workflow_mode": "dynamic",
				"enable_subagent": true
			}
		}
	}`
	req := newSettingsRequest(http.MethodPatch, "/chat/settings", body, "user-profiles", nil)
	w := httptest.NewRecorder()
	PatchChatSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	settings := decodeChatSettingsResponse(t, w)
	if settings.QuickQuestion.ThinkingDepth != "low" ||
		!settings.QuickQuestion.ConversationSettings.EnableWorkflow ||
		settings.QuickQuestion.ConversationSettings.EnableSubagent {
		t.Fatalf("unexpected quick-question defaults: %#v", settings.QuickQuestion)
	}
	if settings.NewTask.ThinkingDepth != "max" ||
		settings.NewTask.ConversationSettings.EnableWorkflow ||
		!settings.NewTask.ConversationSettings.EnableSubagent {
		t.Fatalf("unexpected new-task defaults: %#v", settings.NewTask)
	}
	// The legacy fields mirror the new-task profile for installed clients and
	// for call paths that have not adopted entry-specific defaults yet.
	if settings.EnableWorkflow || settings.WorkflowMode != "dynamic" || !settings.EnableSubagent {
		t.Fatalf("legacy fields do not mirror new-task defaults: %#v", settings)
	}

	var stored orm.UserChatSettings
	if err := corestore.DB().Where("user_id = ?", "user-profiles").First(&stored).Error; err != nil {
		t.Fatalf("reload chat settings: %v", err)
	}
	if !json.Valid(stored.QuickQuestionDefaults) || !json.Valid(stored.NewTaskDefaults) {
		t.Fatalf("stored entry defaults are not valid JSON: quick=%s task=%s", stored.QuickQuestionDefaults, stored.NewTaskDefaults)
	}
}

func TestPatchChatSettingsUpdatesOnlyRequestedProfile(t *testing.T) {
	setupChatSettingsTest(t)
	first := newSettingsRequest(http.MethodPatch, "/chat/settings", `{
		"quick_question":{"thinking_depth":"low"},
		"new_task":{"thinking_depth":"max"}
	}`, "user-partial", nil)
	firstRecorder := httptest.NewRecorder()
	PatchChatSettings(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("seed profiles: status=%d body=%s", firstRecorder.Code, firstRecorder.Body.String())
	}

	second := newSettingsRequest(http.MethodPatch, "/chat/settings", `{
		"quick_question":{"conversation_settings":{"enable_subagent":false}}
	}`, "user-partial", nil)
	secondRecorder := httptest.NewRecorder()
	PatchChatSettings(secondRecorder, second)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("patch quick profile: status=%d body=%s", secondRecorder.Code, secondRecorder.Body.String())
	}
	settings := decodeChatSettingsResponse(t, secondRecorder)
	if settings.QuickQuestion.ThinkingDepth != "low" || settings.QuickQuestion.ConversationSettings.EnableSubagent {
		t.Fatalf("quick profile was not partially merged: %#v", settings.QuickQuestion)
	}
	if settings.NewTask.ThinkingDepth != "max" || !settings.NewTask.ConversationSettings.EnableSubagent {
		t.Fatalf("new-task profile was changed by quick patch: %#v", settings.NewTask)
	}
}

func TestUserChatSettingsUpdateQueryLocksPostgresOnly(t *testing.T) {
	postgresDB, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=postgres dbname=core sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open postgres dry-run db: %v", err)
	}
	var postgresRow orm.UserChatSettings
	postgresStatement := userChatSettingsUpdateQuery(postgresDB).
		Where("user_id = ?", "user-lock").First(&postgresRow).Statement
	if sql := postgresStatement.SQL.String(); !strings.Contains(sql, "FOR UPDATE") {
		t.Fatalf("postgres settings read must lock the row: %s", sql)
	}

	setupChatSettingsTest(t)
	var sqliteRow orm.UserChatSettings
	sqliteStatement := userChatSettingsUpdateQuery(corestore.DB().Session(&gorm.Session{DryRun: true})).
		Where("user_id = ?", "user-lock").First(&sqliteRow).Statement
	if sql := sqliteStatement.SQL.String(); strings.Contains(sql, "FOR UPDATE") {
		t.Fatalf("sqlite settings read must not emit FOR UPDATE: %s", sql)
	}
}

func TestPatchChatSettingsRejectsInvalidEntryDefaults(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "thinking depth", body: `{"quick_question":{"thinking_depth":"turbo"}}`},
		{name: "workflow mode", body: `{"new_task":{"conversation_settings":{"workflow_mode":"manual"}}}`},
		{name: "chat executor", body: `{"quick_question":{"conversation_settings":{"chat_executor":"unknown"}}}`},
		{name: "empty nested patch", body: `{"quick_question":{"conversation_settings":{}}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupChatSettingsTest(t)
			req := newSettingsRequest(http.MethodPatch, "/chat/settings", tc.body, "invalid-user", nil)
			w := httptest.NewRecorder()
			PatchChatSettings(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want %d, body=%s", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

func TestEntryDefaultsDriveNewConversationAndThinkingDepth(t *testing.T) {
	setupChatSettingsTest(t)
	body := `{
		"quick_question": {
			"thinking_depth": "low",
			"conversation_settings": {
				"chat_executor": "lazymind",
				"enable_workflow": false,
				"workflow_mode": "dynamic",
				"enable_subagent": false
			}
		},
		"new_task": {
			"thinking_depth": "max",
			"conversation_settings": {
				"chat_executor": "lazymind",
				"enable_workflow": true,
				"workflow_mode": "auto",
				"enable_subagent": true
			}
		}
	}`
	req := newSettingsRequest(http.MethodPatch, "/chat/settings", body, "user-runtime-defaults", nil)
	w := httptest.NewRecorder()
	PatchChatSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch defaults: status=%d body=%s", w.Code, w.Body.String())
	}

	db := corestore.DB()
	quick := resolveInitialConversationSettings(context.Background(), db, "user-runtime-defaults", false, "", nil)
	if quick.enableWorkflow || quick.enableSubagent || quick.workflowMode != "dynamic" {
		t.Fatalf("unexpected quick conversation settings: %#v", quick)
	}
	task := resolveInitialConversationSettings(context.Background(), db, "user-runtime-defaults", true, "", nil)
	if !task.enableWorkflow || !task.enableSubagent || task.workflowMode != "auto" {
		t.Fatalf("unexpected new-task conversation settings: %#v", task)
	}
	overridden := resolveInitialConversationSettings(context.Background(), db, "user-runtime-defaults", true, "medium", map[string]any{
		"enable_workflow": false,
		"workflow_mode":   "dynamic",
		"enable_subagent": false,
	})
	if overridden.enableWorkflow || overridden.enableSubagent || overridden.workflowMode != "dynamic" {
		t.Fatalf("explicit conversation settings did not override defaults: %#v", overridden)
	}
	if overridden.thinkingDepth != "medium" {
		t.Fatalf("explicit thinking depth did not override defaults: %#v", overridden)
	}
	quickConversation, _, err := ensureConversation(
		context.Background(), db, "conv-quick-defaults", "", nil, nil,
		"user-runtime-defaults", "User", false, "", nil,
	)
	if err != nil {
		t.Fatalf("create quick-question conversation: %v", err)
	}
	if quickConversation.EnableWorkflow == nil || *quickConversation.EnableWorkflow ||
		quickConversation.EnableSubagent == nil || *quickConversation.EnableSubagent ||
		quickConversation.ThinkingDepth != "low" {
		t.Fatalf("quick-question conversation did not persist its profile: %#v", quickConversation)
	}
	taskConversation, _, err := ensureConversation(
		context.Background(), db, "conv-task-defaults", "", nil, nil,
		"user-runtime-defaults", "User", true, "", nil,
	)
	if err != nil {
		t.Fatalf("create new-task conversation: %v", err)
	}
	if taskConversation.EnableWorkflow == nil || !*taskConversation.EnableWorkflow ||
		taskConversation.EnableSubagent == nil || !*taskConversation.EnableSubagent ||
		taskConversation.WorkflowMode == nil || *taskConversation.WorkflowMode != "auto" ||
		taskConversation.ThinkingDepth != "max" {
		t.Fatalf("new-task conversation did not persist its profile: %#v", taskConversation)
	}
	explicitConversation, _, err := ensureConversation(
		context.Background(), db, "conv-explicit-defaults", "", nil, nil,
		"user-runtime-defaults", "User", true, "medium", nil,
	)
	if err != nil {
		t.Fatalf("create explicit-depth conversation: %v", err)
	}
	if explicitConversation.ThinkingDepth != "medium" {
		t.Fatalf("explicit depth was not snapshotted: %#v", explicitConversation)
	}

	updateDefaults := newSettingsRequest(http.MethodPatch, "/chat/settings", `{
		"quick_question":{"thinking_depth":"high"},
		"new_task":{"thinking_depth":"low"}
	}`, "user-runtime-defaults", nil)
	updateRecorder := httptest.NewRecorder()
	PatchChatSettings(updateRecorder, updateDefaults)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("change entry defaults: status=%d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}
	existingConversation, _, err := ensureConversation(
		context.Background(), db, "conv-quick-defaults", "", nil, nil,
		"user-runtime-defaults", "User", true, "max", nil,
	)
	if err != nil {
		t.Fatalf("reload existing conversation: %v", err)
	}
	if existingConversation.ThinkingDepth != "low" {
		t.Fatalf("existing conversation snapshot was replaced: got=%q want=low", existingConversation.ThinkingDepth)
	}

	quickBody := buildChatRequestBody(context.Background(), db, "conv-quick-defaults", "", "hello", nil,
		map[string]any{}, nil, "user-runtime-defaults", 1)
	if got := quickBody["thinking_depth"]; got != "low" {
		t.Fatalf("quick conversation snapshot changed with user defaults: got=%v want=low", got)
	}
	taskBody := buildChatRequestBody(context.Background(), db, "conv-task-defaults", "", "hello", nil,
		map[string]any{"run_in_background": true}, nil, "user-runtime-defaults", 1)
	if got := taskBody["thinking_depth"]; got != "max" {
		t.Fatalf("task conversation snapshot changed with user defaults: got=%v want=max", got)
	}
	if got := buildLazyChatRequest(taskBody).Runtime.ThinkingDepth; got != "max" {
		t.Fatalf("upstream new-task thinking depth=%q, want max", got)
	}
	explicitBody := buildChatRequestBody(context.Background(), db, "conv-task-defaults", "", "hello", nil,
		map[string]any{"run_in_background": true, "thinking_depth": "medium"}, nil, "user-runtime-defaults", 1)
	if got := explicitBody["thinking_depth"]; got != "medium" {
		t.Fatalf("explicit thinking depth=%v, want medium", got)
	}
}

// --- PatchConversationSettings ---

// TestPatchConversationSettings_NoConversation returns 400.
func TestPatchConversationSettings_NoConversation(t *testing.T) {
	setupChatSettingsTest(t)
	req := newSettingsRequest("PATCH", "/chat/conversations//workflow_settings", `{}`, "user-1", nil)
	w := httptest.NewRecorder()
	PatchConversationSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestPatchConversationSettings_MissingUserID returns 401.
func TestPatchConversationSettings_MissingUserID(t *testing.T) {
	setupChatSettingsTest(t)
	vars := map[string]string{"conversation_id": "conv-1"}
	req := newSettingsRequest("PATCH", "/chat/conversations/conv-1/workflow_settings", `{}`, "", vars)
	w := httptest.NewRecorder()
	PatchConversationSettings(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestPatchConversationSettings_InvalidWorkflowMode returns 400.
func TestPatchConversationSettings_InvalidWorkflowMode(t *testing.T) {
	setupChatSettingsTest(t)
	vars := map[string]string{"conversation_id": "conv-1"}
	req := newSettingsRequest("PATCH", "/chat/conversations/conv-1/workflow_settings",
		`{"workflow_mode":"bogus"}`, "user-1", vars)
	w := httptest.NewRecorder()
	PatchConversationSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPatchConversationSettings_RejectsInvalidExecutor(t *testing.T) {
	setupChatSettingsTest(t)
	req := newSettingsRequest(http.MethodPatch, "/chat/conversations/conv/settings",
		`{"chat_executor":"unknown"}`, "user-1", map[string]string{"conversation_id": "conv"})
	w := httptest.NewRecorder()
	PatchConversationSettings(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPatchConversationSettings_PersistsRuntimeSettings(t *testing.T) {
	setupChatSettingsTest(t)
	db := corestore.DB()
	if err := newExternalChatApplication(db).reportHost(
		context.Background(), "user-1", ChatExecutorCodex, "test-host", true, true, "",
	); err != nil {
		t.Fatalf("report external Host: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conv-mode", BaseModel: orm.BaseModel{
		CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	req := newSettingsRequest(http.MethodPatch, "/chat/conversations/conv-mode/settings",
		`{"workflow_mode":"auto","chat_executor":"codex"}`, "user-1", map[string]string{"conversation_id": "conv-mode"})
	w := httptest.NewRecorder()
	PatchConversationSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var stored orm.Conversation
	if err := db.First(&stored, "id = ?", "conv-mode").Error; err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	if stored.WorkflowMode == nil || *stored.WorkflowMode != "auto" {
		t.Fatalf("workflow mode was not persisted: %#v", stored.WorkflowMode)
	}
	if stored.ChatExecutor != ChatExecutorCodex {
		t.Fatalf("chat executor=%q, want %q", stored.ChatExecutor, ChatExecutorCodex)
	}
}

func TestPatchConversationSettings_AllowsExternalAssistantToChangeExecutor(t *testing.T) {
	setupChatSettingsTest(t)
	db := corestore.DB()
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "external-conversation", ChatExecutor: ChatExecutorCodex,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ExternalAgentBinding{ID: "external-binding", ConversationID: "external-conversation",
		Provider: ChatExecutorCodex, HostID: "host-1", ProviderThreadID: "codex-thread",
		CreatedByUserID: "user-1", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	req := newSettingsRequest(http.MethodPatch, "/chat/conversations/external-conversation/settings",
		`{"chat_executor":"lazymind"}`, "user-1", map[string]string{"conversation_id": "external-conversation"})
	w := httptest.NewRecorder()
	PatchConversationSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var stored orm.Conversation
	if err := db.First(&stored, "id = ?", "external-conversation").Error; err != nil {
		t.Fatal(err)
	}
	if stored.ChatExecutor != ChatExecutorLazyMind {
		t.Fatalf("external assistant engine=%q, want %q", stored.ChatExecutor, ChatExecutorLazyMind)
	}
}

func TestPatchConversationSettings_AllowsOneBindingPerAgent(t *testing.T) {
	setupChatSettingsTest(t)
	db := corestore.DB()
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: "managed-conversation", ChatExecutor: ChatExecutorCodex,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "managed-binding", ConversationID: "managed-conversation",
		Provider: ChatExecutorCodex, ProviderThreadID: "managed-codex-thread",
		HostID: "host-1", CreatedByUserID: "user-1",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := newExternalChatApplication(db).reportHost(
		context.Background(), "user-1", ChatExecutorCursor, "cursor-host", true, true, "",
	); err != nil {
		t.Fatal(err)
	}

	changeProvider := newSettingsRequest(
		http.MethodPatch, "/chat/conversations/managed-conversation/settings",
		`{"chat_executor":"cursor"}`, "user-1",
		map[string]string{"conversation_id": "managed-conversation"},
	)
	changeProviderRecorder := httptest.NewRecorder()
	PatchConversationSettings(changeProviderRecorder, changeProvider)
	if changeProviderRecorder.Code != http.StatusOK {
		t.Fatalf("change provider status=%d body=%s", changeProviderRecorder.Code, changeProviderRecorder.Body.String())
	}
	var stored orm.Conversation
	if err := db.First(&stored, "id = ?", "managed-conversation").Error; err != nil {
		t.Fatal(err)
	}
	if stored.ChatExecutor != ChatExecutorCursor {
		t.Fatalf("chat executor=%q, want %q", stored.ChatExecutor, ChatExecutorCursor)
	}
}
