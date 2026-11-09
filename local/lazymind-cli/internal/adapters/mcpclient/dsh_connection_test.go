package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func connectedDSHFixture(t *testing.T) (*Adapter, workflowhost.Pairing, workflowhost.Pairing) {
	t.Helper()
	a := desktopAdapterFixture(t)
	a.dshRunning = func(context.Context, string) (bool, error) { return false, nil }
	a.dshRunner = func(_ context.Context, profile string, _ ...string) error { installDSHFixture(t, profile); return nil }
	status := a.Connect(context.Background())
	if status.State != agentintegration.Enabled || status.Connection == nil || status.Connection.RuntimeState != "waiting" {
		t.Fatalf("install did not distinguish configuration from connection: %+v", status)
	}
	store, _ := credentials.NewStore(a.home, "")
	account, err := store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	pairs := make([]workflowhost.Pairing, 0, 2)
	for _, profile := range dshDesktopProfiles {
		config, err := connectorconfig.Load(connectorconfig.Path(a.home, string(DeepSeekHarness), filepath.Join(dshHome(), "profiles", profile)))
		if err != nil {
			t.Fatal(err)
		}
		var raw workflowhost.Pairing
		body, err := os.ReadFile(config.PairingFile)
		if err != nil || json.Unmarshal(body, &raw) != nil {
			t.Fatal("read pairing fixture")
		}
		pair, err := workflowhost.Load(a.home, raw.ConnectorID, account)
		if err != nil {
			t.Fatal(err)
		}
		pairs = append(pairs, pair)
	}
	return a, pairs[0], pairs[1]
}

func TestDSHConnectionUsesOnlyFreshDesktopRuntime(t *testing.T) {
	a, desktop, web := connectedDSHFixture(t)
	assertState := func(runtime, issue string, ready bool) {
		t.Helper()
		status := a.Status(context.Background())
		code := ""
		if status.Issue != nil {
			code = status.Issue.Code
		}
		if status.State != agentintegration.Enabled || status.Enabled == nil || !*status.Enabled || !status.CanDisconnect || !status.CanConnect ||
			status.Connection == nil || !status.Connection.MCPConfigured || !status.Connection.MCPTrusted ||
			status.Connection.RuntimeState != runtime || status.Connection.ControlReady != ready || code != issue {
			t.Fatalf("runtime=%s issue=%s ready=%v; status=%+v connection=%+v", runtime, issue, ready, status, status.Connection)
		}
	}
	assertState("waiting", "", false)
	if err := workflowhost.RecordRuntimeStatus(a.home, web, "", true); err != nil {
		t.Fatal(err)
	}
	assertState("waiting", "", false)
	a.dshRunning = func(context.Context, string) (bool, error) { return true, nil }
	assertState("unavailable", "host_unavailable", false)
	if err := workflowhost.RecordRuntimeStatus(a.home, desktop, "", true); err != nil {
		t.Fatal(err)
	}
	assertState("connected", "", true)
	if err := workflowhost.RecordRuntimeStatus(a.home, desktop, "", false); err != nil {
		t.Fatal(err)
	}
	assertState("unavailable", "mcp_unavailable", true)
	if err := workflowhost.RecordRuntimeStatus(a.home, desktop, "lazymind_unavailable", true); err != nil {
		t.Fatal(err)
	}
	assertState("unavailable", "lazymind_unavailable", false)
	if err := workflowhost.RecordRuntimeStatus(a.home, desktop, "", true); err != nil {
		t.Fatal(err)
	}
	assertState("connected", "", true)
	path := filepath.Join(a.home, "workflow-hosts", desktop.ConnectorID+".json.runtime-status.json")
	live, err := workflowhost.ReadRuntimeStatus(a.home, desktop)
	if err != nil {
		t.Fatal(err)
	}
	live.CheckedAt = time.Now().Add(-13 * time.Second).UnixMilli()
	body, _ := json.Marshal(live)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	assertState("unavailable", "host_unavailable", false)
	a.dshRunning = func(context.Context, string) (bool, error) { return false, errors.New("inspection failed") }
	assertState("unavailable", "process_inspection_failed", false)
}

