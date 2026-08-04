package plugin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// newHandlerTestDB creates a SQLite DB with all models needed by HTTP handlers.
func newHandlerTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(
		&orm.PluginDraft{},
		&orm.PluginResource{},
		&orm.PluginRevision{},
		&orm.PluginRevisionEntry{},
		&orm.PluginBlob{},
		&orm.UserPluginSetting{},
		&orm.PluginGenerationAnalysis{},
		&orm.PluginRepairRun{},
	); err != nil {
		t.Fatalf("auto migrate handler models: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	return db
}

// seedPluginDraft inserts a draft with valid YAML for validation tests.
func seedPluginDraft(t *testing.T, db *orm.DB, draftID, userID string) {
	t.Helper()
	now := time.Now().UTC()
	db.DB.Create(&orm.PluginDraft{
		ID: draftID, PluginID: "test-plugin", Name: "Test Plugin",
		CreatedBy: userID, Version: 1,
		PluginYAMLContent: "id: test-plugin\nslots:\n  - {id: out}\nsteps:\n  - {id: step_a, label: \"Do work\"}",
		StateYAMLContent:  "transitions:\n  __start__: [{to: __end__}]",
		ScenarioContent:   "",
		ScriptsContent:    "{}",
		CreatedAt:         now, UpdatedAt: now,
	})
}

// seedPluginResource inserts a minimal plugin resource for settings tests.
func seedPluginResource(t *testing.T, db *orm.DB, pluginRef, pluginID, userID string) {
	t.Helper()
	now := time.Now().UTC()
	db.DB.Create(&orm.PluginResource{
		PluginRef: pluginRef, PluginID: pluginID, Name: "Test Plugin",
		OwnerUserID: userID, Status: "active", RelativeRoot: pluginRef,
		HeadRevisionID: "rev-1", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	})
}

// jsonBody returns an io.Reader for a JSON string.
func jsonBody(s string) io.Reader {
	return strings.NewReader(s)
}

// testError2 is a simple error for testing error-matching functions.
type testError2 struct{ msg string }

func (e *testError2) Error() string { return e.msg }

// --- ValidatePluginDraft ---

func TestValidatePluginDraft_NotFound(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/drafts/nonexistent/validate", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": "nonexistent"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ValidatePluginDraft(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestValidatePluginDraft_ValidDraft(t *testing.T) {
	db := newHandlerTestDB(t)
	seedPluginDraft(t, db, "draft-1", "user-1")
	req := httptest.NewRequest(http.MethodPost, "/drafts/draft-1/validate", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": "draft-1"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ValidatePluginDraft(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["valid"] == nil {
		t.Fatalf("expected valid field in response: %s", rec.Body.String())
	}
}

// --- DisabledBuiltinPluginIDs ---

func TestDisabledBuiltinPluginIDs_Empty(t *testing.T) {
	db := newHandlerTestDB(t)
	ids, err := DisabledBuiltinPluginIDs(db.DB, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected empty, got %v", ids)
	}
}

func TestDisabledBuiltinPluginIDs_ReturnsDisabled(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	db.DB.Create(&orm.UserPluginSetting{
		UserID: "user-1", PluginRef: "builtin:bsk_01", Enabled: false,
		UpdatedAt: now,
	})
	db.DB.Create(&orm.UserPluginSetting{
		UserID: "user-1", PluginRef: "builtin:bsk_02", Enabled: true, CallMode: PluginCallModeAuto,
		UpdatedAt: now,
	})
	ids, err := DisabledBuiltinPluginIDs(db.DB, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "bsk_01" {
		t.Fatalf("got %v, want [bsk_01]", ids)
	}
}

// --- missingPluginTables ---

func TestMissingPluginTables(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"no such table: user_plugin_settings", true},
		{"relation \"user_plugin_settings\" does not exist", true},
		{"unknown error", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &testError2{msg: tt.errMsg}
			}
			if got := missingPluginTables(err); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// --- ListPluginVersions ---

func TestListPluginVersions_NotFound(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/plugins/nonexistent/versions", nil)
	req = mux.SetURLVars(req, map[string]string{"plugin_ref": "nonexistent"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListPluginVersions(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// --- PatchUserPluginSetting ---

func TestPatchUserPluginSetting_Unauthorized(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodPatch, "/plugins/test/settings", nil)
	req = mux.SetURLVars(req, map[string]string{"plugin_ref": "test"})
	rec := httptest.NewRecorder()
	PatchUserPluginSetting(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestPatchUserPluginSetting_ExistingPluginUpserts(t *testing.T) {
	db := newHandlerTestDB(t)
	seedPluginResource(t, db, "custom-plugin", "pid-custom", "user-1")
	body := jsonBody(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/plugins/custom-plugin/settings", body)
	req = mux.SetURLVars(req, map[string]string{"plugin_ref": "custom-plugin"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PatchUserPluginSetting(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestPatchUserPluginSetting_NonBuiltinNotFound(t *testing.T) {
	newHandlerTestDB(t)
	body := jsonBody(`{"enabled":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/plugins/unknown-ref/settings", body)
	req = mux.SetURLVars(req, map[string]string{"plugin_ref": "unknown-ref"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	PatchUserPluginSetting(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// --- GetPluginGenerationAnalysis ---

func TestGetPluginGenerationAnalysis_NotFound(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/drafts/d1/analysis", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": "d1"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetPluginGenerationAnalysis(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// --- GetPluginRepairRun ---

func TestGetPluginRepairRun_NotFound(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/drafts/d1/repairs/r1", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": "d1", "repair_id": "r1"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetPluginRepairRun(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}
