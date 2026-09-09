package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
)

func TestCursorStatusUsesFilesystemRequirementWithoutRunningDesktop(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("LAZYMIND_HOME", filepath.Join(home, "lazymind"))
	adapter := testAdapter(Cursor)

	status := adapter.Status(context.Background())
	if status.State != agentintegration.RequirementsMissing {
		t.Fatalf("state=%q, want requirements_missing", status.State)
	}

	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	bindDesktopForTest(t, agentexec.CursorDesktop)
	status = adapter.Status(context.Background())
	if status.State != agentintegration.Ready {
		t.Fatalf("state=%q, want ready", status.State)
	}
}

func TestCursorInstallURLCarriesOneManagedStdioDefinition(t *testing.T) {
	adapter := testAdapter(Cursor)
	value, err := adapter.cursorInstallURL()
	if err != nil {
		t.Fatal(err)
	}
	installURL, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if installURL.Scheme != "cursor" || installURL.Host != "anysphere.cursor-deeplink" || installURL.Path != "/mcp/install" {
		t.Fatalf("install URL=%q", value)
	}
	encoded, err := base64.StdEncoding.DecodeString(installURL.Query().Get("config"))
	if err != nil {
		t.Fatal(err)
	}
	assertStdioDefinition(t, encoded, adapter.self, adapter.home, Cursor)
}

func TestCursorInstallURLPreservesNativeWindowsPaths(t *testing.T) {
	adapter := &Adapter{
		kind: Cursor, self: `C:\Program Files\LazyMind\runtime\bin\lazymind.exe`,
		home: `C:\Users\Alice\AppData\Local\LazyMind`, hostID: "host-1",
	}
	value, err := adapter.cursorInstallURL()
	if err != nil {
		t.Fatal(err)
	}
	installURL, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := base64.StdEncoding.DecodeString(installURL.Query().Get("config"))
	if err != nil {
		t.Fatal(err)
	}
	assertStdioDefinition(t, encoded, adapter.self, adapter.home, Cursor)
}

func TestWorkBuddyUsesWorkBuddyConfiguration(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("LAZYMIND_HOME", filepath.Join(home, "lazymind"))
	path := configPath(WorkBuddy)
	if path != filepath.Join(home, ".workbuddy", "mcp.json") {
		t.Fatalf("path=%q", path)
	}
	if strings.Contains(path, ".codebuddy") {
		t.Fatalf("WorkBuddy path must not target CodeBuddy: %q", path)
	}
}

