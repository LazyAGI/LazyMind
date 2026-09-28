package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestPluginConnectOfflineMigrationFailureAndDisconnect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	root := t.TempDir()
	home := filepath.Join(root, "state")
	profile := filepath.Join(root, "profile")
	t.Setenv("HOME", root)
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv("CODEX_HOME", profile)
	bindCodexDesktopForTest(t, root)
	store, _ := credentials.NewStore(home, "")
	if err := store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:1", AccessToken: "test", RefreshToken: "test"}); err != nil {
		t.Fatal(err)
	}
	self := writeExecutable(t, root, "lazymind", "#!/bin/sh\nexit 0\n")
	node := writeExecutable(t, root, "node", "#!/bin/sh\necho v22.23.0\n")
	t.Setenv("LAZYMIND_NODE_BIN", node)
	binary := writeExecutable(t, root, "codex", `#!/bin/sh
if [ "$1" = "queue" ]; then exit 0; fi
if [ "$1" = "plugin" ] && [ "$2" = "add" ]; then
 if [ -f "$CODEX_HOME/fail-install" ]; then echo install-failed >&2; exit 1; fi
 printf '\n[plugins."%s"]\nenabled = true\n' "$3" >> "$CODEX_HOME/config.toml"
 echo installed >> "$CODEX_HOME/events"
 exit 0
fi
if [ "$1" = "mcp" ] && [ "$2" = "remove" ]; then
 printf '[mcp_servers.other]\ncommand = "other-server"\n[plugins."lazymind-workflow@personal"]\nenabled = true\n' > "$CODEX_HOME/config.toml"
 echo migrated >> "$CODEX_HOME/events"
 exit 0
fi
if [ "$1" = "plugin" ] && [ "$2" = "remove" ]; then
 printf '[mcp_servers.other]\ncommand = "other-server"\n' > "$CODEX_HOME/config.toml"
 exit 0
fi
exit 1
`)
	adapter, err := New(binary, self, &mcpbridge.Bridge{})
	if err != nil {
		t.Fatal(err)
	}
	original := "[mcp_servers.lazymind]\ncommand = \"" + self + "\"\nargs = [\"mcp\", \"proxy\"]\n[mcp_servers.other]\ncommand = \"other-server\"\n"
	path := filepath.Join(profile, "config.toml")
	_ = os.WriteFile(path, []byte(original), 0600)
	fail := filepath.Join(profile, "fail-install")
	_ = os.WriteFile(fail, []byte("fail"), 0600)
	if status := adapter.Connect(context.Background()); status.State != agentintegration.Failed {
		t.Fatalf("expected failed installation: %+v", status)
	}
	if data, _ := os.ReadFile(path); string(data) != original {
		t.Fatal("failed install changed original MCP")
	}
	_ = os.Remove(fail)
	if status := adapter.Connect(context.Background()); status.State != agentintegration.Enabled {
		t.Fatalf("offline install failed: %+v", status)
	}
	events, _ := os.ReadFile(filepath.Join(profile, "events"))
	if string(events) != "installed\nmigrated\n" {
		t.Fatalf("migration ordering: %s", events)
	}
	if status := adapter.Connect(context.Background()); status.State != agentintegration.Enabled {
		t.Fatalf("idempotent connect failed: %+v", status)
	}
	after, _ := os.ReadFile(filepath.Join(profile, "events"))
	if string(after) != string(events) {
		t.Fatal("repeated connect reinstalled plugin")
	}
	record, err := codexplugin.Read()
	if err != nil {
		t.Fatal(err)
	}
	if status := adapter.Disconnect(context.Background()); status.State != agentintegration.Ready {
		t.Fatalf("disconnect failed: %+v", status)
	}
	account, _ := store.AccountScope()
	if workflowhost.Configured(home, record.ConnectorID, account) {
		t.Fatal("disconnect left pairing enabled")
	}
	config, _ := os.ReadFile(path)
	if !strings.Contains(string(config), "other-server") {
		t.Fatal("disconnect removed unrelated server")
	}
}
