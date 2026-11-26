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

func recordDSHFixtureRuntime(t *testing.T, a *Adapter, pair workflowhost.Pairing, issue string, mcpReady bool) error {
	t.Helper()
	record, err := a.forDSHProfile(filepath.Base(pair.Profile), "").readDSHBuild()
	if err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	return workflowhost.RecordRuntimeStatus(a.home, pair, issue, mcpReady, workflowhost.DSHLoad{
		Build: record.Build, Instance: "00000000-0000-0000-0000-000000000001", StartedAt: record.RestartAfter + 1,
	})
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
	if err := recordDSHFixtureRuntime(t, a, web, "", true); err != nil {
		t.Fatal(err)
	}
	assertState("waiting", "", false)
	a.dshRunning = func(context.Context, string) (bool, error) { return true, nil }
	assertState("waiting", "", false)
	if err := recordDSHFixtureRuntime(t, a, desktop, "", true); err != nil {
		t.Fatal(err)
	}
	assertState("connected", "", true)
	if err := recordDSHFixtureRuntime(t, a, desktop, "", false); err != nil {
		t.Fatal(err)
	}
	assertState("unavailable", "mcp_unavailable", true)
	if err := recordDSHFixtureRuntime(t, a, desktop, "lazymind_unavailable", true); err != nil {
		t.Fatal(err)
	}
	assertState("connected", "lazymind_unavailable", true)
	if err := recordDSHFixtureRuntime(t, a, desktop, "lazymind_auth_required", true); err != nil {
		t.Fatal(err)
	}
	assertState("connected", "lazymind_auth_required", true)
	independent := a.Status(t.Context()).Connection
	if independent.LazyMindState != "authorization_required" || independent.PresentationStatus != "available" {
		t.Fatalf("Core authorization changed local control or presentation evidence: %+v", independent)
	}
	if err := recordDSHFixtureRuntime(t, a, desktop, "", true); err != nil {
		t.Fatal(err)
	}
	assertState("connected", "", true)
	path := filepath.Join(a.home, "workflow-hosts", desktop.ConnectorID+".json.runtime-status.json")
	live, err := workflowhost.ReadRuntimeStatus(a.home, desktop)
	if err != nil {
		t.Fatal(err)
	}
	live.Panel = "unavailable"
	if err := workflowhost.RecordRuntimeStatus(a.home, desktop, "", true, live.DSHLoad); err != nil {
		t.Fatal(err)
	}
	panel := a.Status(t.Context()).Connection
	if panel.PresentationStatus != "binding-unavailable" || !panel.ControlReady || panel.LazyMindState != "connected" {
		t.Fatalf("panel failure changed available control: %+v", panel)
	}
	live.Panel, live.ClientBuild = "bound", "0.1.0+0000000000000000"
	if err := workflowhost.RecordRuntimeStatus(a.home, desktop, "", true, live.DSHLoad); err != nil {
		t.Fatal(err)
	}
	panel = a.Status(t.Context()).Connection
	if panel.PresentationStatus != "binding-unavailable" || !panel.ControlReady || panel.LazyMindState != "connected" {
		t.Fatalf("a different client build must affect only presentation: %+v", panel)
	}
	// Keep the verified cold start while expiring its latest heartbeat.
	// Shift the install boundary and the runtime together without a real wait.
	target := a.forDSHProfile("desktop", "")
	record, err := target.readDSHBuild()
	if err != nil {
		t.Fatal(err)
	}
	record.RestartAfter -= 20_000
	if err := target.saveDSHBuild(record); err != nil {
		t.Fatal(err)
	}
	live.CheckedAt -= 20_000
	live.StartedAt -= 20_000
	body, _ := json.Marshal(live)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	assertState("unavailable", "host_unavailable", false)
	a.dshRunning = func(context.Context, string) (bool, error) { return false, errors.New("inspection failed") }
	assertState("unavailable", "process_inspection_failed", false)
}

func TestDSHNativeEnablementAndPairingOverrideLiveRuntime(t *testing.T) {
	for _, scenario := range []string{"disabled plugin", "unselected bundle", "disabled pairing", "account changed", "signed out"} {
		t.Run(scenario, func(t *testing.T) {
			a, pair, _ := connectedDSHFixture(t)
			if err := recordDSHFixtureRuntime(t, a, pair, "", true); err != nil {
				t.Fatal(err)
			}
			want := ""
			switch scenario {
			case "unselected bundle":
				path := filepath.Join(dshHome(), "profiles", "desktop", "package.json")
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, path, strings.Replace(string(body), `,"@lazymind/dsh-workflow"`, "", 1))
				want = "mcp_enable_required"
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
	if err := recordDSHFixtureRuntime(t, a, before, "", true); err != nil {
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

func TestDSHRestartRejectsPreInstallProcessWithMatchingBuild(t *testing.T) {
	a, desktop, _ := connectedDSHFixture(t)
	target := a.forDSHProfile("desktop", "")
	record, err := target.readDSHBuild()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	for _, observed := range []workflowhost.DSHLoad{
		{Build: record.Build, StartedAt: record.RestartAfter},
		{Build: "0.1.0+0000000000000000", StartedAt: record.RestartAfter + 1},
	} {
		observed.Instance = "00000000-0000-0000-0000-000000000002"
		if err := workflowhost.RecordRuntimeStatus(a.home, desktop, "", true, observed); err != nil {
			t.Fatal(err)
		}
		status := a.Status(t.Context())
		if status.Connection.ControlReady || status.Connection.RuntimeState != "waiting" {
			t.Fatalf("wrong build or old process confirmed the installation: %+v", status.Connection)
		}
	}
	if err := recordDSHFixtureRuntime(t, a, desktop, "", true); err != nil {
		t.Fatal(err)
	}
	status := a.Status(t.Context())
	if !status.Connection.ControlReady || status.Connection.RuntimeState != "connected" {
		t.Fatalf("new process did not confirm the installed build: %+v", status.Connection)
	}
}