func TestRaccoonUsesDesktopConfiguration(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	path := configPath(Raccoon)
	if path != filepath.Join(home, ".box-agent", "config", "mcp.json") {
		t.Fatalf("path=%q", path)
	}
	adapter := testAdapter(Raccoon)
	status := adapter.Status(context.Background())
	if status.State != agentintegration.RequirementsMissing {
		t.Fatalf("status=%#v", status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	bindDesktopForTest(t, agentexec.RaccoonDesktop)
	status = adapter.Status(context.Background())
	if status.State != agentintegration.Ready {
		t.Fatalf("status=%#v", status)
	}
}

func TestDeepSeekRequiresInitializedProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DSH_HOME", root)
	adapter := testAdapter(DeepSeekHarness)

	status := adapter.Status(context.Background())
	if status.State != agentintegration.RequirementsMissing || len(status.Requirements) != 1 {
		t.Fatalf("status=%#v", status)
	}
	writeTestFile(t, filepath.Join(root, "profiles", "web", "package.json"), `{}`)
	status = adapter.Status(context.Background())
	if status.State != agentintegration.Ready {
		t.Fatalf("status=%#v", status)
	}
}

func TestRunDSHPluginRequiresInitializedProfile(t *testing.T) {
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	err := runDSHPlugin(context.Background(), "web", "add", "pkg")
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunDSHPluginInstallsIntoExistingProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake pnpm")
	}
	root := t.TempDir()
	t.Setenv("DSH_HOME", root)
	profile := filepath.Join(root, "profiles", "web")
	writeTestFile(t, filepath.Join(profile, "package.json"), `{
  "dependencies": {
    "@lazymind/dsh-workflow": "file:plugin"
  },
  "dsh": {"profile": {"bundles": ["@deepseek-ai/dsh-base"]}}
}`)
	writeTestFile(t, filepath.Join(profile, "node_modules", "@lazymind", "dsh-workflow", "package.json"), `{
  "name": "@lazymind/dsh-workflow",
  "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}
}`)
	bin := t.TempDir()
	record := filepath.Join(bin, "args.txt")
	writeFakePNPMWithScript(t, bin, "printf '%s\\n' \"$*\" > \"$PNPM_ARGS_FILE\"; pwd >> \"$PNPM_ARGS_FILE\"\n")
	t.Setenv("PATH", bin)
	t.Setenv("PNPM_ARGS_FILE", record)
	if err := runDSHPlugin(context.Background(), "web", "add", "pkg"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) < 2 {
		t.Fatalf("pnpm invocation=%q", body)
	}
	wantDir, err := filepath.EvalSymlinks(profile)
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := filepath.EvalSymlinks(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	if lines[0] != "add pkg" || gotDir != wantDir {
		t.Fatalf("pnpm invocation=%q", body)
	}
	manifest, err := os.ReadFile(filepath.Join(profile, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		DSH struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		t.Fatal(err)
	}
	if got := parsed.DSH.Profile.Bundles; len(got) != 2 || got[0] != "@deepseek-ai/dsh-base" || got[1] != "@lazymind/dsh-workflow" {
		t.Fatalf("bundles=%#v", got)
	}
}

func TestManagedJSONConfigPreservesOtherServersAndRemovesOnlyLazyMind(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mcp.json")
	selfName := "lazymind"
	if runtime.GOOS == "windows" {
		selfName += ".exe"
	}
	self := filepath.Join(root, "bin", selfName)
	home := filepath.Join(root, "home")
	writeTestFile(t, self, "test connector")
	writeTestFile(t, path, `{"theme":"dark","mcpServers":{"existing":{"description":"keep","url":"https://example.com/mcp","type":"streamable_http","alwaysLoad":true,"disabled":false,"connect_timeout":15}}}`)

	if err := writeManagedConfig(Cursor, path, self, home, "host-1", false); err != nil {
		t.Fatal(err)
	}
	state, err := readManagedConfig(Cursor, path, self, home, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.configured || !state.owned || !state.current {
		t.Fatalf("managed state=%#v", state)
	}
	var configured struct {
		Theme      string                     `json:"theme"`
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	body, _ := os.ReadFile(path)
	if err := json.Unmarshal(body, &configured); err != nil {
		t.Fatal(err)
	}
	var existing map[string]any
	if err := json.Unmarshal(configured.MCPServers["existing"], &existing); err != nil {
		t.Fatal(err)
	}
	if configured.Theme != "dark" || len(existing) != 6 || existing["description"] != "keep" ||
		existing["url"] != "https://example.com/mcp" || existing["connect_timeout"] != float64(15) {
		t.Fatalf("unrelated configuration changed: %#v", configured)
	}
	if _, err := os.Stat(path + ".lazymind-backup"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	if err := removeManagedConfig(Cursor, path); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	configured.MCPServers = nil
	if err := json.Unmarshal(body, &configured); err != nil {
		t.Fatal(err)
	}
	existing = nil
	if err := json.Unmarshal(configured.MCPServers["existing"], &existing); err != nil {
		t.Fatal(err)
	}
	if _, exists := configured.MCPServers[serverName]; exists || len(existing) != 6 ||
		existing["description"] != "keep" || existing["url"] != "https://example.com/mcp" ||
		existing["connect_timeout"] != float64(15) {
		t.Fatalf("unexpected servers after disconnect: %#v", configured.MCPServers)
	}
}

func TestForeignLazyMindEntryBecomesConflict(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	path := filepath.Join(home, ".workbuddy", "mcp.json")
	writeTestFile(t, path, `{"mcpServers":{"lazymind":{"command":"foreign","args":["run"]}}}`)
	adapter := testAdapter(WorkBuddy)
	status := adapter.Status(context.Background())
	if status.State != agentintegration.Conflict {
		t.Fatalf("status=%#v", status)
	}
}

func TestManagedDSHConfigPreservesOtherPatchEntries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cordis.patch.yml")
	self := filepath.Join(root, "bin", "lazymind")
	home := filepath.Join(root, "home")
	writeTestFile(t, self, "test connector")
	writeTestFile(t, path, "- insert:\n    - id: existing\n      name: existing-plugin\n      config:\n        value: keep\n")

	if err := writeManagedConfig(DeepSeekHarness, path, self, home, "host-1", false); err != nil {
		t.Fatal(err)
	}
	if err := removeManagedConfig(DeepSeekHarness, path); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "id: existing") || strings.Contains(string(body), "mcp-lazymind") {
		t.Fatalf("unexpected DSH patch:\n%s", body)
	}
}

func testAdapter(kind Kind) *Adapter {
	return &Adapter{
		kind:   kind,
		self:   filepath.Join(string(filepath.Separator), "opt", "lazymind"),
		home:   filepath.Join(string(filepath.Separator), "tmp", "lazymind-home"),
		hostID: "host-1",
	}
}

func assertStdioDefinition(t *testing.T, body []byte, command, home string, kind Kind) {
	t.Helper()
	var definition stdioMCPDefinition
	if err := json.Unmarshal(body, &definition); err != nil {
		t.Fatal(err)
	}
	if definition.Command != command || len(definition.Args) != 2 || definition.Args[0] != "mcp" || definition.Args[1] != "proxy" {
		t.Fatalf("definition=%#v", definition)
	}
	if definition.Env["LAZYMIND_HOME"] != home ||
		definition.Env["LAZYMIND_AGENT_PROVIDER"] != string(kind) ||
		definition.Env["LAZYMIND_AGENT_HOST_ID"] != "host-1" || len(definition.Env) != 3 {
		t.Fatalf("env=%#v", definition.Env)
	}
}

func writeTestFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func bindDesktopForTest(t *testing.T, target agentexec.BindingTarget) {
	t.Helper()
	path := filepath.Join(t.TempDir(), string(target)+".exe")
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agentexec.SetExecutableBinding(target, path); err != nil {
		t.Fatal(err)
	}
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func writeFakePNPMWithScript(t *testing.T, dir, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "pnpm"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDSHEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"https://dsh.example.com", "http://localhost:3000/", "http://127.0.0.1:3000", "http://[::1]:3000"} {
		if err := validateDSHEndpoint(endpoint); err != nil {
			t.Errorf("valid endpoint %q: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"http://dsh.example.com", "https://user:password@dsh.example.com", "https://dsh.example.com/path", "https://dsh.example.com/?token=x", "https://dsh.example.com/#fragment", "file:///tmp/dsh"} {
		if err := validateDSHEndpoint(endpoint); err == nil {
			t.Errorf("unsafe endpoint accepted: %q", endpoint)
		}
	}
}
