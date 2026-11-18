package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
	"lazymind/agentconnector/internal/mcpbridge"
)

func TestReconcileJSONRelocatesOwnedBindingsAndPreservesChoices(t *testing.T) {
	for _, kind := range []Kind{Cursor, WorkBuddy, Raccoon, TRAEWork} {
		for _, legacy := range []bool{false, true} {
			t.Run(string(kind)+map[bool]string{true: "/legacy", false: "/connection"}[legacy], func(t *testing.T) {
				root := t.TempDir()
				setTestHome(t, root)
				t.Setenv("APPDATA", filepath.Join(root, "roaming"))
				a := testAdapter(kind)
				a.home = filepath.Join(root, "state")
				a.self, _ = os.Executable()
				old := filepath.Join(root, "removed installation", "lazymind")
				entry := managedStdio(old, a.home, a.hostID, kind)
				if legacy {
					entry = legacyTestStdio(old, a.home, a.hostID, kind)
				} else if _, err := connectorconfig.Save(connectorconfig.Config{Home: a.home, Provider: string(kind), HostID: a.hostID, Controlled: kind == WorkBuddy}); err != nil {
					t.Fatal(err)
				}
				fields := rawStdioFields(t, entry)
				fields["disabled"] = json.RawMessage(`true`)
				fields["cwd"] = json.RawMessage(`"/user/chosen cwd"`)
				fields["custom"] = json.RawMessage(`{"preserve":[1,false,"value"]}`)
				path := configPath(kind)
				before := writeReconcileJSON(t, path, fields)
				if err := a.Reconcile(context.Background()); err != nil {
					t.Fatal(err)
				}
				rootFields, servers, err := readJSONConfig(path)
				var unrelated map[string]string
				_ = json.Unmarshal(servers["unrelated"], &unrelated)
				if err != nil || string(rootFields["theme"]) != `"dark"` || unrelated["command"] != "keep" || len(unrelated) != 1 {
					t.Fatalf("unrelated settings changed: %s %v", rootFields, err)
				}
				var after map[string]json.RawMessage
				if err := json.Unmarshal(servers[serverName], &after); err != nil {
					t.Fatal(err)
				}
				wantCommand, _ := json.Marshal(a.self)
				if !bytes.Equal(after["command"], wantCommand) {
					t.Fatalf("command=%s want=%s", after["command"], wantCommand)
				}
				delete(fields, "command")
				delete(after, "command")
				want, _ := json.Marshal(fields)
				got, _ := json.Marshal(after)
				if !bytes.Equal(want, got) {
					t.Fatalf("entry settings changed: %s want %s", got, want)
				}
				state, err := readManagedConfig(kind, path, a.self, a.home, a.hostID)
				if err != nil || !state.current {
					t.Fatalf("relocated binding is not current: %+v %v", state, err)
				}
				backup, _ := os.ReadFile(path + ".lazymind-backup")
				if !bytes.Equal(backup, before) {
					t.Fatal("original configuration backup changed")
				}
				assertReconcileDoesNotRewrite(t, a, path)
			})
		}
	}
}

func TestReconcileJSONSkipsOtherIdentitiesAndMissingIntegrations(t *testing.T) {
	for _, scenario := range []string{"home", "host", "provider", "profile", "missing record", "invocation", "legacy host", "legacy provider", "legacy home", "legacy incomplete", "absent"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			a := testAdapter(Cursor)
			a.home = filepath.Join(root, "state")
			a.self, _ = os.Executable()
			path := configPath(Cursor)
			if scenario == "absent" {
				if err := a.Reconcile(context.Background()); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
					t.Fatal("missing integration directory was created")
				}
				return
			}
			entry := managedStdio(filepath.Join(root, "removed", "lazymind"), a.home, a.hostID, Cursor)
			c := connectorconfig.Config{Home: a.home, Provider: string(Cursor), HostID: a.hostID}
			switch scenario {
			case "home":
				c.Home = filepath.Join(root, "other-state")
			case "host":
				c.HostID = "another-host"
			case "provider":
				c.Provider = string(Raccoon)
			case "profile":
				c.Profile = filepath.Join(root, "foreign-profile")
			case "invocation":
				entry.Args = []string{"mcp", "other"}
			case "legacy host", "legacy provider", "legacy home", "legacy incomplete":
				entry = legacyTestStdio(entry.Command, a.home, a.hostID, Cursor)
				switch scenario {
				case "legacy host":
					entry.Env["LAZYMIND_AGENT_HOST_ID"] = "other"
				case "legacy provider":
					entry.Env["LAZYMIND_AGENT_PROVIDER"] = string(Raccoon)
				case "legacy home":
					entry.Env["LAZYMIND_HOME"] = filepath.Join(root, "other-state")
				case "legacy incomplete":
					delete(entry.Env, "LAZYMIND_AGENT_HOST_ID")
				}
			}
			if scenario != "missing record" && entry.Env[connectorconfig.EnvironmentKey] != "" {
				reference, err := connectorconfig.Save(c)
				if err != nil {
					t.Fatal(err)
				}
				entry.Env[connectorconfig.EnvironmentKey] = reference
			}
			before := writeReconcileJSON(t, path, rawStdioFields(t, entry))
			if err := a.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("foreign or incomplete binding changed")
			}
			if _, err := os.Stat(path + ".lazymind-backup"); !os.IsNotExist(err) {
				t.Fatal("skipped binding created a backup")
			}
		})
	}
}

