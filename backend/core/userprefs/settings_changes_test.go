package userprefs

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
	"lazymind/core/settingsactivity"
	"lazymind/core/state"
	"lazymind/core/store"
)

func settingsChangeDB(t *testing.T) (*gorm.DB, state.Store) {
	t.Helper()
	db := newUIPreferencesTestDB(t).DB
	cache, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })
	return db, cache
}

func TestSettingsChangeIdleAndEnableApplyDirectly(t *testing.T) {
	db, cache := settingsChangeDB(t)
	for _, key := range []string{"developer_mode_active", "task_center_enabled", "schedules_enabled", "skills_enabled", "workflows_enabled", "mcp_enabled", "document_parsing_enabled"} {
		for _, enabled := range []bool{false, true} {
			result, err := applySettingsChange(context.Background(), db, nil, cache, "owner", SettingsChangeRequest{Key: key, Enabled: &enabled})
			if err != nil || !result.Applied || len(result.Impact.Tasks) != 0 || result.Preferences == nil {
				t.Fatalf("%s=%v: %+v %v", key, enabled, result, err)
			}
		}
	}
}

func TestSettingsChangeRechecksNewTasksAndKeepsExecution(t *testing.T) {
	db, cache := settingsChangeDB(t)
	seed := func(id, user, kind, status string) {
		t.Helper()
		if err := db.Create(&orm.SubAgentTask{ID: id, ConversationID: "c", Title: id, CreateUserID: user, AgentType: kind, Status: status, InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed("a", "owner", "research", "running")
	seed("foreign", "other", "research", "running")
	seed("workflow", "owner", "workflow_step", "running")
	seed("done", "owner", "research", "succeeded")
	seed("waiting", "owner", "research", "waiting")
	ctx := context.Background()
	impact, err := settingsImpact(ctx, db, nil, cache, "owner", "task_center_enabled", false)
	if err != nil || len(impact.Tasks) != 1 || impact.Tasks[0].ID != "subtask:a" {
		t.Fatalf("impact %+v %v", impact, err)
	}
	enabled := false
	req := SettingsChangeRequest{Key: "task_center_enabled", Enabled: &enabled}
	result, err := applySettingsChange(ctx, db, nil, cache, "owner", req)
	if err != nil || result.Applied {
		t.Fatalf("unconfirmed change: %+v %v", result, err)
	}
	req.ConfirmedTaskIDs = []string{"subtask:a"}
	seed("b", "owner", "research", "running")
	result, err = applySettingsChange(ctx, db, nil, cache, "owner", req)
	if err != nil || result.Applied || len(result.Impact.Tasks) != 2 {
		t.Fatalf("new task must require confirmation: %+v %v", result, err)
	}
	prefs, _ := LoadUserUIPreferences(ctx, db, "owner")
	if !prefs.TaskCenterEnabled {
		t.Fatal("unconfirmed change persisted")
	}
	req.ConfirmedTaskIDs = append(req.ConfirmedTaskIDs, "subtask:b")
	result, err = applySettingsChange(ctx, db, nil, cache, "owner", req)
	if err != nil || !result.Applied || result.Preferences.TaskCenterEnabled {
		t.Fatalf("confirmed change: %+v %v", result, err)
	}
	var task orm.SubAgentTask
	db.First(&task, "id = ?", "a")
	if task.Status != "running" {
		t.Fatal("setting changed running task")
	}
	// Replaying a completed save remains safe.
	if result, err = applySettingsChange(ctx, db, nil, cache, "owner", req); err != nil || !result.Applied {
		t.Fatalf("retry: %+v %v", result, err)
	}
}

func TestSettingsChangeActivityAndParsingAreCapabilitySpecific(t *testing.T) {
	db, cache := settingsChangeDB(t)
	ctx := context.Background()
	if err := db.Create(&orm.Conversation{ID: "c", DisplayName: "Active chat", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	entry, _ := json.Marshal(settingsactivity.Activity{ID: "call", RunID: "run", ConversationID: "c", Capability: "mcp_enabled", UpdatedAt: time.Now().Unix()})
	if err := cache.HSet(ctx, "settings:activity:owner", map[string]any{"call": string(entry), "second-call-in-same-run": string(entry)}, time.Hour); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{"mcp_enabled": 1, "skills_enabled": 0, "workflows_enabled": 0, "document_parsing_enabled": 0} {
		impact, err := settingsImpact(ctx, db, nil, cache, "owner", key, false)
		if err != nil || len(impact.Tasks) != want {
			t.Fatalf("%s %+v %v", key, impact, err)
		}
	}
	for id, status := range map[string]string{"parse": "RUNNING", "upload": "UPLOADED", "done": "SUCCEEDED"} {
		ext, _ := json.Marshal(map[string]string{"task_state": status})
		if err := db.Create(&orm.Task{ID: id, TaskType: "TASK_TYPE_PARSE_UPLOADED", Ext: ext, BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	impact, err := settingsImpact(ctx, db, nil, cache, "owner", "document_parsing_enabled", false)
	if err != nil || len(impact.Tasks) != 1 || impact.Tasks[0].ID != "parsing:parse" {
		t.Fatalf("parsing: %+v %v", impact, err)
	}
	if err := db.Model(&orm.Task{}).Where("id = ?", "parse").Update("lazyllm_task_id", "missing-external-state").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := settingsImpact(ctx, db, nil, cache, "owner", "document_parsing_enabled", false); err == nil {
		t.Fatal("missing external state treated as idle")
	}
}

type unavailableSettingsState struct {
	state.Store
	state.CompareAndDeleteStore
}

func (unavailableSettingsState) HGetAll(context.Context, string) (map[string]string, error) {
	return nil, errors.New("unavailable")
}

func TestSettingsChangeFailureDoesNotPersist(t *testing.T) {
	db, cache := settingsChangeDB(t)
	enabled := false
	_, err := applySettingsChange(context.Background(), db, nil, unavailableSettingsState{cache, cache.(state.CompareAndDeleteStore)}, "owner", SettingsChangeRequest{Key: "skills_enabled", Enabled: &enabled})
	if err == nil {
		t.Fatal("expected state error")
	}
	prefs, _ := LoadUserUIPreferences(context.Background(), db, "owner")
	if !prefs.SkillsEnabled {
		t.Fatal("failed check persisted")
	}
	// Exercise rollback after the preference write but before the bulk child update.
	if err := db.Callback().Update().Before("gorm:update").Register("fail_skill_update", func(tx *gorm.DB) {
		if tx.Statement.Table == "skills" {
			tx.AddError(errors.New("write failed"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err = applySettingsChange(context.Background(), db, nil, cache, "owner", SettingsChangeRequest{Key: "skills_enabled", Enabled: &enabled})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected update failure, got %v", err)
	}
	prefs, _ = LoadUserUIPreferences(context.Background(), db, "owner")
	if !prefs.SkillsEnabled {
		t.Fatal("transaction did not roll back")
	}
}

func TestSettingsChangeRejectsInvalidRequests(t *testing.T) {
	db, cache := settingsChangeDB(t)
	store.Init(db, nil, cache)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, body := range []string{`{"key":"other","enabled":false}`, `{"key":"skills_enabled"}`, `{"key":"skills_enabled","enabled":false,"user_id":"other"}`, `{"key":"skills_enabled","enabled":false} {}`} {
		r := httptest.NewRequest(http.MethodPost, "/api/core/settings/changes:apply", strings.NewReader(body))
		r.Header.Set("X-User-Id", "owner")
		w := httptest.NewRecorder()
		ApplySettingsChange(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s => %d", body, w.Code)
		}
	}
}

func TestSettingsChangeWorkflowScheduleAndDeveloperScopes(t *testing.T) {
	db, cache := settingsChangeDB(t)
	ctx := context.Background()
	seed := func(row any) {
		t.Helper()
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed(&orm.Conversation{ID: "c", DisplayName: "Work", BaseModel: orm.BaseModel{CreateUserID: "owner"}})
	seed(&orm.WorkflowSession{ID: "wf", ConversationID: "c", CreateUserID: "owner", WorkflowRef: "review", Status: "active"})
	seed(&orm.WorkflowSessionStep{ID: "old", SessionID: "wf", StepID: "step", Attempt: 1, Status: "running"})
	seed(&orm.WorkflowSessionStep{ID: "latest", SessionID: "wf", StepID: "step", Attempt: 2, Status: "succeeded"})
	impact, err := settingsImpact(ctx, db, nil, cache, "owner", "workflows_enabled", false)
	if err != nil || len(impact.Tasks) != 0 {
		t.Fatalf("superseded attempt %+v %v", impact, err)
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "latest").Update("status", "running").Error; err != nil {
		t.Fatal(err)
	}
	impact, err = settingsImpact(ctx, db, nil, cache, "owner", "workflows_enabled", false)
	if err != nil || len(impact.Tasks) != 1 || impact.Tasks[0].ID != "workflow:wf" {
		t.Fatalf("active workflow %+v %v", impact, err)
	}
	impact, err = settingsImpact(ctx, db, nil, cache, "owner", "task_center_enabled", false)
	if err != nil || len(impact.Tasks) != 0 {
		t.Fatalf("workflow coupled to subtasks %+v %v", impact, err)
	}
	future := time.Now().Add(time.Hour)
	seed(&orm.TaskCenterTask{ID: "future", ConversationID: "c", UserID: "owner", TaskType: "scheduled", Status: "pending", ScheduledFireAt: &future, CreatedAt: time.Now()})
	seed(&orm.TaskCenterTask{ID: "now", ConversationID: "c", UserID: "owner", TaskType: "scheduled", Status: "running", CreatedAt: time.Now()})
	impact, err = settingsImpact(ctx, db, nil, cache, "owner", "schedules_enabled", false)
	if err != nil || len(impact.Tasks) != 1 || impact.Tasks[0].ID != "schedule:now" {
		t.Fatalf("schedule %+v %v", impact, err)
	}
	impact, err = settingsImpact(ctx, db, nil, cache, "owner", "developer_mode_active", false)
	if err != nil || len(impact.Tasks) != 1 || impact.Tasks[0].ID != "chat:c" {
		t.Fatalf("developer %+v %v", impact, err)
	}
	impact, err = settingsImpact(ctx, db, nil, nil, "owner", "developer_mode_active", true)
	if err != nil || len(impact.Tasks) != 0 {
		t.Fatalf("enabling must not check runs %+v %v", impact, err)
	}
}
