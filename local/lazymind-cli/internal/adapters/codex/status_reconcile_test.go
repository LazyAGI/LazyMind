package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/connectorlauncher"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowconnector"
)

func TestStatusKeepsInstalledConfigurationWhenPathRepairFails(t *testing.T) {
	for _, failure := range []string{"configuration_lock", "invalid_launcher_record"} {
		for _, nativeEnabled := range []bool{true, false} {
			name := failure + map[bool]string{true: "/enabled", false: "/disabled"}[nativeEnabled]
			t.Run(name, func(t *testing.T) {
				adapter, _, _ := newPluginReconnectTestAdapter(t)
				record, err := codexplugin.Read()
				if err != nil {
					t.Fatal(err)
				}
				heartbeat, _ := json.Marshal(codexhost.Status{Connector: record.ConnectorID, Ready: true, CheckedAt: time.Now().UnixMilli()})
				if err := os.WriteFile(record.PairingFile+".codex-status.json", heartbeat, 0600); err != nil {
					t.Fatal(err)
				}
				if !nativeEnabled {
					path := filepath.Join(record.Profile, "config.toml")
					body, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(body), "enabled = true", "enabled = false")), 0600); err != nil {
						t.Fatal(err)
					}
				}
				ctx := context.Background()
				if failure == "configuration_lock" {
					unlock, err := localfile.LockContext(ctx, filepath.Join(record.Profile, ".lazymind-integration.lock"))
					if err != nil {
						t.Fatal(err)
					}
					defer unlock()
					bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
					defer cancel()
					ctx = bounded
				} else {
					path := filepath.Join(filepath.Dir(connectorlauncher.Path(adapter.home)), "target.json")
					if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				status := adapter.Status(ctx)
				if status.Enabled == nil || !*status.Enabled || !status.CanDisconnect || status.Connection == nil || !status.Connection.MCPConfigured || status.Connection.MCPEnabled != nativeEnabled {
					t.Fatalf("path repair failure lost saved native state: %+v", status)
				}
				wantIssue := "configuration_failed"
				if !nativeEnabled {
					wantIssue = "mcp_enable_required"
				}
				if status.Issue == nil || status.Issue.Code != wantIssue {
					t.Fatalf("issue=%+v want %s", status.Issue, wantIssue)
				}
				if nativeEnabled && status.State != agentintegration.Failed {
					t.Fatalf("repair failure was not reported: %+v", status)
				}
				if status.DiagnosticID == "" || status.DiagnosticLog == "" {
					t.Fatal("path repair failure lost diagnostics")
				}
				if _, err := os.Stat(status.DiagnosticLog); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestStatusReportsControlAndCoreEvidenceSeparately(t *testing.T) {
	adapter, store, _ := newPluginReconnectTestAdapter(t)
	record, err := codexplugin.Read()
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	_, pair, err := codexhost.Load(adapter.home, account)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		probe, issue, state string
		mcpDisabled         bool
	}{
		{"legacy", workflowconnector.ConnectionPending, "pending", false},
		{"lazymind_auth_required", "lazymind_auth_required", "authorization_required", false},
		{"lazymind_unavailable", "lazymind_unavailable", "unavailable", false},
		{"", "", "connected", false},
		{"", "mcp_enable_required", "connected", true},
	} {
		t.Run(tc.state+"/"+tc.issue, func(t *testing.T) {
			if tc.mcpDisabled {
				path := filepath.Join(record.Profile, "config.toml")
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(body), "enabled = true", "enabled = false")), 0600); err != nil {
					t.Fatal(err)
				}
			}
			at := time.Now().UnixMilli()
			heartbeat := codexhost.Status{Connector: pair.ConnectorID, CheckedAt: at, Ready: true}
			heartbeat.Presentation.Status = "binding-unavailable"
			if tc.probe != "legacy" {
				heartbeat.ConnectionEvidence = workflowconnector.ConnectionEvidence{PairingRevision: pair.RuntimeRevision,
					Connection: &workflowconnector.ConnectionObservation{CheckedAt: at, Issue: tc.probe}}
			}
			data, _ := json.Marshal(heartbeat)
			if err := os.WriteFile(record.PairingFile+".codex-status.json", data, 0600); err != nil {
				t.Fatal(err)
			}
			status := adapter.Status(context.Background())
			conn := status.Connection
			got := ""
			if status.Issue != nil {
				got = status.Issue.Code
			}
			if conn == nil || !conn.ControlReady || conn.MCPEnabled == tc.mcpDisabled || conn.LazyMindState != tc.state ||
				conn.PresentationStatus != "binding-unavailable" || got != tc.issue {
				t.Fatalf("unexpected status: %+v, connection: %+v", status, conn)
			}
		})
	}
}
