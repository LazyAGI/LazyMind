package mcpclient

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/workbuddyhost"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestWorkBuddyPreflightDoesNotMutateAndStillAllowsDisconnect(t *testing.T) {
	for _, tc := range []struct{ version, issue string }{{"5.8.2", "unsupported_version"}, {"unknown", "version_unreadable"}} {
		t.Run(tc.version, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			home := filepath.Join(root, "lazy-home")
			t.Setenv("LAZYMIND_HOME", home)
			t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
			app := filepath.Join(root, "WorkBuddy.exe")
			writeTestFile(t, app, "fixture")
			if _, err := agentexec.SetExecutableBinding(agentexec.WorkBuddyDesktop, app); err != nil {
				t.Fatal(err)
			}
			manifest, _ := json.Marshal(map[string]string{"version": tc.version})
			header, _ := json.Marshal(map[string]any{"files": map[string]any{"package.json": map[string]any{"size": len(manifest), "offset": "0"}}})
			size := (len(header) + 3) &^ 3
			archive := make([]byte, 16+size)
			binary.LittleEndian.PutUint32(archive, 4)
			binary.LittleEndian.PutUint32(archive[4:], uint32(size+8))
			binary.LittleEndian.PutUint32(archive[8:], uint32(size+4))
			binary.LittleEndian.PutUint32(archive[12:], uint32(len(header)))
			copy(archive[16:], header)
			archive = append(archive, manifest...)
			writeTestFile(t, filepath.Join(root, "resources", "app.asar"), string(archive))
			path := configPath(WorkBuddy)
			writeTestFile(t, path, `{"mcpServers":{"other":{"command":"keep"}}}`)
			adapter := testAdapter(WorkBuddy)
			adapter.self, _ = os.Executable()
			adapter.home = home
			adapter.bridge = dshTestProbe{}
			// No credentials are required to classify the unsupported desktop.
			before, _ := os.ReadFile(path)
			status := adapter.Connect(context.Background())
			if status.State != agentintegration.Failed || status.Issue == nil || status.Issue.Code != tc.issue {
				t.Fatalf("status=%+v", status)
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("preflight changed MCP config")
			}
			if _, err := os.Stat(workbuddyhost.ConfigPath(home)); !os.IsNotExist(err) {
				t.Fatalf("preflight created connector config: %v", err)
			}
			// An already-owned MCP entry can still be removed with the same old desktop.
			store, err := credentials.NewStore(home, "")
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:19000", Username: "fixture", AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600, SavedAt: float64(time.Now().Unix())}); err != nil {
				t.Fatal(err)
			}
			if err = writeManagedConfig(WorkBuddy, path, adapter.self, home, adapter.hostID, false); err != nil {
				t.Fatal(err)
			}
			status = adapter.Connect(context.Background())
			if !status.CanDisconnect || status.Issue == nil || status.Issue.Code != tc.issue {
				t.Fatalf("cannot clean up: %+v", status)
			}
			status = adapter.Disconnect(context.Background())
			if status.State == agentintegration.Failed {
				t.Fatalf("disconnect blocked: %+v", status)
			}
			body, _ := os.ReadFile(path)
			var document struct {
				MCPServers map[string]json.RawMessage `json:"mcpServers"`
			}
			json.Unmarshal(body, &document)
			if document.MCPServers["lazymind"] != nil || document.MCPServers["other"] == nil {
				t.Fatal("cleanup did not preserve unrelated entry")
			}
		})
	}
}

func TestWorkBuddySignedOutDisconnectRevokesLocalPairingAndKeepsOtherServers(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	home := filepath.Join(root, "lazy-home")
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", filepath.Join(root, "profile"), "previous-account")
	if err != nil {
		t.Fatal(err)
	}
	config := workbuddyhost.Config{Node: filepath.Join(root, "node"), PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), CDPPort: 1}
	body, _ := json.Marshal(config)
	writeTestFile(t, workbuddyhost.ConfigPath(home), string(body))
	if err = integrationstate.Set(home, "workbuddy", true); err != nil {
		t.Fatal(err)
	}
	adapter := testAdapter(WorkBuddy)
	adapter.self, _ = os.Executable()
	adapter.home = home
	path := configPath(WorkBuddy)
	writeTestFile(t, path, `{"mcpServers":{"other":{"command":"keep"}}}`)
	if err = writeManagedConfig(WorkBuddy, path, adapter.self, home, adapter.hostID, false); err != nil {
		t.Fatal(err)
	}
	// There is deliberately no credential store for the previous account.
	status := adapter.Disconnect(context.Background())
	if status.State == agentintegration.Failed || status.Enabled == nil || *status.Enabled {
		t.Fatalf("disconnect=%+v", status)
	}
	if _, err = workflowhost.Verify(home, pair.ConnectorID, pair.Token, "previous-account"); err == nil {
		t.Fatal("signed-out disconnect retained authorization")
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.MCPServers["other"] == nil || doc.MCPServers["lazymind"] != nil {
		t.Fatal("disconnect changed unrelated MCP entries")
	}
}
