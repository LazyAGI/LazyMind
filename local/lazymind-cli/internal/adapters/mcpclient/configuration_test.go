package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
)

func TestProductEntryCanBeReplacedAcrossLazyMindEnvironments(t *testing.T) {
	for _, kind := range []Kind{Cursor, WorkBuddy, Raccoon, TRAEWork} {
		t.Run(string(kind), func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			t.Setenv("APPDATA", filepath.Join(root, "roaming"))
			home := filepath.Join(root, "current")
			t.Setenv("LAZYMIND_HOME", home)
			t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
			adapter := testAdapter(kind)
			adapter.home = home
			adapter.self, _ = os.Executable()
			adapter.bridge = dshTestProbe{}
			targets := map[Kind]agentexec.BindingTarget{Cursor: agentexec.CursorDesktop, WorkBuddy: agentexec.WorkBuddyDesktop, Raccoon: agentexec.RaccoonDesktop, TRAEWork: agentexec.TRAEWorkDesktop}
			bindDesktopForTest(t, targets[kind])
			path := configPath(kind)
			writeTestFile(t, path, `{"mcpServers":{"unrelated":{"command":"keep"}}}`)
			oldCLI := filepath.Join(root, "local", "lazymind")
			writeTestFile(t, oldCLI, "old CLI")
			if err := writeManagedConfig(kind, path, oldCLI, filepath.Join(root, "old-home"), "old-host", false); err != nil {
				t.Fatal(err)
			}
			status := adapter.Status(context.Background())
			if status.Enabled == nil || !*status.Enabled || !status.CanDisconnect || status.State != agentintegration.Conflict || status.Issue.Code != "environment_mismatch" {
				t.Fatalf("foreign configuration: %+v", status)
			}
			status = adapter.Disconnect(context.Background())
			if status.Enabled == nil || *status.Enabled || status.State == agentintegration.Failed {
				t.Fatalf("disconnect: %+v", status)
			}
			if kind == Raccoon || kind == TRAEWork {
				status = adapter.Connect(context.Background())
				if status.State != agentintegration.Enabled {
					t.Fatalf("reconnect: %+v", status)
				}
			} else {
				// Cursor's native approval and WorkBuddy's setup use this same publication step.
				if err := writeManagedConfig(kind, path, adapter.self, home, adapter.hostID, false); err != nil {
					t.Fatal(err)
				}
			}
			state, err := readManagedConfig(kind, path, adapter.self, home, adapter.hostID)
			if err != nil || !state.current {
				t.Fatalf("current configuration: %+v %v", state, err)
			}
			_, servers, err := readJSONConfig(path)
			if err != nil || servers["unrelated"] == nil {
				t.Fatal("lost unrelated entry", err)
			}
			// The named entry may be overwritten directly when another environment writes it again.
			if err := writeManagedConfig(kind, path, oldCLI, filepath.Join(root, "old-home"), "old-host", false); err != nil {
				t.Fatal(err)
			}
			if err := writeManagedConfig(kind, path, adapter.self, home, adapter.hostID, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnreadableConfigurationIsUnknownAndHasDiagnostic(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	adapter := testAdapter(Raccoon)
	adapter.home = filepath.Join(root, "state")
	writeTestFile(t, configPath(Raccoon), "{broken")
	status := adapter.Status(context.Background())
	if status.Enabled != nil || status.CanConnect || status.Issue == nil || status.Issue.Code != "configuration_unreadable" || status.DiagnosticID == "" {
		t.Fatalf("unknown: %+v", status)
	}
	before, _ := os.ReadFile(configPath(Raccoon))
	status = adapter.Disconnect(context.Background())
	after, _ := os.ReadFile(configPath(Raccoon))
	if string(before) != string(after) || status.Enabled != nil {
		t.Fatal("failed read changed the configuration")
	}
	var row map[string]any
	body, _ := os.ReadFile(status.DiagnosticLog)
	for _, line := range bytes.Split(bytes.TrimSpace(body), []byte("\n")) {
		if json.Unmarshal(line, &row) != nil {
			t.Fatal("invalid log record")
		}
	}
}

func TestFailedDisconnectKeepsActualEntryAndReportsRetry(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	adapter := testAdapter(Raccoon)
	adapter.home = filepath.Join(root, "state")
	adapter.self, _ = os.Executable()
	path := configPath(Raccoon)
	if err := writeManagedConfig(Raccoon, path, adapter.self, adapter.home, adapter.hostID, false); err != nil {
		t.Fatal(err)
	}
	// An invalid lock path simulates an I/O failure without relying on OS privilege levels.
	if err := os.Remove(path + ".lazymind-lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".lazymind-lock", 0700); err != nil {
		t.Fatal(err)
	}
	status := adapter.Disconnect(context.Background())
	if status.State != agentintegration.Failed || status.Enabled == nil || !*status.Enabled || status.RetryAction != "disconnect" || !status.CanDisconnect {
		t.Fatalf("failed disconnect: %+v", status)
	}
	if err := os.Remove(path + ".lazymind-lock"); err != nil {
		t.Fatal(err)
	}
	status = adapter.Disconnect(context.Background())
	if status.State == agentintegration.Failed || status.Enabled == nil || *status.Enabled {
		t.Fatalf("retry: %+v", status)
	}
}
