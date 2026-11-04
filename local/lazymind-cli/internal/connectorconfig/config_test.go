package connectorconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestWorkflowInputRootsSurviveReconnectAndRejectRelativePaths(t *testing.T) {
	c := Config{Home: t.TempDir(), Provider: "cursor", HostID: "host", WorkflowInputRoots: []string{t.TempDir(), t.TempDir()}}
	path, err := Save(c)
	if err != nil {
		t.Fatal(err)
	}
	// Reconnection rewrites identity fields but preserves the user's file policy.
	if _, err := Save(Config{Home: c.Home, Provider: c.Provider, HostID: c.HostID}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got.WorkflowInputRoots, c.WorkflowInputRoots) {
		t.Fatalf("input roots lost: %+v %v", got, err)
	}
	got.WorkflowInputRoots = []string{"relative/project"}
	body, _ := json.Marshal(got)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "workflow_input_roots") {
		t.Fatalf("relative authorization root accepted: %v", err)
	}
}

func TestManagedConfigurationIgnoresInheritedOverridesAndReadsUpdates(t *testing.T) {
	home := t.TempDir()
	path, err := Save(Config{Home: home, Provider: "codex", HostID: "host", Profile: t.TempDir(), Controlled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvironmentKey, path)
	for name := range overrideKeys {
		t.Setenv(name, "stale-invalid-value")
		if got := Value(name); got != "" {
			t.Fatalf("%s inherited %q", name, got)
		}
	}
	for _, name := range []string{"LAZYMIND_SERVER_URL", "LAZYMIND_RUNTIME_STATE_FILE"} {
		t.Setenv(name, "stale")
		if Value(name) != "" {
			t.Fatal(name)
		}
	}
	if Value("LAZYMIND_HOME") != home || Value("LAZYMIND_AGENT_PROVIDER") != "codex" {
		t.Fatal("lost identity")
	}
	c, _ := Load(path)
	c.Overrides = map[string]string{"LAZYMIND_WEB_URL": "https://panel.example/prefix"}
	// Save preserves deliberately edited overrides on reinstall; first write models that edit.
	body := `{"version":1,"home":` + strconv.Quote(c.Home) + `,"provider":"codex","host_id":"host","profile":` + strconv.Quote(c.Profile) + `,"overrides":{"LAZYMIND_WEB_URL":"https://panel.example/prefix"}}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := URL("LAZYMIND_WEB_URL", "http://localhost:8090"); err != nil || got != "https://panel.example/prefix" {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := Save(Config{Home: home, Provider: "codex", HostID: "host", Profile: c.Profile, Controlled: true}); err != nil {
		t.Fatal(err)
	}
	if Value("LAZYMIND_WEB_URL") != "https://panel.example/prefix" {
		t.Fatal("manual override lost")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := URL("LAZYMIND_WEB_URL", "http://localhost:8090"); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("malformed configuration hidden: %v", err)
	}
	if _, err := Save(c); err == nil {
		t.Fatal("overwrote malformed configuration")
	}
}
func TestManualAndDesktopModes(t *testing.T) {
	t.Setenv(EnvironmentKey, "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_WEB_URL", "file:///stale")
	if _, err := URL("LAZYMIND_WEB_URL", "http://localhost:8090"); err == nil {
		t.Fatal("invalid manual URL accepted")
	}
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
	if got, err := URL("LAZYMIND_WEB_URL", "http://localhost:8091"); err != nil || got != "http://localhost:8091" {
		t.Fatalf("desktop inherited override: %q %v", got, err)
	}
	for _, bad := range []string{"http://example.com:9999", "http://127.0.0.1:9999/path", "https://localhost:9999", "http://user@localhost:9999"} {
		if ValidateURL("LAZYMIND_ASSISTANT_BRIDGE_URL", bad) == nil {
			t.Fatal(bad)
		}
	}
}
func TestConfigurationIdentityAndServiceIsolation(t *testing.T) {
	home := t.TempDir()
	profile := t.TempDir()
	c := Config{Home: home, Provider: "deepseek-harness", HostID: "host", Profile: profile, PairingFile: filepath.Join(home, "workflow-hosts", "pair.json"), Controlled: true}
	path, err := Save(c)
	if err != nil {
		t.Fatal(err)
	}
	if path == Path(home, c.Provider, t.TempDir()) {
		t.Fatal("profiles share a descriptor")
	}
	t.Setenv(EnvironmentKey, path)
	t.Setenv("LAZYMIND_AGENT_PROVIDER", "codex")
	t.Setenv("LAZYMIND_WORKFLOW_PAIRING_FILE", "wrong")
	env := strings.Join(ServiceEnvironment(home), "\n")
	if strings.Contains(env, EnvironmentKey+"=") || strings.Contains(env, "LAZYMIND_AGENT_PROVIDER=") || strings.Contains(env, "LAZYMIND_WORKFLOW_PAIRING_FILE=") {
		t.Fatal("shared service inherited Agent identity")
	}
	c.PairingFile = filepath.Join(t.TempDir(), "pair.json")
	c.Version = 1
	if c.Validate() == nil {
		t.Fatal("foreign pairing accepted")
	}
}
