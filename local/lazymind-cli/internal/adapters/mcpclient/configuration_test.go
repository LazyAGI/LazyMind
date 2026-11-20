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
	"lazymind/agentconnector/internal/workbuddyhost"
)

func TestProductEntryCanBeReplacedAcrossLazyMindEnvironments(t *testing.T) {
	for _, kind := range []Kind{Cursor, Raccoon, TRAEWork} {
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
			targets := map[Kind]agentexec.BindingTarget{Cursor: agentexec.CursorDesktop, Raccoon: agentexec.RaccoonDesktop, TRAEWork: agentexec.TRAEWorkDesktop}
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
				// Cursor's native approval uses the same publication step.
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

func TestWorkBuddyBindingIsExclusiveToItsDeployment(t *testing.T) {
	for _, action := range []string{"connect", "disconnect", "write"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			ownerHome := filepath.Join(root, "desktop")
			other := testAdapter(WorkBuddy)
			other.home = filepath.Join(root, "compose")
			other.self, _ = os.Executable()
			path := configPath(WorkBuddy)
			if err := writeManagedConfig(WorkBuddy, path, other.self, ownerHome, "desktop-host", false); err != nil {
				t.Fatal(err)
			}
			read := func() []byte {
				t.Helper()
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return body
			}
			before := read()
			if action == "write" {
				err := writeManagedConfig(WorkBuddy, path, other.self, other.home, other.hostID, false)
				if err == nil {
					t.Fatal("WorkBuddy registration was writable by a second deployment")
				}
			} else {
				var status agentintegration.Status
				if action == "connect" {
					status = other.Connect(t.Context())
				} else {
					status = other.Disconnect(t.Context())
				}
				if status.State != agentintegration.Conflict || status.Issue == nil ||
					status.Issue.Code != "environment_mismatch" || status.CanConnect || status.CanDisconnect {
					t.Fatalf("expected an ownership conflict, got %+v", status)
				}
				if status.Enabled == nil || *status.Enabled {
					t.Fatal("WorkBuddy appears enabled in the wrong deployment")
				}
			}
			if !bytes.Equal(before, read()) {
				t.Fatal("second deployment changed the owner's WorkBuddy configuration")
			}
		})
	}
}

func TestWorkBuddyKeepsRegistrationWhenDisconnectCleanupFails(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	owner := testAdapter(WorkBuddy)
	owner.home = filepath.Join(root, "owner")
	owner.self, _ = os.Executable()
	path := configPath(WorkBuddy)
	if err := writeManagedConfig(WorkBuddy, path, owner.self, owner.home, owner.hostID, false); err != nil {
		t.Fatal(err)
	}
	localConfig := workbuddyhost.ConfigPath(owner.home)
	writeTestFile(t, localConfig, "{invalid")
	status := owner.Disconnect(t.Context())
	if status.State != agentintegration.Failed {
		t.Fatalf("expected cleanup to fail, got %+v", status)
	}
	state, err := readManagedConfig(WorkBuddy, path, owner.self, owner.home, owner.hostID)
	if err != nil || !state.owned {
		t.Fatalf("cleanup failure released WorkBuddy ownership: %+v, %v", state, err)
	}
	if err := os.Remove(localConfig); err != nil {
		t.Fatal(err)
	}
	if status = owner.Disconnect(t.Context()); status.State == agentintegration.Failed {
		t.Fatalf("owning deployment could not retry: %+v", status)
	}
	other := filepath.Join(root, "other")
	if err := writeManagedConfig(WorkBuddy, path, owner.self, other, "other-host", false); err != nil {
		t.Fatalf("WorkBuddy could not be configured after successful cleanup: %v", err)
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
