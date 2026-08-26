package executorpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreDefaultsEnabledAndPersistsDisable(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := store.Enabled("codex"); err != nil || !enabled {
		t.Fatalf("default enabled=%v err=%v", enabled, err)
	}
	changed := store.Changes()
	status, err := store.SetEnabled("codex", false)
	if err != nil || status.Enabled {
		t.Fatalf("disable status=%#v err=%v", status, err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("policy change was not broadcast")
	}
	if info, err := os.Stat(filepath.Join(home, "executor-policy", "codex.disabled")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("marker info=%v err=%v", info, err)
	}

	reloaded, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := reloaded.Enabled("codex"); err != nil || enabled {
		t.Fatalf("reloaded enabled=%v err=%v", enabled, err)
	}
	if _, err := reloaded.SetEnabled("codex", true); err != nil {
		t.Fatal(err)
	}
	if enabled, err := reloaded.Enabled("codex"); err != nil || !enabled {
		t.Fatalf("re-enabled=%v err=%v", enabled, err)
	}
}

func TestStoreKeepsProvidersIndependent(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEnabled("cursor", false); err != nil {
		t.Fatal(err)
	}
	statuses, err := store.Statuses()
	if err != nil {
		t.Fatal(err)
	}
	if statuses["cursor"].Enabled || !statuses["codex"].Enabled || !statuses["workbuddy"].Enabled {
		t.Fatalf("statuses=%#v", statuses)
	}
	if _, err := store.SetEnabled("unknown", false); err == nil {
		t.Fatal("unsupported provider was accepted")
	}
}
