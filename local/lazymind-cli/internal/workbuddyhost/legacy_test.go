package workbuddyhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRetireLegacyPluginsPreservesOtherSettingsAndBacksUpOnce(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "settings.json")
	original := []byte(`{"custom":{"number":1234567890123456789},"enabledPlugins":{"lazymind-workflow-panel@lazymind-workflow-panel-local":true,"lazymind-workflow-resume@lazymind-workflow-resume-local":true,"lazymind-workflow-panel@other":true,"other@official":true}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := retireLegacyPlugins(profile); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := os.ReadFile(path)
	var settings struct {
		Custom         json.RawMessage
		EnabledPlugins map[string]bool
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if string(settings.Custom) != "{\n    \"number\": 1234567890123456789\n  }" {
		t.Fatalf("custom setting changed: %s", settings.Custom)
	}
	for key, enabled := range settings.EnabledPlugins {
		want := key == "lazymind-workflow-panel@other" || key == "other@official"
		if enabled != want {
			t.Fatalf("unexpected setting %s=%v", key, enabled)
		}
	}
	backups, _ := filepath.Glob(filepath.Join(profile, "settings.json.before-lazymind-cdp-*"))
	if len(backups) != 1 {
		t.Fatalf("backups=%v", backups)
	}
	backup, _ := os.ReadFile(backups[0])
	if string(backup) != string(original) {
		t.Fatal("original settings were not preserved")
	}
}

func TestRetireLegacyPluginsLeavesMissingAndInvalidSettingsAlone(t *testing.T) {
	profile := t.TempDir()
	if err := retireLegacyPlugins(profile); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile, "settings.json")
	for _, body := range []string{`{broken`, `{"enabledPlugins":[]}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if retireLegacyPlugins(profile) == nil {
			t.Fatal("invalid settings accepted")
		}
		got, _ := os.ReadFile(path)
		if string(got) != body {
			t.Fatal("invalid settings were overwritten")
		}
	}
}
