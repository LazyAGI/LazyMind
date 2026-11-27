package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/assistantbridge"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowconnector/connectortest"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestDesktopDSHFirstConnectReplacesLegacyBridge(t *testing.T) {
	legacySource := os.Getenv("LAZYMIND_TEST_LEGACY_BRIDGE_EXE")
	bundledSource := os.Getenv("LAZYMIND_TEST_BRIDGE_EXE")
	if legacySource == "" || bundledSource == "" {
		t.Skip("native Desktop CI supplies both Bridge executables")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	root := t.TempDir()
	home, dshHome := filepath.Join(root, "LazyMind home"), filepath.Join(root, "DSH home")
	profile := filepath.Join(dshHome, "profiles", "desktop")
	resources := filepath.Join(root, "LazyMind.app", "Contents", "Resources", "runtime")
	legacyResources := resources
	if runtime.GOOS == "windows" {
		// The Windows portable ZIP can be unpacked into a new directory while
		// the predecessor's running executable remains locked in place.
		resources = filepath.Join(root, "LazyMind current", "resources", "runtime")
		legacyResources = filepath.Join(root, "LazyMind previous", "resources", "runtime")
	}
	bin := filepath.Join(resources, "bin")
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	bundled := filepath.Join(bin, "lazymind"+suffix)
	legacy := filepath.Join(legacyResources, "bin", "lazymind"+suffix)
	write := func(path string, contents []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0700); err != nil {
			t.Fatal(err)
		}
	}
	copyExe := func(source, destination string) {
		t.Helper()
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		write(destination, body)
	}
	copyExe(legacySource, legacy)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dshResources := filepath.Join(root, "DSH fixture.app", "Contents", "Resources")
	dshExecutable := filepath.Join(filepath.Dir(dshResources), "MacOS", "DeepSeek Harness")
	if runtime.GOOS == "windows" {
		dshResources = filepath.Join(root, "DSH fixture", "resources")
		dshExecutable = filepath.Join(filepath.Dir(dshResources), "DeepSeek Harness.exe")
	}
	dshBin := filepath.Join(dshResources, "runtime", "cli", "bin")
	dsh := filepath.Join(dshBin, "dsh")
	copyExe(self, dshExecutable)
	copyExe(self, filepath.Join(dshBin, desktopDSHInstaller+suffix))
	if runtime.GOOS == "windows" {
		dsh += ".cmd"
		write(dsh, []byte("@echo off\r\n\"%~dp0"+desktopDSHInstaller+".exe\" %*\r\nexit /b %errorlevel%\r\n"))
	} else {
		write(dsh, []byte("#!/bin/sh\nexec \"$(dirname \"$0\")/"+desktopDSHInstaller+"\" \"$@\"\n"))
	}
	write(filepath.Join(dshResources, "app.asar"), []byte("fixture"))
	for _, name := range []string{"desktop", "web"} {
		write(filepath.Join(dshHome, "profiles", name, "package.json"), []byte(`{"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-web-app"]}}}`))
	}
	release := connectortest.WriteDSHPackage(t, bundled, "0.1.0+0000000000000001")

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv("LAZYMIND_HOST_HOME", root)
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
	t.Setenv("LAZYMIND_RUNTIME_RESOURCES_ROOT", resources)
	t.Setenv("LAZYMIND_RUNTIME_OWNER_TOKEN", "native-desktop-bridge-test")
	t.Setenv("LAZYMIND_DESKTOP_OWNER_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LAZYMIND_DESKTOP_OWNER_ORIGIN", "")
	t.Setenv("LAZYMIND_DSH_PATH", "")
	t.Setenv("LAZYMIND_DSH_DESKTOP_PATH", "")
	t.Setenv("LAZYMIND_DSH_PROFILE", "")
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv(connectorconfig.EnvironmentKey, "")
	desktopHome, err := connectorconfig.EnvironmentHome(home, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentexec.SetExecutableBinding(agentexec.DeepSeekHarnessCLI, dsh); err != nil {
		t.Fatal(err)
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "desktop fixture", Version: "1"}, nil)
	for _, tool := range strings.Fields("cloud_document.get cloud_document.list cloud_document.search knowledge.document.get knowledge.document.list knowledge.list knowledge.search model.chat model.list skill.get skill.list tool.call tool.list") {
		mcpServer.AddTool(&mcp.Tool{Name: tool, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	}
	mux := http.NewServeMux()
	mux.Handle("/api/core/mcp/capabilities/v1", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil))
	mux.HandleFunc("/api/core/workflow-control/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"schema_ready":true,"protocol":"workflow.control.v1"}`)
	})
	mux.HandleFunc("/api/core/workflow-host-actions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"actions":[]}`)
	})
	core := httptest.NewServer(mux)
	defer core.Close()
	store, err := credentials.NewStore(desktopHome, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: core.URL, Username: "desktop fixture", AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600, SavedAt: float64(time.Now().Unix())}); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		health, _ := assistantbridge.Health(cleanup, address)
		switch {
		case health["home"] == home:
			_ = exec.CommandContext(cleanup, bundled, "assistant", "stop", "--listen", address).Run()
		case health["executable"] == legacy && health["version"] == "v9":
			_ = exec.CommandContext(cleanup, legacySource, "assistant", "stop", "--listen", address).Run()
		}
	})
	legacyStart := exec.CommandContext(ctx, legacy, "assistant", "start", "--listen", address)
	legacyStart.Env = append(os.Environ(), "LAZYMIND_RUNTIME_RESOURCES_ROOT="+legacyResources)
	if out, err := legacyStart.CombinedOutput(); err != nil {
		t.Fatalf("start genuine upstream v9: %v\n%s", err, out)
	}
	before, err := assistantbridge.Health(ctx, address)
	if err != nil || before["version"] != "v9" || before["service"] != nil || before["home"] != nil || before["isolated"] != nil {
		t.Fatalf("expected unscoped upstream v9, got %v: %v", before, err)
	}
	if _, err := connectorconfig.ReadBridgeEndpoint(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old Bridge unexpectedly left an endpoint record: %v", err)
	}

	if legacy == bundled {
		// The previous macOS bundle can be replaced at the original path.
		if err := os.Remove(legacy); err != nil {
			t.Fatal(err)
		}
	}
	copyExe(bundledSource, bundled)

	readStatus := func(data []byte) agentintegration.Status {
		t.Helper()
		var status agentintegration.Status
		if err := json.Unmarshal(data, &status); err != nil || status.State != agentintegration.Enabled || status.Connection == nil {
			t.Fatalf("desktop connector returned %s: %v", data, err)
		}
		return status
	}
	result := filepath.Join(root, "first-click-result.json")
	renderer := exec.CommandContext(ctx, "node", "node_modules/vitest/vitest.mjs", "run", "--config", "vitest.desktop-dsh.config.ts")
	renderer.Dir = "../../../../frontend"
	renderer.Env = append(os.Environ(),
		"LAZYMIND_TEST_DESKTOP_DSH_CONNECTOR_EXE="+bundled,
		"LAZYMIND_TEST_DESKTOP_DSH_ADDRESS="+address,
		"LAZYMIND_TEST_DESKTOP_DSH_RESULT="+result,
	)
	if out, err := renderer.CombinedOutput(); err != nil {
		t.Logf("Desktop renderer:\n%s", out)
		for _, name := range []string{"agent-setup.log", "assistant-bridge.log"} {
			if data, readErr := os.ReadFile(filepath.Join(home, "logs", name)); readErr == nil {
				if len(data) > 8192 {
					data = data[len(data)-8192:]
				}
				t.Logf("%s:\n%s", name, data)
			}
		}
		t.Fatalf("enable the real Desktop DSH switch: %v", err)
	}
	connected, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	status := readStatus(connected)
	if !status.Connection.MCPConfigured || status.Connection.RuntimeState != "waiting" {
		t.Fatalf("first click did not install the DSH connector: %+v", status.Connection)
	}
	after, err := assistantbridge.Health(ctx, address)
	if err != nil || after["version"] != "v12" || after["pid"] == before["pid"] ||
		after["service"] != "lazymind-assistant-bridge" || after["home"] != home {
		t.Fatalf("expected bundled Bridge after the first click, got %v: %v", after, err)
	}
	executable, _ := after["executable"].(string)
	if !strings.HasPrefix(executable, filepath.Join(home, "bridge", "bin")+string(filepath.Separator)) {
		t.Fatalf("replacement did not run its bundled staged executable: %v", after)
	}

	pkg := filepath.Join(profile, "node_modules", filepath.FromSlash(dshFixturePackage))
	const loadEntries = `
import { randomUUID } from 'node:crypto'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { readFile } from 'node:fs/promises'
const [, dir, expected] = process.argv
const { connectorBuild } = await import(pathToFileURL(join(dir, 'lib', 'index.js')))
let browser
new Function('window', await readFile(join(dir, 'lib', 'client.js'), 'utf8'))(
  { __ModuleLoader__: { load: ({ factory }) => { browser = factory() } } })
if (connectorBuild !== expected || browser.connectorBuild !== expected) throw new Error('unexpected DSH package')
console.log(JSON.stringify({
  build: connectorBuild, client_build: browser.connectorBuild, process_instance: randomUUID(),
  process_started_at: Math.round(Date.now() - process.uptime() * 1000),
}))
`
	loaded, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", loadEntries, pkg, release.Version).CombinedOutput()
	if err != nil {
		t.Fatalf("load installed DSH connector's Node and browser entries: %v\n%s", err, loaded)
	}
	var runtimeLoad workflowhost.DSHLoad
	if err := json.Unmarshal(loaded, &runtimeLoad); err != nil {
		t.Fatal(err)
	}
	config, err := connectorconfig.Load(connectorconfig.Path(desktopHome, "deepseek-harness", profile))
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := workflowhost.Load(desktopHome, strings.TrimSuffix(filepath.Base(config.PairingFile), ".json"), account)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/v1/workflow-host/actions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+pair.Token)
	request.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
	request.Header.Set("X-LazyMind-MCP-Ready", "1")
	request.Header.Set("X-LazyMind-DSH-Build", runtimeLoad.Build)
	request.Header.Set("X-LazyMind-DSH-Client-Build", runtimeLoad.ClientBuild)
	request.Header.Set("X-LazyMind-DSH-Process", runtimeLoad.Instance)
	request.Header.Set("X-LazyMind-DSH-Process-Started-At", strconv.FormatInt(runtimeLoad.StartedAt, 10))
	response, err := core.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replacement Bridge could not relay connector actions: %s", response.Status)
	}
	statusCmd := exec.CommandContext(ctx, bundled, "internal", "agent", "deepseek-harness", "status")
	output, err := statusCmd.Output()
	if err != nil {
		t.Fatalf("read connected status: %v", err)
	}
	status = readStatus(output)
	if status.Connection.RuntimeState != "connected" || !status.Connection.ControlReady {
		t.Fatalf("installed DSH connector could not use the replacement Bridge: %+v", status.Connection)
	}
}

