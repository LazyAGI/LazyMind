package workbuddyhost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func testWindowsStartupEnvironment(t *testing.T) windowsStartupEnvironment {
	t.Helper()
	key := fmt.Sprintf(`Software\LazyMindTests\WorkBuddyStartup-%d`, time.Now().UnixNano())
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, key) })
	return windowsStartupEnvironment{keyPath: key, name: desktopDebugPortEnv, notify: func() error { return nil }}
}

func TestWindowsStartupPersistsAcrossInstancesAndRestoresPreviousValue(t *testing.T) {
	env := testWindowsStartupEnvironment(t)
	home := t.TempDir()
	t.Setenv(desktopDebugPortEnv, "stale-process-value")
	if err := env.Set("9444"); err != nil {
		t.Fatal(err)
	}
	if err := updateStartupEnvironment(home, 9333, true, env); err != nil {
		t.Fatal(err)
	}
	// A fresh adapter reads the persistent registry, not the parent process.
	next := windowsStartupEnvironment{keyPath: env.keyPath, name: env.name, notify: env.notify}
	if got, err := next.Get(); err != nil || got != "9333" {
		t.Fatalf("value=%q err=%v", got, err)
	}
	if err := updateStartupEnvironment(home, 9333, true, next); err != nil {
		t.Fatal(err)
	}
	if err := updateStartupEnvironment(home, 0, false, next); err != nil {
		t.Fatal(err)
	}
	if got, err := next.Get(); err != nil || got != "9444" {
		t.Fatalf("restored=%q err=%v", got, err)
	}
}

func TestWindowsStartupRetriesPublicationAfterRegistryWrite(t *testing.T) {
	for _, previous := range []string{"", "9444"} {
		t.Run("previous="+previous, func(t *testing.T) {
			env := testWindowsStartupEnvironment(t)
			home := t.TempDir()
			if previous != "" {
				if err := env.Set(previous); err != nil {
					t.Fatal(err)
				}
			}
			fail := true
			publications := 0
			env.notify = func() error {
				publications++
				if fail {
					return errors.New("desktop notification failed")
				}
				return nil
			}
			if err := updateStartupEnvironment(home, 9333, true, env); err == nil {
				t.Fatal("expected notification failure")
			}
			if got, _ := env.Get(); got != "9333" {
				t.Fatalf("registry write missing: %q", got)
			}
			fail = false
			if err := updateStartupEnvironment(home, 9333, true, env); err != nil {
				t.Fatal(err)
			}
			if publications != 2 {
				t.Fatalf("enable publication attempts=%d", publications)
			}
			fail = true
			if err := updateStartupEnvironment(home, 0, false, env); err == nil {
				t.Fatal("expected restore notification failure")
			}
			if got, _ := env.Get(); got != previous {
				t.Fatalf("restored=%q", got)
			}
			fail = false
			if err := updateStartupEnvironment(home, 0, false, env); err != nil {
				t.Fatal(err)
			}
			if publications != 4 {
				t.Fatalf("publication attempts=%d", publications)
			}
			if _, err := os.Stat(filepath.Join(home, "workbuddy-workflow", "startup-environment.json")); !os.IsNotExist(err) {
				t.Fatalf("ownership remains: %v", err)
			}
		})
	}
}

func TestWindowsStartupPreservesExternalChanges(t *testing.T) {
	env := testWindowsStartupEnvironment(t)
	home := t.TempDir()
	if err := updateStartupEnvironment(home, 9333, true, env); err != nil {
		t.Fatal(err)
	}
	if err := env.Set("9555"); err != nil {
		t.Fatal(err)
	}
	if err := updateStartupEnvironment(home, 9333, true, env); err == nil {
		t.Fatal("expected external change conflict")
	}
	if err := updateStartupEnvironment(home, 0, false, env); err != nil {
		t.Fatal(err)
	}
	if got, _ := env.Get(); got != "9555" {
		t.Fatalf("external value changed: %q", got)
	}
}
