package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workbuddyhost"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestWorkBuddyStatusTrustGuidance(t *testing.T) {
	for _, tc := range []struct {
		name, health, approvals, want           string
		heartbeat, revoke, disablePair, signOut bool
	}{
		{name: "first rejection", approvals: "{}", heartbeat: true, want: "mcp_trust_required"},
		{name: "missing approvals", approvals: "missing", heartbeat: true, want: "mcp_trust_required"},
		{name: "revoked trust", revoke: true, heartbeat: true, want: "mcp_trust_required"},
		{name: "revoked trust without heartbeat", revoke: true, want: "mcp_trust_required"},
		{name: "revoked trust with CDP outage", revoke: true, heartbeat: true, health: "cdp_unavailable", want: "mcp_trust_required"},
		{name: "revoked trust while closed", revoke: true, health: "waiting_for_app", want: "mcp_trust_required"},
		{name: "revoked trust with startup outage", revoke: true, health: "startup_unavailable", want: "mcp_trust_required"},
		{name: "trusted connection", heartbeat: true},
		{name: "trusted CDP outage", heartbeat: true, health: "cdp_unavailable", want: "cdp_unavailable"},
		{name: "trusted missing heartbeat", want: "host_unavailable"},
		{name: "unreadable approvals", approvals: "{", heartbeat: true, want: "configuration_unreadable"},
		{name: "approvals read failure", approvals: "directory", heartbeat: true, want: "configuration_unreadable"},
		{name: "null approvals store", approvals: "null", heartbeat: true, want: "configuration_unreadable"},
		{name: "invalid approval timestamp", approvals: "invalid-timestamp", heartbeat: true, want: "configuration_unreadable"},
		{name: "disabled pairing", approvals: "{}", disablePair: true, want: "pairing_disabled"},
		{name: "signed out", approvals: "{}", signOut: true, want: "lazymind_auth_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			a := testAdapter(WorkBuddy)
			a.home = filepath.Join(root, "lazy-home")
			a.self, _ = os.Executable()
			t.Setenv("LAZYMIND_HOME", a.home)
			t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
			installWorkBuddyAuthTestDesktop(t, root)
			store, err := credentials.NewStore(a.home, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(credentials.Credentials{ServerURL: "http://localhost:8092", Username: "fixture",
				AccessToken: "fixture-access", RefreshToken: "fixture-refresh", ExpiresIn: 3600}); err != nil {
				t.Fatal(err)
			}
			account, err := store.AccountScope()
			if err != nil {
				t.Fatal(err)
			}
			path := configPath(WorkBuddy)
			if err := writeManagedConfig(WorkBuddy, path, a.self, a.home, a.hostID, false); err != nil {
				t.Fatal(err)
			}
			pair, err := workflowhost.EnsureForProvider(a.home, "workbuddy", filepath.Dir(path), account)
			if err != nil {
				t.Fatal(err)
			}
			config := workbuddyhost.Config{PairingFile: filepath.Join(a.home, "workflow-hosts", pair.ConnectorID+".json"),
				Node: filepath.Join(root, "node"), CDPPort: 9333}
			body, _ := json.Marshal(config)
			writeTestFile(t, workbuddyhost.ConfigPath(a.home), string(body))
			if tc.heartbeat {
				body, _ = json.Marshal(workbuddyhost.Status{ConnectorID: pair.ConnectorID, CheckedAt: time.Now().UnixMilli(),
					Ready: true, Cancellation: "session", Version: "5.9.3", BridgeURL: "http://127.0.0.1:9334",
					Presentation: workbuddyhost.PresentationStatus{Status: "mounted"}})
				writeTestFile(t, config.PairingFile+".workbuddy-status.json", string(body))
			}
			previous := workBuddyStartupHealth
			workBuddyStartupHealth = func(context.Context, string, int) string { return tc.health }
			t.Cleanup(func() { workBuddyStartupHealth = previous })
			// This managed command has fixed arguments and one environment key.
			// The parser's independent golden fixtures cover approval-key hashing.
			hash := sha256.Sum256([]byte(a.self + "|mcp,proxy|LAZYMIND_CONNECTOR_CONFIG"))
			approved := fmt.Sprintf(`{"%x::lazymind":1792563117657}`, hash)
			approvalsPath := filepath.Join(filepath.Dir(path), "mcp-approvals.json")
			writeTestFile(t, approvalsPath, approved)
			if tc.revoke {
				before := a.Status(context.Background())
				if before.Connection == nil || !before.Connection.MCPTrusted {
					t.Fatalf("fixture is not trusted: %+v", before)
				}
				writeTestFile(t, approvalsPath, "{}")
			} else if tc.approvals != "" {
				value := tc.approvals
				if value == "invalid-timestamp" {
					value = fmt.Sprintf(`{"%x::lazymind":"invalid"}`, hash)
				}
				if value == "missing" || value == "directory" {
					if err := os.Remove(approvalsPath); err != nil {
						t.Fatal(err)
					}
					if value == "directory" {
						if err := os.Mkdir(approvalsPath, 0700); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					writeTestFile(t, approvalsPath, value)
				}
			}
			if tc.disablePair {
				if err := workflowhost.Disable(a.home, pair.ConnectorID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.signOut {
				if err := os.Remove(filepath.Join(a.home, "credentials.json")); err != nil {
					t.Fatal(err)
				}
			}
			status := a.Status(context.Background())
			got := ""
			if status.Issue != nil {
				got = status.Issue.Code
			}
			if got != tc.want {
				t.Fatalf("issue=%q want %q; status=%+v connection=%+v", got, tc.want, status, status.Connection)
			}
			if status.Enabled == nil || !*status.Enabled || !status.CanDisconnect {
				t.Fatalf("lost configured switch: %+v", status)
			}
			if tc.want == "mcp_trust_required" {
				if status.State != agentintegration.Enabled || status.Connection.MCPTrusted || status.Message != workBuddyIssueMessage(tc.want) {
					t.Fatalf("incorrect trust guidance: %+v connection=%+v", status, status.Connection)
				}
				// Re-approval removes only the trust issue, retaining actual outages.
				writeTestFile(t, approvalsPath, approved)
				restored := a.Status(context.Background())
				if !restored.Connection.MCPTrusted || restored.Issue != nil && restored.Issue.Code == "mcp_trust_required" {
					t.Fatalf("stale trust guidance after approval: %+v", restored)
				}
			}
		})
	}
}
