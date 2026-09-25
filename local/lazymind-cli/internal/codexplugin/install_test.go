package codexplugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func fixture(t *testing.T) (home, profile, self, binary string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	user := t.TempDir()
	home, profile = filepath.Join(user, "state"), filepath.Join(user, "codex-profile")
	t.Setenv("HOME", user)
	t.Setenv("CODEX_HOME", profile)
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := credentials.NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: "http://localhost:8090", AccessToken: "test-access", RefreshToken: "test-refresh"}); err != nil {
		t.Fatal(err)
	}
	self = executable(t, user, "lazymind", "#!/bin/sh\nexit 0\n")
	binary = executable(t, user, "codex", `#!/bin/sh
if [ "$1" = "queue" ] && [ "$2" = "--help" ]; then exit 0; fi
if [ "$1" = "plugin" ] && [ "$2" = "add" ]; then
 printf '[plugins."%s"]\nenabled = true\n' "$3" > "$CODEX_HOME/config.toml"
 printf '{"installed":true}\n'
 exit 0
fi
exit 1
`)
	node := executable(t, user, "node", "#!/bin/sh\nprintf 'v22.23.0\\n'\n")
	t.Setenv("LAZYMIND_NODE_BIN", node)
	return
}
func executable(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallPairsConfiguresAndReinstallsWithoutDuplicates(t *testing.T) {
	home, profile, self, binary := fixture(t)
	t.Setenv("LAZYMIND_WEB_URL", "https://panel.example.test")
	root, market, _ := Paths()
	existing := map[string]any{"name": "personal", "interface": map[string]any{"displayName": "My plugins"}, "plugins": []any{
		map[string]any{"name": "existing", "source": map[string]any{"source": "local", "path": "./plugins/existing"}},
	}}
	if err := writeJSON(market, existing); err != nil {
		t.Fatal(err)
	}
	first, err := Install(context.Background(), home, self, binary, profile, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Install(context.Background(), home, self, binary, profile, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.ConnectorID != second.ConnectorID || first.Version != second.Version {
		t.Fatal("reinstall changed stable identity")
	}
	enabled, err := Enabled(profile, first.Marketplace)
	if err != nil || !enabled {
		t.Fatalf("plugin not enabled: %v", err)
	}
	var document map[string]any
	data, _ := os.ReadFile(market)
	_ = json.Unmarshal(data, &document)
	if len(document["plugins"].([]any)) != 2 || document["interface"].(map[string]any)["displayName"] != "My plugins" {
		t.Fatal("marketplace contents were not preserved")
	}
	data, _ = os.ReadFile(filepath.Join(root, ".mcp.json"))
	_ = json.Unmarshal(data, &document)
	server := document["mcpServers"].(map[string]any)["lazymind"].(map[string]any)
	env := server["env"].(map[string]any)
	if server["command"] != self || server["args"].([]any)[1] != "codex-workflow" || env["LAZYMIND_WORKFLOW_PAIRING_FILE"] != first.PairingFile || env["LAZYMIND_WEB_URL"] != "https://panel.example.test" {
		t.Fatal("incomplete plugin configuration")
	}
	store, _ := credentials.NewStore(home, "")
	account, _ := store.AccountScope()
	if !workflowhost.Configured(home, first.ConnectorID, account) {
		t.Fatal("missing pairing")
	}
	var pair workflowhost.Pairing
	pairBytes, _ := os.ReadFile(first.PairingFile)
	_ = json.Unmarshal(pairBytes, &pair)
	if strings.Contains(string(data), pair.Token) {
		t.Fatal("secret embedded in plugin")
	}
	record, err := Read()
	if err != nil || record.BuildID != BuildID() {
		t.Fatalf("missing installation record: %v", err)
	}
}

func TestInstallRejectsForeignPluginAndInvalidRuntime(t *testing.T) {
	home, profile, self, binary := fixture(t)
	root, _, _ := Paths()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "user.txt")
	_ = os.WriteFile(marker, []byte("keep"), 0600)
	if _, err := Install(context.Background(), home, self, binary, profile, "host"); err == nil {
		t.Fatal("overwrote foreign plugin")
	}
	if data, _ := os.ReadFile(marker); string(data) != "keep" {
		t.Fatal("foreign content changed")
	}
	oldNode := executable(t, t.TempDir(), "node", "#!/bin/sh\necho v20.10.0\n")
	t.Setenv("LAZYMIND_NODE_BIN", oldNode)
	if _, err := FindNode(context.Background()); err == nil {
		t.Fatal("accepted unsupported node")
	}
}

func TestMarketplaceConflictDoesNotReplaceEntry(t *testing.T) {
	_, _, _, _ = fixture(t)
	_, path, _ := Paths()
	before := map[string]any{"name": "personal", "plugins": []any{map[string]any{"name": Name, "source": map[string]any{"source": "local", "path": "./other"}}}}
	if err := writeJSON(path, before); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if _, err := readMarketplace(path); err == nil {
		t.Fatal("accepted conflicting entry")
	}
	after, _ := os.ReadFile(path)
	if string(data) != string(after) {
		t.Fatal("modified marketplace on conflict")
	}
}

func TestFailedInstallRemainsRetryable(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "first install", true: "upgrade"}[upgrade], func(t *testing.T) {
			home, profile, self, binary := fixture(t)
			workingBinary, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			if upgrade {
				if _, err := Install(t.Context(), home, self, binary, profile, "host"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = \"queue\" ]; then exit 0; fi\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(t.Context(), home, self, binary, profile, "host"); err == nil {
				t.Fatal("expected install failure")
			}
			record, err := Read()
			if err != nil || record.BuildID != "" || record.Home != home || record.Profile != profile {
				t.Fatalf("failed install must retain ownership without a successful build: %+v %v", record, err)
			}
			if err := os.WriteFile(binary, workingBinary, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(t.Context(), home, self, binary, profile, "host"); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			record, err = Read()
			if err != nil || record.BuildID != BuildID() {
				t.Fatalf("successful retry did not commit build: %+v %v", record, err)
			}
		})
	}
}
