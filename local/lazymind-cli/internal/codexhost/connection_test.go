package codexhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/integrationstate"
)

// These tests observe only simulated OS state. They never launch, quit or
// reconfigure the developer's application or desktop.
func connectionFixture(t *testing.T) string {
	t.Helper()
	app, running, listener, desktop, probe := startupApplication, startupRunning, startupListenerMatches, startupDesktopReady, controlProbe
	t.Cleanup(func() {
		startupApplication, startupRunning, startupListenerMatches, startupDesktopReady, controlProbe = app, running, listener, desktop, probe
	})
	startupApplication = func() (string, error) { return "/original/Codex.app", nil }
	startupRunning = func(context.Context, string) (bool, error) { return true, nil }
	startupListenerMatches = func(context.Context, string, int) bool { return true }
	startupDesktopReady = func(context.Context, int) (bool, error) { return true, nil }
	controlProbe = func(context.Context, string, Config) (bool, error) { return true, nil }
	home := t.TempDir()
	if err := Configure(home, filepath.Join(home, "node"), filepath.Join(home, "pairing.json")); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCurrentConnectionNeedsNoManagedStartupEntry(t *testing.T) {
	home := connectionFixture(t)
	if err := CheckConnection(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(home, "codex-workflow"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "config.json" {
		t.Fatal("readiness created persistent launch settings")
	}
}

func TestConnectionChecksCurrentProcessEndpointOwnerAndNativeCapability(t *testing.T) {
	for _, scenario := range []string{"closed", "no-cdp", "foreign", "bad-endpoint", "not-ready", "probe-failed", "changed-owner", "disabled", "missing-config", "uninspectable"} {
		t.Run(scenario, func(t *testing.T) {
			home := connectionFixture(t)
			want := "CODEX_CONNECTION_REQUIRED"
			switch scenario {
			case "closed":
				startupRunning = func(context.Context, string) (bool, error) { return false, nil }
			case "no-cdp":
				startupDesktopReady = func(context.Context, int) (bool, error) { return false, nil }
			case "foreign":
				startupListenerMatches = func(context.Context, string, int) bool { return false }
				want = "CODEX_CDP_UNAVAILABLE"
			case "bad-endpoint":
				startupDesktopReady = func(context.Context, int) (bool, error) { return false, errors.New("wrong application") }
				want = "CODEX_CDP_UNAVAILABLE"
			case "not-ready":
				controlProbe = func(context.Context, string, Config) (bool, error) { return false, nil }
				want = "CODEX_CONTROL_UNAVAILABLE"
			case "probe-failed":
				controlProbe = func(context.Context, string, Config) (bool, error) { return false, errors.New("timeout") }
				want = "CODEX_CONTROL_UNAVAILABLE"
			case "changed-owner":
				want = "CODEX_CDP_UNAVAILABLE"
				controlProbe = func(context.Context, string, Config) (bool, error) {
					startupListenerMatches = func(context.Context, string, int) bool { return false }
					return true, nil
				}
			case "disabled":
				if err := integrationstate.Set(home, "codex", false); err != nil {
					t.Fatal(err)
				}
				want = "CODEX_INTEGRATION_DISABLED"
			case "missing-config":
				if err := os.Remove(configPath(home)); err != nil {
					t.Fatal(err)
				}
				want = "CODEX_CONFIGURATION_UNAVAILABLE"
			case "uninspectable":
				startupRunning = func(context.Context, string) (bool, error) { return false, errors.New("inspection failed") }
				want = "CODEX_CDP_UNAVAILABLE"
			}
			// Even a recently written optimistic heartbeat must not authorize it.
			if err := os.WriteFile(filepath.Join(home, "pairing.json.codex-status.json"), []byte(`{"ready":true,"checked_at":9999999999999}`), 0600); err != nil {
				t.Fatal(err)
			}
			err := CheckConnection(context.Background(), home)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %s, got %v", want, err)
			}
			if want == "CODEX_CONNECTION_REQUIRED" && !strings.Contains(err.Error(), "recovery window") {
				t.Fatal("missing recovery guidance")
			}
		})
	}
}

func TestEveryExecutionConnectionCheckProbesAgain(t *testing.T) {
	home := connectionFixture(t)
	calls := 0
	controlProbe = func(context.Context, string, Config) (bool, error) { calls++; return calls == 1, nil }
	if err := CheckConnection(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	if err := CheckConnection(context.Background(), home); err == nil {
		t.Fatal("cached native readiness survived connection loss")
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
