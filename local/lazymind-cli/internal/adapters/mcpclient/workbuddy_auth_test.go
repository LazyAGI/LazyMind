package mcpclient

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workbuddyadapter "lazymind/agentconnector/internal/adapters/workbuddy"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/workbuddyhost"
)

func TestWorkBuddyConnectChecksAuthenticationBeforeConfigurationOrRestart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		auth  workbuddyadapter.AuthenticationResult
		state agentintegration.State
		issue string
		login bool
		stage string
	}{
		{name: "signed out", auth: workbuddyadapter.AuthenticationResult{LoginRequired: true, Message: "signed out"}, state: agentintegration.ActionRequired, issue: "workbuddy_login_required", login: true, stage: "login"},
		{name: "expired", auth: workbuddyadapter.AuthenticationResult{LoginRequired: true, Message: "expired"}, state: agentintegration.ActionRequired, issue: "workbuddy_login_required", login: true, stage: "login"},
		{name: "unreadable", auth: workbuddyadapter.AuthenticationResult{Message: "fixture private diagnostic"}, state: agentintegration.Failed, issue: "workbuddy_auth_unreadable", stage: "login"},
		// A valid WorkBuddy session proceeds to the separate LazyMind account
		// check. It must not be sent back through WorkBuddy login.
		{name: "signed in", auth: workbuddyadapter.AuthenticationResult{Ready: true}, state: agentintegration.Failed, issue: "lazymind_auth_required", stage: "pairing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			setTestHome(t, root)
			home := filepath.Join(root, "lazy-home")
			t.Setenv("LAZYMIND_HOME", home)
			t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
			installWorkBuddyAuthTestDesktop(t, root)
			path := configPath(WorkBuddy)
			original := `{"mcpServers":{"other":{"command":"keep"}}}`
			writeTestFile(t, path, original)
			previous := workBuddyAuthentication
			calls := 0
			workBuddyAuthentication = func() workbuddyadapter.AuthenticationResult { calls++; return tc.auth }
			t.Cleanup(func() { workBuddyAuthentication = previous })
			adapter := testAdapter(WorkBuddy)
			adapter.self, _ = os.Executable()
			adapter.home = home
			adapter.bridge = dshTestProbe{}
			status := adapter.Connect(context.Background())
			if calls != 1 || status.State != tc.state || status.Issue == nil || status.Issue.Code != tc.issue {
				t.Fatalf("calls=%d status=%+v issue=%+v", calls, status, status.Issue)
			}
			if tc.login {
				if status.Action == nil || status.Action.Kind != "login" {
					t.Fatalf("missing login action: %+v", status.Action)
				}
			} else if status.Action != nil {
				t.Fatalf("unknown auth must not initiate login: %+v", status.Action)
			}
			if status.Issue.Stage != tc.stage || status.Issue.OperationID == "" {
				t.Fatalf("missing login operation correlation: %+v", status.Issue)
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != original {
				t.Fatalf("auth precheck changed MCP config: %s, %v", body, err)
			}
			if enabled, err := integrationstate.Read(home, "workbuddy"); err != nil || enabled != nil {
				t.Fatalf("auth precheck changed enable preference: %v, %v", enabled, err)
			}
			for _, path := range []string{workbuddyhost.ConfigPath(home), filepath.Join(home, "workflow-hosts")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("auth precheck created pairing/configuration %q: %v", path, err)
				}
			}
			log, err := os.ReadFile(filepath.Join(home, "logs", "agent-setup.log"))
			if err != nil {
				t.Fatal(err)
			}
			for _, stage := range []string{"pairing", "configuration", "restart", "control"} {
				if stage == tc.stage {
					continue
				}
				if strings.Contains(string(log), `"stage":"`+stage+`"`) {
					t.Fatalf("auth failure advanced to %s", stage)
				}
			}
			if strings.Contains(string(log)+status.Message, "fixture private diagnostic") {
				t.Fatal("auth inspection detail leaked into user result or log")
			}
		})
	}
}

func installWorkBuddyAuthTestDesktop(t *testing.T, root string) {
	t.Helper()
	app := filepath.Join(root, "WorkBuddy.exe")
	writeTestFile(t, app, "non-executable fixture")
	if _, err := agentexec.SetExecutableBinding(agentexec.WorkBuddyDesktop, app); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"version":"5.9.3"}`)
	header, err := json.Marshal(map[string]any{"files": map[string]any{"package.json": map[string]any{"size": len(manifest), "offset": "0"}}})
	if err != nil {
		t.Fatal(err)
	}
	size := (len(header) + 3) &^ 3
	archive := make([]byte, 16+size)
	binary.LittleEndian.PutUint32(archive, 4)
	binary.LittleEndian.PutUint32(archive[4:], uint32(size+8))
	binary.LittleEndian.PutUint32(archive[8:], uint32(size+4))
	binary.LittleEndian.PutUint32(archive[12:], uint32(len(header)))
	copy(archive[16:], header)
	archive = append(archive, manifest...)
	writeTestFile(t, filepath.Join(root, "resources", "app.asar"), string(archive))
}
