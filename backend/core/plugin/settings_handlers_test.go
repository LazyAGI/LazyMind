package plugin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
)

func TestPluginRefPathVarPreservesColon(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "colon", path: "/chat/settings/plugins/builtin:image-plugin"},
		{name: "slash alias", path: "/chat/settings/plugins/builtin/image-plugin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := mux.NewRouter()
			router.UseEncodedPath()
			router.HandleFunc("/chat/settings/plugins/{plugin_ref:.+}", func(w http.ResponseWriter, r *http.Request) {
				if got := pluginSettingRefPathVar(r); got != "builtin:image-plugin" {
					t.Errorf("pluginSettingRefPathVar() = %q, want %q", got, "builtin:image-plugin")
				}
			}).Methods(http.MethodPatch)

			req := httptest.NewRequest(http.MethodPatch, tc.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("route status = %d, want %d", resp.Code, http.StatusOK)
			}
		})
	}
}

func TestNormalizePluginCallModeKeepsLegacyEnabledCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		enabled bool
		want    string
	}{
		{name: "legacy enabled", enabled: true, want: PluginCallModeAuto},
		{name: "legacy disabled", enabled: false, want: PluginCallModeDisabled},
		{name: "manual", mode: PluginCallModeManual, enabled: true, want: PluginCallModeManual},
		{name: "unknown enabled", mode: "unknown", enabled: true, want: PluginCallModeAuto},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePluginCallMode(tt.mode, tt.enabled); got != tt.want {
				t.Fatalf("normalizePluginCallMode(%q, %v) = %q, want %q", tt.mode, tt.enabled, got, tt.want)
			}
		})
	}
}

func TestPluginCallModeEnabled(t *testing.T) {
	if !pluginCallModeEnabled(PluginCallModeAuto) || !pluginCallModeEnabled(PluginCallModeManual) {
		t.Fatal("auto and manual call modes must remain callable")
	}
	if pluginCallModeEnabled(PluginCallModeDisabled) {
		t.Fatal("disabled call mode must not remain callable")
	}
}

func TestPluginCallModeDatabaseChecksSQLite(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "plugin-call-mode.db"))
	if err != nil {
		t.Fatalf("connect SQLite database: %v", err)
	}
	if err := db.AutoMigrate(&orm.UserPluginSetting{}, &orm.PluginResource{}, &orm.PluginRevision{}); err != nil {
		t.Fatalf("migrate plugin call-mode tables: %v", err)
	}

	now := time.Now().UTC()
	plugins := []struct {
		ref  string
		mode string
	}{
		{ref: "user:auto", mode: PluginCallModeAuto},
		{ref: "user:manual", mode: PluginCallModeManual},
		{ref: "user:disabled", mode: PluginCallModeDisabled},
	}
	for index, item := range plugins {
		resourceID := "resource-" + item.mode
		revisionID := "revision-" + item.mode
		if err := db.Create(&orm.PluginResource{
			ID:             resourceID,
			PluginRef:      item.ref,
			PluginID:       "plugin-" + item.mode,
			OwnerUserID:    "user-1",
			OwnerScope:     "user",
			SourceType:     "user",
			RelativeRoot:   "root-" + item.mode,
			Name:           item.mode,
			Status:         "active",
			HeadRevisionID: revisionID,
			Version:        int64(index + 1),
			CreatedAt:      now,
			UpdatedAt:      now,
		}).Error; err != nil {
			t.Fatalf("create plugin %s: %v", item.ref, err)
		}
		if err := db.Create(&orm.PluginRevision{
			ID:               revisionID,
			PluginResourceID: resourceID,
			RevisionNo:       1,
			TreeHash:         "hash-" + item.mode,
			CreatedAt:        now,
		}).Error; err != nil {
			t.Fatalf("create plugin revision %s: %v", item.ref, err)
		}
		if err := db.Create(&orm.UserPluginSetting{
			UserID:    "user-1",
			PluginRef: item.ref,
			Enabled:   item.mode != PluginCallModeDisabled,
			CallMode:  item.mode,
			UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("create plugin setting %s: %v", item.ref, err)
		}
	}
	// Keep an inconsistent row to verify call-time validation uses call_mode,
	// not only the legacy enabled flag.
	if err := db.Create(&orm.UserPluginSetting{
		UserID:    "user-1",
		PluginRef: "builtin:paused",
		Enabled:   true,
		CallMode:  PluginCallModeDisabled,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create inconsistent builtin setting: %v", err)
	}
	if err := db.Create(&orm.UserPluginSetting{
		UserID:    "user-1",
		PluginRef: "builtin:manual",
		Enabled:   true,
		CallMode:  PluginCallModeManual,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create manual builtin setting: %v", err)
	}

	for _, item := range plugins {
		got, err := UserPluginCallMode(db.DB, "user-1", item.ref)
		if err != nil {
			t.Fatalf("read call mode %s: %v", item.ref, err)
		}
		if got != item.mode {
			t.Fatalf("read call mode %s=%q, want %q", item.ref, got, item.mode)
		}
	}
	disabled, err := DisabledBuiltinPluginIDs(db.DB, "user-1")
	if err != nil {
		t.Fatalf("read disabled builtin plugins: %v", err)
	}
	if len(disabled) != 1 || disabled[0] != "paused" {
		t.Fatalf("disabled builtin plugins=%v, want [paused]", disabled)
	}
	manual, err := ManualBuiltinPluginIDs(db.DB, "user-1")
	if err != nil {
		t.Fatalf("read manual builtin plugins: %v", err)
	}
	if len(manual) != 1 || manual[0] != "manual" {
		t.Fatalf("manual builtin plugins=%v, want [manual]", manual)
	}

	catalog, err := EnabledCatalog(db.DB, "user-1")
	if err != nil {
		t.Fatalf("read enabled plugin catalog: %v", err)
	}
	if len(catalog) != 2 {
		t.Fatalf("enabled plugin catalog count=%d, want 2", len(catalog))
	}
	for _, item := range catalog {
		if item["call_mode"] == PluginCallModeDisabled {
			t.Fatalf("disabled plugin leaked into enabled catalog: %#v", item)
		}
	}
}