func TestDSHBundleUpdatePreservesConfigurationAndRetryIntent(t *testing.T) {
	for _, action := range []string{"connect", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			a, _, _ := connectedDSHFixture(t)
			for _, profile := range dshDesktopProfiles {
				path := a.forDSHProfile(profile, "").dshConfigPath()
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, path, strings.ReplaceAll(string(body), dshBundleHash(), strings.Repeat("0", 64)))
			}
			record, err := a.dshRecord()
			if err != nil {
				t.Fatal(err)
			}
			record.Action = action
			before, _ := json.Marshal(record)
			writeTestFile(t, a.dshRecordPath(), string(before))
			status := a.Status(context.Background())
			if status.Issue == nil || status.Issue.Code != "plugin_update_required" || status.Enabled == nil || !*status.Enabled ||
				!status.CanConnect || !status.CanDisconnect {
				t.Fatalf("bundle update lost configuration: %+v", status)
			}
			if action == "disconnect" && status.RetryAction != "disconnect" || action == "connect" && status.RetryAction == "disconnect" {
				t.Fatalf("bundle update changed retry intent: %+v", status)
			}
			after, _ := os.ReadFile(a.dshRecordPath())
			if string(before) != string(after) {
				t.Fatal("inspection rewrote the installation record")
			}
		})
	}
}

func TestDSHNativeEnablementAndPairingOverrideLiveRuntime(t *testing.T) {
	for _, scenario := range []string{"disabled plugin", "disabled pairing", "account changed", "signed out"} {
		t.Run(scenario, func(t *testing.T) {
			a, pair, _ := connectedDSHFixture(t)
			if err := workflowhost.RecordRuntimeStatus(a.home, pair, "", true); err != nil {
				t.Fatal(err)
			}
			want := ""
			switch scenario {
			case "disabled plugin":
				path := a.forDSHProfile("desktop", "").dshConfigPath()
				doc, _ := readYAMLDocument(path)
				for _, row := range yamlSequence(doc).Content {
					if scalarValue(row, "id") == dshWorkflowRow {
						mappingValue(row, "disabled").Value = "true"
					}
				}
				body, _ := encodeYAML(doc)
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
				want = "mcp_enable_required"
			case "disabled pairing":
				if err := workflowhost.Disable(a.home, pair.ConnectorID); err != nil {
					t.Fatal(err)
				}
				want = "pairing_disabled"
			case "account changed":
				store, _ := credentials.NewStore(a.home, "")
				if err := store.Save(credentials.Credentials{ServerURL: "https://another.example", Username: "other", AccessToken: "other", RefreshToken: "refresh", ExpiresIn: 3600, SavedAt: float64(time.Now().Unix())}); err != nil {
					t.Fatal(err)
				}
				want = "pairing_account_mismatch"
			case "signed out":
				if err := os.Remove(filepath.Join(a.home, "credentials.json")); err != nil {
					t.Fatal(err)
				}
				want = "lazymind_auth_required"
			}
			status := a.Status(context.Background())
			if status.State != agentintegration.Enabled || status.Enabled == nil || !*status.Enabled || !status.CanDisconnect ||
				status.Connection == nil || !status.Connection.MCPConfigured || status.Connection.ControlReady || status.Connection.RuntimeState != "unavailable" ||
				status.Issue == nil || status.Issue.Code != want {
				t.Fatalf("want=%s status=%+v connection=%+v", want, status, status.Connection)
			}
		})
	}
}

func TestDSHReconnectRepairsPairingWithoutReusingReadiness(t *testing.T) {
	a, before, _ := connectedDSHFixture(t)
	if err := workflowhost.RecordRuntimeStatus(a.home, before, "", true); err != nil {
		t.Fatal(err)
	}
	if err := workflowhost.Disable(a.home, before.ConnectorID); err != nil {
		t.Fatal(err)
	}
	result := a.Connect(context.Background())
	if result.State != agentintegration.Enabled || result.Issue != nil || result.Connection == nil ||
		!result.Connection.MCPTrusted || result.Connection.ControlReady || result.Connection.RuntimeState != "waiting" {
		t.Fatalf("reconnect did not repair the disabled pairing independently of readiness: %+v connection=%+v", result, result.Connection)
	}
	after, err := workflowhost.Load(a.home, before.ConnectorID, before.AccountScope)
	if err != nil || before.Token != after.Token || before.RuntimeRevision == after.RuntimeRevision {
		t.Fatalf("pairing was not re-enabled with a new runtime revision: %v", err)
	}
	if _, err := workflowhost.ReadRuntimeStatus(a.home, after); err == nil {
		t.Fatal("reconnected installation reused old readiness")
	}
}
