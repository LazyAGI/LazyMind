package userprefs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

type uiPreferencesAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ChatPreferenceNoticeDismissed bool   `json:"chat_preference_notice_dismissed"`
		DeveloperModeActive           bool   `json:"developer_mode_active"`
		AcceptedUserAgreementVersion  string `json:"accepted_user_agreement_version"`
		TaskCenterEnabled             bool   `json:"task_center_enabled"`
		SkillsEnabled                 bool   `json:"skills_enabled"`
		WorkflowsEnabled              bool   `json:"workflows_enabled"`
		MCPEnabled                    bool   `json:"mcp_enabled"`
		DocumentParsingEnabled        bool   `json:"document_parsing_enabled"`
		UserPreferenceConfigured      bool   `json:"user_preference_configured"`
		UpdatedAt                     string `json:"updated_at"`
	} `json:"data"`
}

func newUIPreferencesTestDB(t *testing.T) *orm.DB {
	t.Helper()

	return orm.MigrateAllModelsForTest(t)
}

func decodeUIPreferencesResponse(t *testing.T, rec *httptest.ResponseRecorder) uiPreferencesAPITestResponse {
	t.Helper()

	var resp uiPreferencesAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestGetUIPreferencesDefaultsAndDerivedPreferenceStatus(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/api/core/user/ui-preferences", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	GetUIPreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeUIPreferencesResponse(t, rec)
	if resp.Data.ChatPreferenceNoticeDismissed || resp.Data.DeveloperModeActive || resp.Data.UserPreferenceConfigured ||
		!resp.Data.TaskCenterEnabled || !resp.Data.SkillsEnabled || !resp.Data.WorkflowsEnabled || !resp.Data.MCPEnabled ||
		!resp.Data.DocumentParsingEnabled {
		t.Fatalf("expected legacy flags false and feature controls true, got %#v", resp.Data)
	}

	seedUserPreferenceFile(t, db, "u1", `preferences:
  - name: pref.response.concise
    summary: Keep answers concise.
    ref: references/response-concise.md
    created_at: "2026-07-24T00:00:00Z"
    updated_at: "2026-07-24T00:00:00Z"
`)

	req = httptest.NewRequest(http.MethodGet, "/api/core/user/ui-preferences", nil)
	req.Header.Set("X-User-Id", "u1")
	rec = httptest.NewRecorder()

	GetUIPreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	resp = decodeUIPreferencesResponse(t, rec)
	if !resp.Data.UserPreferenceConfigured {
		t.Fatalf("expected user_preference_configured true")
	}
}

func TestPatchUIPreferencesPreservesOtherFeatureControls(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(`{"task_center_enabled":false,"mcp_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	PatchUIPreferences(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeUIPreferencesResponse(t, rec)
	if resp.Data.TaskCenterEnabled || resp.Data.MCPEnabled || !resp.Data.SkillsEnabled || resp.Data.WorkflowsEnabled ||
		!resp.Data.DocumentParsingEnabled {
		t.Fatalf("unexpected controls after patch: %#v", resp.Data)
	}

	secondReq := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(`{"skills_enabled":false}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("X-User-Id", "u1")
	secondRec := httptest.NewRecorder()
	PatchUIPreferences(secondRec, secondReq)
	secondResp := decodeUIPreferencesResponse(t, secondRec)
	if secondRec.Code != http.StatusOK || secondResp.Data.TaskCenterEnabled || secondResp.Data.MCPEnabled || secondResp.Data.SkillsEnabled ||
		secondResp.Data.WorkflowsEnabled || !secondResp.Data.DocumentParsingEnabled {
		t.Fatalf("feature controls should patch independently, got %#v status=%d", secondResp.Data, secondRec.Code)
	}

	thirdReq := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(`{"document_parsing_enabled":false}`))
	thirdReq.Header.Set("Content-Type", "application/json")
	thirdReq.Header.Set("X-User-Id", "u1")
	thirdRec := httptest.NewRecorder()
	PatchUIPreferences(thirdRec, thirdReq)
	thirdResp := decodeUIPreferencesResponse(t, thirdRec)
	if thirdRec.Code != http.StatusOK || thirdResp.Data.TaskCenterEnabled || thirdResp.Data.MCPEnabled || thirdResp.Data.SkillsEnabled ||
		thirdResp.Data.WorkflowsEnabled || thirdResp.Data.DocumentParsingEnabled {
		t.Fatalf("document parsing control should patch independently, got %#v status=%d", thirdResp.Data, thirdRec.Code)
	}
}

func TestPatchUIPreferencesTaskCenterControlsWorkflowAvailability(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()

	resources := []orm.WorkflowResource{
		{ID: "workflow-user", WorkflowRef: "user:u1", WorkflowID: "user", OwnerUserID: "u1", OwnerScope: "user:u1", RelativeRoot: "workflows/user", Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: "workflow-builtin", WorkflowRef: "builtin:shared", WorkflowID: "shared", OwnerScope: "builtin", RelativeRoot: "workflows/shared", Status: "active", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&resources).Error; err != nil {
		t.Fatalf("seed workflows: %v", err)
	}

	patch := func(body string) uiPreferencesAPITestResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", "u1")
		rec := httptest.NewRecorder()
		PatchUIPreferences(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %s: status=%d body=%s", body, rec.Code, rec.Body.String())
		}
		return decodeUIPreferencesResponse(t, rec)
	}
	assertWorkflowsDisabled := func() {
		t.Helper()
		var settings []orm.UserWorkflowSetting
		if err := db.Where("user_id = ?", "u1").Order("plugin_ref").Find(&settings).Error; err != nil {
			t.Fatalf("load workflow settings: %v", err)
		}
		if len(settings) != 2 {
			t.Fatalf("expected both available workflows to be synchronized, got %#v", settings)
		}
		for _, setting := range settings {
			if setting.Enabled || setting.CallMode != workflow.WorkflowCallModeDisabled {
				t.Fatalf("workflow should be disabled with task center off, got %#v", setting)
			}
		}
	}

	resp := patch(`{"task_center_enabled":false}`)
	if resp.Data.TaskCenterEnabled || resp.Data.WorkflowsEnabled {
		t.Fatalf("disabling task center must also disable workflows, got %#v", resp.Data)
	}
	assertWorkflowsDisabled()

	resp = patch(`{"workflows_enabled":true}`)
	if resp.Data.WorkflowsEnabled {
		t.Fatalf("workflows cannot be enabled while task center is disabled, got %#v", resp.Data)
	}
	assertWorkflowsDisabled()

	resp = patch(`{"task_center_enabled":true}`)
	if !resp.Data.TaskCenterEnabled || resp.Data.WorkflowsEnabled {
		t.Fatalf("re-enabling task center must not re-enable workflows automatically, got %#v", resp.Data)
	}
	assertWorkflowsDisabled()
}

func TestPatchUIPreferencesBulkControlsSkillsAndWorkflowsIndependently(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()

	seedSkill := func(id, owner, root string, enabled bool) {
		t.Helper()
		if err := db.Model(&orm.SkillV2Skill{}).Create(map[string]any{
			"id": id, "owner_user_id": owner, "create_user_id": owner,
			"category": "internal", "skill_name": id, "relative_root": root,
			"is_enabled": enabled, "created_at": now, "updated_at": now,
		}).Error; err != nil {
			t.Fatalf("seed skill %s: %v", id, err)
		}
	}
	seedSkill("skill-u1-on", "u1", "skills/u1/on", true)
	seedSkill("skill-u1-off", "u1", "skills/u1/off", false)
	seedSkill("skill-u2-on", "u2", "skills/u2/on", true)

	workflows := []orm.WorkflowResource{
		{ID: "workflow-u1", WorkflowRef: "user:u1", WorkflowID: "u1", OwnerUserID: "u1", OwnerScope: "user:u1", RelativeRoot: "workflows/u1", Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: "workflow-shared", WorkflowRef: "builtin:shared", WorkflowID: "shared", OwnerUserID: "", OwnerScope: "builtin", RelativeRoot: "workflows/shared", Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: "workflow-u2", WorkflowRef: "user:u2", WorkflowID: "u2", OwnerUserID: "u2", OwnerScope: "user:u2", RelativeRoot: "workflows/u2", Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: "workflow-archived", WorkflowRef: "user:u1:archived", WorkflowID: "archived", OwnerUserID: "u1", OwnerScope: "user:u1", RelativeRoot: "workflows/u1-archived", Status: "archived", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&workflows).Error; err != nil {
		t.Fatalf("seed workflows: %v", err)
	}
	if err := db.Create(&orm.UserWorkflowSetting{UserID: "u1", WorkflowRef: "user:u1", Enabled: true, CallMode: workflow.WorkflowCallModeAuto, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed workflow setting: %v", err)
	}

	patch := func(field string, enabled bool) uiPreferencesAPITestResponse {
		t.Helper()
		body := `{"` + field + `":` + map[bool]string{true: "true", false: "false"}[enabled] + `}`
		req := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", "u1")
		rec := httptest.NewRecorder()
		PatchUIPreferences(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %s=%v: status=%d body=%s", field, enabled, rec.Code, rec.Body.String())
		}
		return decodeUIPreferencesResponse(t, rec)
	}
	assertSkills := func(enabled bool) {
		t.Helper()
		var userSkills []orm.SkillV2Skill
		if err := db.Where("owner_user_id = ?", "u1").Order("id").Find(&userSkills).Error; err != nil {
			t.Fatalf("load user skills: %v", err)
		}
		if len(userSkills) != 2 || userSkills[0].IsEnabled != enabled || userSkills[1].IsEnabled != enabled {
			t.Fatalf("user skills should all be enabled=%v, got %#v", enabled, userSkills)
		}
		var otherSkill orm.SkillV2Skill
		if err := db.Where("id = ?", "skill-u2-on").Take(&otherSkill).Error; err != nil || !otherSkill.IsEnabled {
			t.Fatalf("other user's skill should remain enabled, row=%#v err=%v", otherSkill, err)
		}
	}
	assertWorkflows := func(enabled bool, expectedCount int) {
		t.Helper()
		expectedCallMode := workflow.WorkflowCallModeDisabled
		if enabled {
			expectedCallMode = workflow.WorkflowCallModeAuto
		}
		var settings []orm.UserWorkflowSetting
		if err := db.Where("user_id = ?", "u1").Order("plugin_ref").Find(&settings).Error; err != nil {
			t.Fatalf("load workflow settings: %v", err)
		}
		if len(settings) != expectedCount {
			t.Fatalf("expected %d workflow settings, got %#v", expectedCount, settings)
		}
		for _, setting := range settings {
			if setting.Enabled != enabled || setting.CallMode != expectedCallMode {
				t.Fatalf("workflow %s should be enabled=%v call_mode=%s, got %#v", setting.WorkflowRef, enabled, expectedCallMode, setting)
			}
		}
	}

	resp := patch("skills_enabled", false)
	assertSkills(false)
	assertWorkflows(true, 1)
	if resp.Data.SkillsEnabled || !resp.Data.WorkflowsEnabled {
		t.Fatalf("skill bulk change must preserve workflow control, got %#v", resp.Data)
	}

	resp = patch("workflows_enabled", false)
	assertSkills(false)
	assertWorkflows(false, 2)
	if resp.Data.SkillsEnabled || resp.Data.WorkflowsEnabled {
		t.Fatalf("workflow bulk change must preserve skill control, got %#v", resp.Data)
	}

	patch("skills_enabled", true)
	assertSkills(true)
	assertWorkflows(false, 2)

	patch("workflows_enabled", true)
	assertSkills(true)
	assertWorkflows(true, 2)
}

func TestReenablingTaskCenterMovesSchedulesForwardWithoutChangingHistory(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	schedule := orm.UserSchedule{
		ID: "sched-1", UserID: "u1", CronExpr: "*/10 * * * *", Timezone: "UTC", PromptTemplate: "daily", Enabled: true,
		NextRunAt: now.Add(-time.Hour), RunCount: 7, CreatedAt: now.Add(-time.Hour),
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	for _, body := range []string{`{"task_center_enabled":false}`, `{"task_center_enabled":true}`} {
		req := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", "u1")
		rec := httptest.NewRecorder()
		PatchUIPreferences(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %s: status=%d body=%s", body, rec.Code, rec.Body.String())
		}
	}
	var saved orm.UserSchedule
	if err := db.Where("id = ?", schedule.ID).Take(&saved).Error; err != nil {
		t.Fatalf("load schedule: %v", err)
	}
	if !saved.NextRunAt.After(now) {
		t.Fatalf("expected next run in the future, got %s", saved.NextRunAt)
	}
	if saved.RunCount != schedule.RunCount || saved.LastRunAt != nil {
		t.Fatalf("resume must not backfill or alter history: %#v", saved)
	}
}

func TestSettingsOverviewExposesRawAndEffectiveStates(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.UserSchedule{
		ID: "sched-overview", UserID: "u1", CronExpr: "0 9 * * *", Timezone: "UTC", PromptTemplate: "daily", Enabled: true,
		NextRunAt: now.Add(time.Hour), CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "u1", "task_center_enabled": false, "skills_enabled": true, "mcp_enabled": true, "created_at": now, "updated_at": now}).Error; err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	overview, err := buildSettingsOverview(httptest.NewRequest(http.MethodGet, "/settings/overview", nil), db.DB, "u1")
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}
	if overview.Controls.TaskCenterEnabled {
		t.Fatal("expected task center control to be disabled")
	}
	var taskSection settingsOverviewSection
	for _, section := range overview.Sections {
		if section.ID == "tasks" {
			taskSection = section
			break
		}
	}
	if taskSection.RawEnabled == nil || *taskSection.RawEnabled || taskSection.EffectiveEnabled == nil || *taskSection.EffectiveEnabled || taskSection.Counts.Enabled != 1 {
		t.Fatalf("unexpected task section: %#v", taskSection)
	}
}

func seedUserPreferenceFile(t *testing.T, db *orm.DB, userID, content string) {
	t.Helper()

	now := time.Now()
	if err := db.Create(&orm.MemoryCurrentEntry{
		UserID:    userID,
		Path:      "memory/users/preference.yaml",
		EntryType: "file",
		Content:   []byte(content),
		Size:      int64(len([]byte(content))),
		Mime:      "application/yaml; charset=utf-8",
		FileType:  "yaml",
		Binary:    false,
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create current preference file: %v", err)
	}
}

func TestPatchUIPreferencesPartiallyUpdatesProvidedFields(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	firstReq := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(`{"chat_preference_notice_dismissed":true}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.Header.Set("X-User-Id", "u1")
	firstRec := httptest.NewRecorder()

	PatchUIPreferences(firstRec, firstReq)

	if firstRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", firstRec.Code, firstRec.Body.String())
	}
	firstResp := decodeUIPreferencesResponse(t, firstRec)
	if !firstResp.Data.ChatPreferenceNoticeDismissed || firstResp.Data.DeveloperModeActive {
		t.Fatalf("unexpected first response: %#v", firstResp.Data)
	}

	secondReq := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(`{"developer_mode_active":true}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("X-User-Id", "u1")
	secondRec := httptest.NewRecorder()

	PatchUIPreferences(secondRec, secondReq)

	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", secondRec.Code, secondRec.Body.String())
	}
	secondResp := decodeUIPreferencesResponse(t, secondRec)
	if !secondResp.Data.ChatPreferenceNoticeDismissed || !secondResp.Data.DeveloperModeActive {
		t.Fatalf("expected second patch to keep dismissed and set developer active, got %#v", secondResp.Data)
	}

	thirdReq := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(`{"developer_mode_active":false}`))
	thirdReq.Header.Set("Content-Type", "application/json")
	thirdReq.Header.Set("X-User-Id", "u1")
	thirdRec := httptest.NewRecorder()

	PatchUIPreferences(thirdRec, thirdReq)

	if thirdRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", thirdRec.Code, thirdRec.Body.String())
	}
	thirdResp := decodeUIPreferencesResponse(t, thirdRec)
	if !thirdResp.Data.ChatPreferenceNoticeDismissed || thirdResp.Data.DeveloperModeActive {
		t.Fatalf("expected false value to update without clearing dismissed, got %#v", thirdResp.Data)
	}
}

func TestPatchUIPreferencesPersistsAcceptedUserAgreementVersion(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	patchReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/core/user/ui-preferences",
		strings.NewReader(`{"accepted_user_agreement_version":" V0.2 "}`),
	)
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("X-User-Id", "u1")
	patchRec := httptest.NewRecorder()

	PatchUIPreferences(patchRec, patchReq)

	if patchRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", patchRec.Code, patchRec.Body.String())
	}
	patchResp := decodeUIPreferencesResponse(t, patchRec)
	if patchResp.Data.AcceptedUserAgreementVersion != "V0.2" {
		t.Fatalf("expected trimmed agreement version V0.2, got %q", patchResp.Data.AcceptedUserAgreementVersion)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/core/user/ui-preferences", nil)
	getReq.Header.Set("X-User-Id", "u1")
	getRec := httptest.NewRecorder()

	GetUIPreferences(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", getRec.Code, getRec.Body.String())
	}
	getResp := decodeUIPreferencesResponse(t, getRec)
	if getResp.Data.AcceptedUserAgreementVersion != "V0.2" {
		t.Fatalf("expected persisted agreement version V0.2, got %q", getResp.Data.AcceptedUserAgreementVersion)
	}
}

func TestUIPreferencesHandlersRejectMissingUserIdentity(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	tests := []struct {
		name    string
		handler http.HandlerFunc
		request *http.Request
	}{
		{
			name:    "get",
			handler: GetUIPreferences,
			request: httptest.NewRequest(http.MethodGet, "/api/core/user/ui-preferences", nil),
		},
		{
			name:    "patch",
			handler: PatchUIPreferences,
			request: httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(`{"accepted_user_agreement_version":"V0.2"}`)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.handler(rec, tt.request)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPatchUIPreferencesRejectsEmptyPatch(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodPatch, "/api/core/user/ui-preferences", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	PatchUIPreferences(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.Model(&orm.UserUIPreferences{}).Where("user_id = ?", "u1").Count(&count).Error; err != nil {
		t.Fatalf("count user ui preferences: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty patch should not create row, got count %d", count)
	}
}
