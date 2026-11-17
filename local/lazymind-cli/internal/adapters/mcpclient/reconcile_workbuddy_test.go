package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/connectorlauncher"
	"lazymind/agentconnector/internal/mcpbridge"
)

func TestReconcileWorkBuddyTransfersOnlyExistingTrust(t *testing.T) {
	for _, scenario := range []string{"approved", "missing approval", "revoked old", "revoked old with approved destination", "missing old with approved destination", "revoked destination", "existing destination", "foreign", "absent store"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			a := testAdapter(WorkBuddy)
			a.home = filepath.Join(root, "state")
			a.self, _ = os.Executable()
			c := connectorconfig.Config{Home: a.home, Provider: string(WorkBuddy), HostID: a.hostID, Controlled: true}
			if scenario == "foreign" {
				c.HostID = "foreign"
			}
			reference, err := connectorconfig.Save(c)
			if err != nil {
				t.Fatal(err)
			}
			old := managedStdio(filepath.Join(root, "removed", "lazymind"), a.home, a.hostID, WorkBuddy)
			old.Env[connectorconfig.EnvironmentKey] = reference
			after := old
			after.Command = a.self
			const cwd = "/preserve/cwd"
			oldKey, newKey := workbuddyApprovalKey(old, cwd), workbuddyApprovalKey(after, cwd)
			approvals := map[string]json.RawMessage{"unrelated::server": json.RawMessage(`null`), oldKey: json.RawMessage(`1792563117657`)}
			switch scenario {
			case "missing approval":
				delete(approvals, oldKey)
			case "revoked old":
				approvals[oldKey] = json.RawMessage(`null`)
			case "revoked old with approved destination":
				approvals[oldKey] = json.RawMessage(`null`)
				approvals[newKey] = json.RawMessage(`1792563117657`)
			case "missing old with approved destination":
				delete(approvals, oldKey)
				approvals[newKey] = json.RawMessage(`1792563117657`)
			case "revoked destination":
				approvals[newKey] = json.RawMessage(`null`)
			case "existing destination":
				approvals[newKey] = json.RawMessage(`1234`)
			}
			path := configPath(WorkBuddy)
			fields := rawStdioFields(t, old)
			fields["disabled"] = json.RawMessage(`true`)
			fields["cwd"], _ = json.Marshal(cwd)
			writeReconcileJSON(t, path, fields)
			store := filepath.Join(filepath.Dir(path), "mcp-approvals.json")
			before, _ := json.Marshal(approvals)
			if scenario != "absent store" {
				writeTestFile(t, store, string(before))
			}
			if err := a.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if scenario == "absent store" {
				if pathExists(store) {
					t.Fatal("migration created trust without an approval store")
				}
				return
			}
			body, _ := os.ReadFile(store)
			invalidatedLeftover := scenario == "revoked old with approved destination" || scenario == "missing old with approved destination"
			if scenario != "approved" && !invalidatedLeftover && !bytes.Equal(body, before) {
				t.Fatal("migration changed a trust decision")
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got[oldKey], approvals[oldKey]) || string(got["unrelated::server"]) != "null" {
				t.Fatal("migration lost existing approval decisions")
			}
			if scenario == "approved" && string(got[newKey]) != "1792563117657" {
				t.Fatal("existing approval timestamp was not preserved")
			}
			if scenario == "revoked destination" && string(got[newKey]) != "null" {
				t.Fatal("migration overrode explicit revocation")
			}
			if invalidatedLeftover && string(got[newKey]) != "null" {
				t.Fatal("an interrupted migration revived missing or revoked trust")
			}
			if scenario == "existing destination" && string(got[newKey]) != "1234" {
				t.Fatal("migration overwrote existing destination approval")
			}
			trusted, enabled, err := workbuddySetup(path)
			wantTrust := scenario == "approved" || scenario == "existing destination" || scenario == "foreign"
			if err != nil || trusted != wantTrust || enabled {
				t.Fatalf("trust=%v enabled=%v err=%v", trusted, enabled, err)
			}
			assertReconcileDoesNotRewrite(t, a, store)
		})
	}
}

func TestNewWorkBuddyKeepsTrustAcrossLaterInstallationMoves(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	home := filepath.Join(root, "state")
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv(connectorconfig.EnvironmentKey, "")
	a, err := New(WorkBuddy, "", &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connectorconfig.Save(connectorconfig.Config{Home: home, Provider: string(WorkBuddy), HostID: a.hostID, Controlled: true}); err != nil {
		t.Fatal(err)
	}
	old := managedStdio(filepath.Join(root, "removed install", "lazymind"), home, a.hostID, WorkBuddy)
	path := configPath(WorkBuddy)
	writeReconcileJSON(t, path, rawStdioFields(t, old))
	approvalPath := filepath.Join(filepath.Dir(path), "mcp-approvals.json")
	body, _ := json.Marshal(map[string]any{workbuddyApprovalKey(old, ""): 1792563117657})
	writeTestFile(t, approvalPath, string(body))
	if err := a.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	trusted, enabled, err := workbuddySetup(path)
	if err != nil || !trusted || !enabled {
		t.Fatalf("trust was not relocated: trusted=%v enabled=%v err=%v", trusted, enabled, err)
	}
	beforeConfig, _ := os.ReadFile(path)
	beforeApprovals, _ := os.ReadFile(approvalPath)
	newInstall := filepath.Join(root, "another installation", "lazymind")
	writeTestFile(t, newInstall, "new installation executable")
	if _, err := connectorlauncher.Publish(context.Background(), home, newInstall); err != nil {
		t.Fatal(err)
	}
	next, err := New(WorkBuddy, "", &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterConfig, _ := os.ReadFile(path)
	afterApprovals, _ := os.ReadFile(approvalPath)
	if !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforeApprovals, afterApprovals) {
		t.Fatal("later installation move changed a trusted fixed registration")
	}
	trusted, _, err = workbuddySetup(path)
	if err != nil || !trusted {
		t.Fatal("fixed launcher lost native trust", err)
	}
}
