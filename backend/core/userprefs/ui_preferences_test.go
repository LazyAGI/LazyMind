package userprefs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type uiPreferencesAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ChatPreferenceNoticeDismissed bool   `json:"chat_preference_notice_dismissed"`
		DeveloperModeActive           bool   `json:"developer_mode_active"`
		UserPreferenceConfigured      bool   `json:"user_preference_configured"`
		UpdatedAt                     string `json:"updated_at"`
	} `json:"data"`
}

func newUIPreferencesTestDB(t *testing.T) *orm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := orm.Connect(orm.DriverSQLite, dbPath)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
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
	if resp.Data.ChatPreferenceNoticeDismissed || resp.Data.DeveloperModeActive || resp.Data.UserPreferenceConfigured {
		t.Fatalf("expected all default booleans false, got %#v", resp.Data)
	}

	now := time.Now()
	if err := db.Create(&orm.SystemUserPreference{
		ID:            "pref-1",
		UserID:        "u1",
		AgentPersona:  "严谨助手",
		ContentHash:   "hash",
		Version:       1,
		AutoEvo:       true,
		CreatedAt:     now,
		UpdatedAt:     now,
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
	}).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}

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
