package executorpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
