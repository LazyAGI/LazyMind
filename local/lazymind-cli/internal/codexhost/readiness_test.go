package codexhost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowhost"
)

func readinessFixture(t *testing.T) (string, workflowhost.Pairing, string) {
	t.Helper()
	home := connectionFixture(t)
	pair, err := workflowhost.EnsureForProvider(home, "codex", filepath.Join(home, "profile"), "account")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")
	if err := Configure(home, filepath.Join(home, "node"), file); err != nil {
		t.Fatal(err)
	}
	return home, pair, file + ".codex-status.json"
}

func writeReadiness(file string, status Status) error {
	body, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return localfile.Write(file, body, 0600)
}

func TestWaitReadyRequiresFreshHealthyHeartbeatAndLiveControl(t *testing.T) {
	for _, scenario := range []string{"ready", "missing", "before-restart", "expired", "future", "other-connector", "not-ready", "capability_unavailable", "cdp_unavailable", "control_window_unavailable", "live-control-lost", "live-cdp-lost", "live-window-lost"} {
		t.Run(scenario, func(t *testing.T) {
			home, pair, file := readinessFixture(t)
			since := time.Now().Add(-time.Second)
			status := Status{Connector: pair.ConnectorID, Ready: true, CheckedAt: time.Now().UnixMilli()}
			wantIssue := "host_unavailable"
			switch scenario {
			case "before-restart":
				status.CheckedAt = since.Add(-time.Second).UnixMilli()
			case "expired":
				status.CheckedAt = time.Now().Add(-time.Minute).UnixMilli()
			case "future":
				status.CheckedAt = time.Now().Add(time.Minute).UnixMilli()
			case "other-connector":
				status.Connector = "other"
			case "not-ready":
				status.Ready = false
			case "capability_unavailable", "cdp_unavailable", "control_window_unavailable":
				status.Issue, wantIssue = scenario, scenario // Even optimistic ready cannot override an issue.
			case "live-control-lost":
				controlProbe = func(context.Context, string, Config) (bool, error) { return false, nil }
				wantIssue = "capability_unavailable"
			case "live-cdp-lost", "live-window-lost":
				wantIssue = "cdp_unavailable"
				if scenario == "live-window-lost" {
					wantIssue = "control_window_unavailable"
				}
				controlProbe = func(context.Context, string, Config) (bool, error) {
					return false, setupError(wantIssue, errors.New("live probe failed"))
				}
			}
			if scenario != "missing" {
				if err := writeReadiness(file, status); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err := WaitReady(ctx, home, "account", since)
			if scenario == "ready" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || SetupIssue(err) != wantIssue {
				t.Fatalf("want timeout with %s, got %v", wantIssue, err)
			}
		})
	}
}

func TestWaitReadyRetriesInitializingControl(t *testing.T) {
	home, pair, file := readinessFixture(t)
	since := time.Now().Add(-time.Second)
	status := Status{Connector: pair.ConnectorID, Issue: "capability_unavailable", CheckedAt: time.Now().UnixMilli()}
	if err := writeReadiness(file, status); err != nil {
		t.Fatal(err)
	}
	updated := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		status.Ready, status.Issue, status.CheckedAt = true, "", time.Now().UnixMilli()
		updated <- writeReadiness(file, status)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := WaitReady(ctx, home, "account", since)
	if updateErr := <-updated; updateErr != nil {
		t.Fatal(updateErr)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestWaitReadyRejectsPairingRevokedDuringLiveProbe(t *testing.T) {
	home, pair, file := readinessFixture(t)
	if err := writeReadiness(file, Status{Connector: pair.ConnectorID, Ready: true, CheckedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	controlProbe = func(context.Context, string, Config) (bool, error) {
		if err := workflowhost.Disable(home, pair.ConnectorID); err != nil {
			t.Fatal(err)
		}
		return true, nil
	}
	err := WaitReady(context.Background(), home, "account", time.Now().Add(-time.Second))
	var pairing *workflowhost.PairingError
	if !errors.As(err, &pairing) || pairing.Code != "HOST_PAIRING_DISABLED" {
		t.Fatalf("accepted revoked pairing: %v", err)
	}
}
