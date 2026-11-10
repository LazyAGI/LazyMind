package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
	"lazymind/agentconnector/internal/workflowhost"
)

func newPluginReconnectTestAdapter(t *testing.T) (*Adapter, *credentials.Store, *int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	oldPrepare, oldHealth := prepareCodexDesktop, startupHealth
	prepareCalls := 0
	prepareCodexDesktop = func(context.Context, string) error { prepareCalls++; return nil }
	startupHealth = func(context.Context, string) string { return "" }
	t.Cleanup(func() { prepareCodexDesktop, startupHealth = oldPrepare, oldHealth })
	root := t.TempDir()
	home, profile := filepath.Join(root, "state"), filepath.Join(root, "profile")
	t.Setenv("HOME", root)
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv(connectorconfig.EnvironmentKey, "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("CODEX_HOME", profile)
	bindCodexDesktopForTest(t, root)
	store, err := credentials.NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:1", Username: "account-one", AccessToken: "test", RefreshToken: "test"}); err != nil {
		t.Fatal(err)
	}
	self := writeExecutable(t, root, "lazymind", "#!/bin/sh\nexit 0\n")
	node := writeExecutable(t, root, "node", "#!/bin/sh\necho v22.23.0\n")
	t.Setenv("LAZYMIND_NODE_BIN", node)
	binary := writeExecutable(t, root, "codex", `#!/bin/sh
if [ "$1" = "--version" ]; then echo codex-cli 1.0.0; exit 0; fi
if [ "$1" = "queue" ]; then exit 0; fi
if [ "$1" = "plugin" ] && [ "$2" = "add" ]; then
 printf '[plugins."%s"]\nenabled = true\n' "$3" > "$CODEX_HOME/config.toml"
 echo installed >> "$CODEX_HOME/events"
 exit 0
fi
if [ "$1" = "plugin" ] && [ "$2" = "remove" ]; then
 echo fixture-remove-failed >&2
 exit 1
fi
exit 1
`)
	adapter, err := New(binary, self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	if status := adapter.Connect(context.Background()); status.State != agentintegration.Enabled {
		t.Fatalf("initial connect: %+v", status)
	}
	return adapter, store, &prepareCalls
}

func TestPluginReconnectValidatesCurrentAccountPairing(t *testing.T) {
	for _, scenario := range []string{"account_switch", "failed_disconnect", "missing_pairing", "valid_pairing", "native_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			adapter, store, prepareCalls := newPluginReconnectTestAdapter(t)
			before, err := codexplugin.Read()
			if err != nil {
				t.Fatal(err)
			}
			account, err := store.AccountScope()
			if err != nil {
				t.Fatal(err)
			}
			_, previousPair, err := codexhost.Load(adapter.home, account)
			if err != nil {
				t.Fatal(err)
			}
			wantInstalls := 2
			switch scenario {
			case "account_switch":
				if err := store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:1", Username: "account-two", AccessToken: "test", RefreshToken: "test"}); err != nil {
					t.Fatal(err)
				}
			case "failed_disconnect":
				status := adapter.Disconnect(context.Background())
				if status.State != agentintegration.Failed || status.RetryAction != "disconnect" || workflowhost.Configured(adapter.home, before.ConnectorID, account) {
					t.Fatalf("expected disabled pairing after plugin removal failed: %+v", status)
				}
			case "missing_pairing":
				if err := os.Remove(before.PairingFile); err != nil {
					t.Fatal(err)
				}
			case "valid_pairing":
				wantInstalls = 1
			case "native_disabled":
				wantInstalls = 1
				config := filepath.Join(before.Profile, "config.toml")
				body, err := os.ReadFile(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, []byte(strings.ReplaceAll(string(body), "enabled = true", "enabled = false")), 0600); err != nil {
					t.Fatal(err)
				}
			}
			status := adapter.Status(context.Background())
			if status.Enabled == nil || !*status.Enabled || !status.CanDisconnect {
				t.Fatalf("pairing health changed the saved configuration switch: %+v", status)
			}
			account, err = store.AccountScope()
			if err != nil {
				t.Fatal(err)
			}
			if wantInstalls == 2 && workflowhost.Configured(adapter.home, before.ConnectorID, account) {
				t.Fatal("status inspection unexpectedly repaired the pairing")
			}
			status = adapter.Connect(context.Background())
			if status.State != agentintegration.Enabled || status.Enabled == nil || !*status.Enabled {
				t.Fatalf("reconnect: %+v", status)
			}
			after, err := codexplugin.Read()
			if err != nil {
				t.Fatal(err)
			}
			config, pair, err := codexhost.Load(adapter.home, account)
			if err != nil {
				t.Fatalf("reconnect did not restore the current account pairing: %v", err)
			}
			if (after.ConnectorID != before.ConnectorID) != (scenario == "account_switch") {
				t.Fatal("connector identity must change only with the account")
			}
			if config.PairingFile != after.PairingFile || pair.ConnectorID != after.ConnectorID {
				t.Fatal("host configuration does not use the repaired pairing")
			}
			connection, err := connectorconfig.Load(connectorconfig.Path(adapter.home, "codex", after.Profile))
			if err != nil || connection.PairingFile != after.PairingFile {
				t.Fatalf("MCP configuration does not use the repaired pairing: %v", err)
			}
			if scenario != "account_switch" && scenario != "missing_pairing" && pair.Token != previousPair.Token {
				t.Fatal("reconnect replaced a reusable pairing credential")
			}
			if scenario == "native_disabled" && (status.Connection == nil || status.Connection.MCPEnabled) {
				t.Fatal("reconnect changed the native enable choice")
			}
			events, err := os.ReadFile(filepath.Join(after.Profile, "events"))
			if err != nil || strings.Count(string(events), "installed\n") != wantInstalls {
				t.Fatalf("expected %d installs, got %q: %v", wantInstalls, events, err)
			}
			if *prepareCalls != 2 {
				t.Fatalf("reconnect did not prepare the desktop: %d calls", *prepareCalls)
			}
		})
	}
}

func TestPluginReconnectRequiresCurrentAccount(t *testing.T) {
	adapter, store, prepareCalls := newPluginReconnectTestAdapter(t)
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	status := adapter.Connect(context.Background())
	if status.State != agentintegration.Failed || status.Enabled == nil || !*status.Enabled || !status.CanDisconnect {
		t.Fatalf("signed-out reconnect must fail while preserving the registration: %+v", status)
	}
	if *prepareCalls != 1 {
		t.Fatal("signed-out reconnect attempted to restart the desktop")
	}
}