const desktopDSHInstaller = "desktop-dsh-install-fixture"

// The test binary stands in for DSH's bundled runtime only. The LazyMind
// Connector, the old Bridge and the new Bridge are built from their CLI sources.
func runDesktopDSHFixture(name string, args []string) error {
	if name == "DeepSeek Harness" && len(args) == 5 && args[0] == "--expose-internals" {
		_, err := fmt.Fprintln(os.Stdout, `{"version":"0.2.0-rc.2","supported":true,"profiles":[{"name":"desktop","mcpVersion":"0.2.0-rc.2","mcpPath":"/bundled/mcp"},{"name":"web","mcpVersion":"0.2.0-rc.2","mcpPath":"/bundled/mcp"}]}`)
		return err
	}
	if name == desktopDSHInstaller && len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(os.Stdout, "0.2.0-rc.2")
		return err
	}
	if name != desktopDSHInstaller || len(args) != 5 ||
		args[0] != "plugin" || args[1] != "--profile" || args[3] != "add" || !filepath.IsAbs(args[4]) ||
		(args[2] != "desktop" && args[2] != "web") {
		return fmt.Errorf("unexpected packaged DSH fixture invocation")
	}
	profile := filepath.Join(os.Getenv("DSH_HOME"), "profiles", args[2])
	target := filepath.Join(profile, "node_modules", filepath.FromSlash(dshFixturePackage))
	if err := connectortest.UnpackDSHArchive(args[4], target); err != nil {
		return err
	}
	// DSH's installer records the dependency in the target profile manifest.
	path := filepath.Join(profile, "package.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		return err
	}
	manifest["dependencies"] = map[string]string{dshFixturePackage: "file:" + args[4]}
	body, err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}
