package workbuddyhost

import (
	"context"
	"encoding/json"
	"errors"
	"lazymind/agentconnector/internal/workflowhost"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusRejectsStaleOtherConnectorAndRemoteEndpoint(t *testing.T) {
	config := Config{PairingFile: filepath.Join(t.TempDir(), "pair.json")}
	base := Status{ConnectorID: "host-test", CheckedAt: time.Now().UnixMilli(), Ready: true, BridgeURL: "http://127.0.0.1:19092"}
	for _, tc := range []struct {
		name   string
		mutate func(*Status)
		fail   bool
	}{
		{"current", func(*Status) {}, false},
		{"stale", func(s *Status) { s.CheckedAt -= 30000 }, true},
		{"other connector", func(s *Status) { s.ConnectorID = "other" }, true},
		{"remote URL", func(s *Status) { s.BridgeURL = "http://example.com" }, true},
		{"userinfo", func(s *Status) { s.BridgeURL = "http://secret@127.0.0.1:19091" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.mutate(&s)
			body, _ := json.Marshal(s)
			if err := os.WriteFile(config.PairingFile+".workbuddy-status.json", body, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := ReadStatus(config, "host-test")
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestWaitReadyRequiresAHeartbeatAfterInstallationWithCancellation(t *testing.T) {
	home := t.TempDir()
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", filepath.Join(home, "profile"), "account")
	if err != nil {
		t.Fatal(err)
	}
	config := Config{PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), Node: filepath.Join(home, "node"), CDPPort: 9333}
	body, _ := json.Marshal(config)
	if err := writeAtomic(ConfigPath(home), body); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Second)
	for _, tc := range []struct {
		name         string
		cancellation string
		version      string
		checked      time.Time
		ok           bool
	}{
		{"fresh", "session", "5.8.3", time.Now(), true},
		{"newer", "session", "6.0.0", time.Now(), true},
		{"numeric ordering", "session", "5.8.10", time.Now(), true},
		{"prerelease below minimum", "session", "5.8.3-rc.1", time.Now(), false},
		{"unknown version", "session", "", time.Now(), false},
		{"before restart", "session", "5.8.3", since.Add(-time.Second), false},
		{"old worker", "none", "5.8.3", time.Now(), false},
		{"unsupported layout", "session", "5.7.7", time.Now(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(Status{ConnectorID: pair.ConnectorID, Version: tc.version, Ready: true, Cancellation: tc.cancellation, CheckedAt: tc.checked.UnixMilli(), BridgeURL: "http://127.0.0.1:19091"})
			if err := writeAtomic(config.PairingFile+".workbuddy-status.json", body); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			err := WaitReady(ctx, home, "account", since)
			if (err == nil) != tc.ok {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func waitReadyFixture(t *testing.T) (string, Config, workflowhost.Pairing) {
	t.Helper()
	home := t.TempDir()
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", filepath.Join(home, "profile"), "account")
	if err != nil {
		t.Fatal(err)
	}
	config := Config{PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), Node: filepath.Join(home, "node"), CDPPort: 9333}
	body, _ := json.Marshal(config)
	if err = writeAtomic(ConfigPath(home), body); err != nil {
		t.Fatal(err)
	}
	return home, config, pair
}

func TestWaitReadyAllowsCapabilitiesToBecomeReadyDuringStartup(t *testing.T) {
	home, config, pair := waitReadyFixture(t)
	since := time.Now().Add(-time.Second)
	status := Status{ConnectorID: pair.ConnectorID, Version: "6.0.0", Issue: "capability_unavailable",
		CheckedAt: time.Now().UnixMilli(), BridgeURL: "http://127.0.0.1:19091"}
	body, _ := json.Marshal(status)
	if err := writeAtomic(config.PairingFile+".workbuddy-status.json", body); err != nil {
		t.Fatal(err)
	}
	updated := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		status.Issue, status.Ready, status.Cancellation = "", true, "session"
		status.CheckedAt = time.Now().UnixMilli()
		body, _ := json.Marshal(status)
		updated <- writeAtomic(config.PairingFile+".workbuddy-status.json", body)
	}()
	defer func() {
		if err := <-updated; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := WaitReady(ctx, home, "account", since); err != nil {
		t.Fatalf("startup recovered, but installation failed: %v", err)
	}
}

func TestWaitReadyDistinguishesStartupFromTerminalFailures(t *testing.T) {
	for _, sample := range []struct {
		issue string
		wait  bool
	}{
		{"capability_unavailable", true},
		{"host_unavailable", true},
		{"lazymind_unavailable", true},
		{"unsupported_version", false},
		{"version_unreadable", false},
		{"lazymind_auth_required", false},
		{"pairing_disabled", false},
		{"pairing_account_mismatch", false},
	} {
		t.Run(sample.issue, func(t *testing.T) {
			home, config, pair := waitReadyFixture(t)
			body, _ := json.Marshal(Status{ConnectorID: pair.ConnectorID, Version: "6.0.0", Issue: sample.issue,
				CheckedAt: time.Now().UnixMilli(), BridgeURL: "http://127.0.0.1:19091"})
			if err := writeAtomic(config.PairingFile+".workbuddy-status.json", body); err != nil {
				t.Fatal(err)
			}
			timeout := 2 * time.Second
			if sample.wait {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err := WaitReady(ctx, home, "account", time.Now().Add(-time.Second))
			var compatibility *CompatibilityError
			if !errors.As(err, &compatibility) || compatibility.Code != sample.issue {
				t.Fatalf("error=%v, want issue=%s", err, sample.issue)
			}
			if waited := errors.Is(ctx.Err(), context.DeadlineExceeded); waited != sample.wait {
				t.Fatalf("waited until deadline=%v, want=%v", waited, sample.wait)
			}
		})
	}
}
