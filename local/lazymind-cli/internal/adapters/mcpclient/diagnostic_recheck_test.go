package mcpclient

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
)

func TestDiagnosticEnvironmentRechecksTheSameInstallation(t *testing.T) {
	for _, bootstrap := range []string{"home", "connection"} {
		t.Run(bootstrap, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			t.Setenv(connectorconfig.EnvironmentKey, "")
			t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
			t.Setenv("LAZYMIND_HOME", filepath.Join(root, "installed home"))
			bindDesktopForTest(t, agentexec.RaccoonDesktop)
			createAdapter := func() *Adapter {
				t.Helper()
				store, err := credentials.NewStore("", "")
				if err != nil {
					t.Fatal(err)
				}
				bridge, err := mcpbridge.New(store)
				if err != nil {
					t.Fatal(err)
				}
				adapter, err := New(Raccoon, "", bridge)
				if err != nil {
					t.Fatal(err)
				}
				return adapter
			}
			installed := createAdapter()
			if err := writeManagedConfig(Raccoon, configPath(Raccoon), installed.self, installed.home, installed.hostID, false); err != nil {
				t.Fatal(err)
			}
			if bootstrap == "connection" {
				t.Setenv(connectorconfig.EnvironmentKey, connectorconfig.Path(installed.home, string(Raccoon), ""))
				t.Setenv("LAZYMIND_HOME", filepath.Join(root, "unrelated home"))
			}
			before := createAdapter().Status(context.Background())
			if before.State != agentintegration.Enabled {
				t.Fatalf("fixture is not configured: %+v", before)
			}
			// Use the JSON contract exposed to the repair request, including the
			// resolved home when a connection record overrides an inherited value.
			body, err := json.Marshal(before.DiagnosticContext)
			if err != nil {
				t.Fatal(err)
			}
			var diagnostic agentintegration.DiagnosticContext
			if err := json.Unmarshal(body, &diagnostic); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LAZYMIND_HOME", "")
			t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
			t.Setenv(connectorconfig.EnvironmentKey, "")
			plain := createAdapter().Status(context.Background())
			if plain.Issue == nil || plain.Issue.Code != "environment_mismatch" {
				t.Fatalf("expected a different installation without the environment: %+v", plain)
			}
			for key, value := range diagnostic.Environment {
				if value == "~" {
					value = root
				} else if suffix, found := strings.CutPrefix(value, "~"+string(filepath.Separator)); found {
					value = filepath.Join(root, suffix)
				}
				t.Setenv(key, value)
			}
			rechecked := createAdapter().Status(context.Background())
			if rechecked.State != before.State || rechecked.Issue != nil || rechecked.DiagnosticLog != before.DiagnosticLog {
				t.Fatalf("repair check used another installation: %+v", rechecked)
			}
			if os.Getenv("LAZYMIND_HOME") != installed.home {
				t.Fatal("diagnostics did not preserve the resolved installation home")
			}
		})
	}
}