func TestNewAdapterReconcilesToStableLauncherAcrossInstallations(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	home := filepath.Join(root, "state")
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv(connectorconfig.EnvironmentKey, "")
	self, _ := os.Executable()
	a, err := New(Cursor, self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	if a.self != connectorlauncher.Path(home) || !agentexec.SameExecutable(a.actualSelf, self) {
		t.Fatalf("self=%q actual=%q", a.self, a.actualSelf)
	}
	if _, err := connectorconfig.Save(connectorconfig.Config{Home: home, Provider: string(Cursor), HostID: a.hostID}); err != nil {
		t.Fatal(err)
	}
	writeReconcileJSON(t, configPath(Cursor), rawStdioFields(t, managedStdio(filepath.Join(root, "deleted.app", "lazymind"), home, a.hostID, Cursor)))
	if err := a.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := readManagedConfig(Cursor, configPath(Cursor), a.self, home, a.hostID)
	if err != nil || !state.current || state.command != connectorlauncher.Path(home) {
		t.Fatalf("stable binding: %+v %v", state, err)
	}
	other := filepath.Join(root, "new installation", filepath.Base(self))
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, other, string(body))
	if err := os.Chmod(other, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := connectorlauncher.Publish(context.Background(), home, other); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Cursor, self, &mcpbridge.Bridge{}); err != nil {
		t.Fatal(err)
	}
	var target struct {
		Executable string `json:"executable"`
	}
	body, _ = os.ReadFile(filepath.Join(filepath.Dir(a.self), "target.json"))
	if json.Unmarshal(body, &target) != nil || !agentexec.SameExecutable(target.Executable, other) {
		t.Fatal("old installation status construction replaced the new target")
	}
	assertReconcileDoesNotRewrite(t, a, configPath(Cursor))
}

func TestNewAdapterStatusRepairsLegacyBindingWithoutReconnect(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "state"))
	t.Setenv(connectorconfig.EnvironmentKey, "")
	bindDesktopForTest(t, agentexec.CursorDesktop)
	a, err := New(Cursor, "", &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	old := legacyTestStdio(filepath.Join(root, "removed", "lazymind"), a.home, a.hostID, Cursor)
	writeReconcileJSON(t, configPath(Cursor), rawStdioFields(t, old))
	status := a.Status(context.Background())
	if status.State != agentintegration.Enabled || status.Enabled == nil || !*status.Enabled || status.Issue != nil || status.Message != "" {
		t.Fatalf("legacy migration required reconnect: %+v", status)
	}
}

func rawStdioFields(t *testing.T, entry stdioMCPDefinition) map[string]json.RawMessage {
	t.Helper()
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func writeReconcileJSON(t *testing.T, path string, fields map[string]json.RawMessage) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"theme": "dark", "mcpServers": map[string]any{serverName: fields, "unrelated": map[string]string{"command": "keep"}}})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, string(body))
	return body
}

func assertReconcileDoesNotRewrite(t *testing.T, a *Adapter, path string) {
	t.Helper()
	stamp := time.Unix(1000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := a.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged configuration rewritten: %v", err)
	}
}
