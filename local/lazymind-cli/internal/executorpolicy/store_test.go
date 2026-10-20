package executorpolicy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStatusSerializesNegativeProbeResults(t *testing.T) {
	body, err := json.Marshal(Status{Provider: "codex", Installed: new(bool), Ready: new(bool), BridgeState: BridgeReady})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"installed", "ready"} {
		if value, exists := decoded[field]; !exists || value != false {
			t.Fatalf("%s must be explicitly false so clients do not reuse stale host state: %s", field, body)
		}
	}
}

func TestPolicyActionOmitsUnknownProbeResults(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.SetEnabled("codex", true)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"installed", "ready"} {
		if _, exists := decoded[field]; exists {
			t.Fatalf("policy-only action must not overwrite the client's probe result: %s", body)
		}
	}
}

func TestStoreDefaultsDisabledAndPersistsExplicitEnable(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := store.Enabled("codex"); err != nil || enabled {
		t.Fatalf("default enabled=%v err=%v", enabled, err)
	}
	changed := store.Changes()
	status, err := store.SetEnabled("codex", true)
	if err != nil || !status.Enabled {
		t.Fatalf("enable status=%#v err=%v", status, err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("policy change was not broadcast")
	}
	info, err := os.Stat(filepath.Join(home, "executor-policy", "codex.enabled"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("marker permissions=%o", info.Mode().Perm())
	}

	legacyDisabled := filepath.Join(home, "executor-policy", "codex.disabled")
	if err := os.WriteFile(legacyDisabled, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := reloaded.Enabled("codex"); err != nil || !enabled {
		t.Fatalf("reloaded enabled=%v err=%v", enabled, err)
	}
	if _, err := os.Stat(legacyDisabled); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy policy marker was not removed: %v", err)
	}
	if _, err := reloaded.SetEnabled("codex", false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := reloaded.Enabled("codex"); err != nil || enabled {
		t.Fatalf("disabled=%v err=%v", enabled, err)
	}
	enabledMarker := filepath.Join(home, "executor-policy", "codex.enabled")
	if _, err := os.Stat(enabledMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("enabled policy marker was not removed: %v", err)
	}
}

func TestStoreKeepsProvidersIndependent(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEnabled("cursor", true); err != nil {
		t.Fatal(err)
	}
	statuses, err := store.Statuses()
	if err != nil {
		t.Fatal(err)
	}
	if !statuses["cursor"].Enabled || statuses["codex"].Enabled || statuses["workbuddy"].Enabled {
		t.Fatalf("statuses=%#v", statuses)
	}
	if _, err := store.SetEnabled("unknown", false); err == nil {
		t.Fatal("unsupported provider was accepted")
	}
}

func TestStoreRecheckBroadcastsWithoutChangingPolicy(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEnabled("cursor", true); err != nil {
		t.Fatal(err)
	}
	changed := store.Changes()
	store.Recheck()
	select {
	case <-changed:
	default:
		t.Fatal("recheck was not broadcast")
	}
	if enabled, err := store.Enabled("cursor"); err != nil || !enabled {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
}
