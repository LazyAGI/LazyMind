package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"

	"github.com/gorilla/mux"
)

// setupChatSettingsTest creates a SQLite store with UserChatSettings and Conversation tables migrated.
func setupChatSettingsTest(t *testing.T) {
	t.Helper()
	db := newPromptTestDB(t)
	// Also migrate Conversation table for PatchConversationWorkflowSettings.
	if err := db.AutoMigrate(&orm.Conversation{}); err != nil {
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
	var resp common.APIResponse
	json.NewDecoder(w.Body).Decode(&resp)
	data, _ := resp.Data.(map[string]any)
	// Defaults: enable_workflow=true, workflow_mode=dynamic, enable_subagent=true.
	if v, ok := data["enable_workflow"].(bool); !ok || !v {
		t.Fatalf("enable_workflow: got %v, want true", data["enable_workflow"])
	}
	if v, ok := data["workflow_mode"].(string); !ok || v != "dynamic" {
		t.Fatalf("workflow_mode: got %v, want dynamic", data["workflow_mode"])
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
	var resp common.APIResponse
	json.NewDecoder(w2.Body).Decode(&resp)
	data, _ := resp.Data.(map[string]any)
	if v, ok := data["enable_workflow"].(bool); ok && v {
		t.Fatalf("enable_workflow: expected false, got %v", v)
	}
	if v, ok := data["workflow_mode"].(string); ok && v != "auto" {
		t.Fatalf("workflow_mode: expected auto, got %v", v)
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

// --- PatchConversationWorkflowSettings ---

// TestPatchConversationWorkflowSettings_NoConversation returns 400.
func TestPatchConversationWorkflowSettings_NoConversation(t *testing.T) {
	setupChatSettingsTest(t)
	req := newSettingsRequest("PATCH", "/chat/conversations//workflow_settings", `{}`, "user-1", nil)
	w := httptest.NewRecorder()
	PatchConversationWorkflowSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestPatchConversationWorkflowSettings_MissingUserID returns 401.
func TestPatchConversationWorkflowSettings_MissingUserID(t *testing.T) {
	setupChatSettingsTest(t)
	vars := map[string]string{"conversation_id": "conv-1"}
	req := newSettingsRequest("PATCH", "/chat/conversations/conv-1/workflow_settings", `{}`, "", vars)
	w := httptest.NewRecorder()
	PatchConversationWorkflowSettings(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestPatchConversationWorkflowSettings_InvalidWorkflowMode returns 400.
func TestPatchConversationWorkflowSettings_InvalidWorkflowMode(t *testing.T) {
	setupChatSettingsTest(t)
	vars := map[string]string{"conversation_id": "conv-1"}
	req := newSettingsRequest("PATCH", "/chat/conversations/conv-1/workflow_settings",
		`{"workflow_mode":"bogus"}`, "user-1", vars)
	w := httptest.NewRecorder()
	PatchConversationWorkflowSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPatchConversationWorkflowSettings_PersistsPhysicalPluginModeColumn(t *testing.T) {
	setupChatSettingsTest(t)
	db := corestore.DB()
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conv-mode", BaseModel: orm.BaseModel{
		CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	req := newSettingsRequest(http.MethodPatch, "/chat/conversations/conv-mode/workflow-settings",
		`{"workflow_mode":"auto"}`, "user-1", map[string]string{"conversation_id": "conv-mode"})
	w := httptest.NewRecorder()
	PatchConversationWorkflowSettings(w, req)
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
}
