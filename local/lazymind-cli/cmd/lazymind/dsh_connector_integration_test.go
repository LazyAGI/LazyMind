package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowconnector/connectortest"
	"lazymind/agentconnector/internal/workflowhost"
)

const dshNativeInstallFixture = "dsh-native-install-fixture"
const dshFixturePackage = "@lazymind/dsh-workflow"

func runDSHNativeInstallFixture(args []string) error {
	want := []string{"plugin", "--profile", "web", "add", "--workspace-root", "@deepseek-ai/dsh-mcp-client@0.2.0"}
	if len(args) != len(want)+1 || !slices.Equal(args[:len(want)], want) || !filepath.IsAbs(args[len(want)]) {
		return fmt.Errorf("unexpected DSH installer arguments %q, want %q followed by an absolute archive path", args, want)
	}
	archive := args[len(want)]
	home := os.Getenv("DSH_HOME")
	if home == "" {
		return errors.New("DSH installer did not receive DSH_HOME")
	}
	target := filepath.Join(home, "profiles", "web", "node_modules", filepath.FromSlash(dshFixturePackage))
	if err := connectortest.UnpackDSHArchive(archive, target); err != nil {
		return err
	}
	calls, err := os.OpenFile(filepath.Join(home, "native-installations"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(calls, archive)
	return errors.Join(err, calls.Close())
}

func TestDSHStandaloneCLIReconnectsWithSelectedRelease(t *testing.T) {
	testDSHStandaloneCLIReconnect(t, false)
}

func TestDSHStandaloneCLIRecoversRetainedReleaseWithoutReinstalling(t *testing.T) {
	testDSHStandaloneCLIReconnect(t, true)
}

func testDSHStandaloneCLIReconnect(t *testing.T, recoverLegacy bool) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI requires Node to exercise both DSH entry points: %v", err)
		}
		t.Skipf("Node is required to exercise both DSH entry points: %v", err)
	}
	root := t.TempDir()
	home, dshHome := filepath.Join(root, "LazyMind home"), filepath.Join(root, "DSH home")
	profile := filepath.Join(dshHome, "profiles", "web")
	bin := filepath.Join(root, "CLI bin")
	self := filepath.Join(bin, "lazymind")
	fixture := filepath.Join(bin, dshNativeInstallFixture)
	pnpm := filepath.Join(bin, "pnpm")
	pnpmBody := "#!/bin/sh\nexit 1\n"
	if runtime.GOOS == "windows" {
		self += ".exe"
		fixture += ".exe"
		pnpm += ".cmd"
		pnpmBody = "@exit /b 1\r\n"
	}
	write := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(profile, "package.json"), []byte(`{"dsh":{"profile":{"bundles":["@lazymind/dsh-workflow"]}},"dependencies":{"@lazymind/dsh-workflow":"file:old.tgz"}}`), 0600)
	write(filepath.Join(profile, "node_modules", "@deepseek-ai", "dsh-session", "package.json"), []byte(`{"version":"0.2.0"}`), 0600)
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.ReadFile(testExecutable)
	if err != nil {
		t.Fatal(err)
	}
	write(fixture, helper, 0700)
	dsh := fixture
	if runtime.GOOS == "windows" {
		// Exercise the same batch invocation path as the Windows DSH launcher.
		dsh = filepath.Join(bin, "dsh.cmd")
		write(dsh, []byte("@echo off\r\n\"%~dp0"+dshNativeInstallFixture+".exe\" %*\r\nexit /b %errorlevel%\r\n"), 0700)
	}
	// DSH's package manager is discovered by name. The fixture installs the
	// archive itself, so an unexpected invocation of pnpm should fail.
	write(pnpm, []byte(pnpmBody), 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LAZYMIND_HOME", home)
	t.Setenv("LAZYMIND_HOST_HOME", root)
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_DSH_PATH", dsh)
	t.Setenv("LAZYMIND_DSH_DESKTOP_PATH", filepath.Join(root, "no-desktop"))
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("LAZYMIND_DSH_PROFILE", "web")
	t.Setenv(connectorconfig.EnvironmentKey, "")
	compiled := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", self, ".")
	if output, err := compiled.CombinedOutput(); err != nil {
		t.Fatalf("build the CLI once: %s %v", output, err)
	}
	digest := func() [32]byte {
		t.Helper()
		body, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(body)
	}
	original := digest()
	t.Logf("LazyMind CLI SHA-256 %x", original)

	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	for _, tool := range strings.Fields("cloud_document.get cloud_document.list cloud_document.search knowledge.document.get knowledge.document.list knowledge.list knowledge.search model.chat model.list skill.get skill.list tool.call tool.list") {
		server.AddTool(&mcp.Tool{Name: tool, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	}
	routes := http.NewServeMux()
	routes.Handle("/api/core/mcp/capabilities/v1", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	routes.HandleFunc("/api/core/workflow-control/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"schema_ready":true,"protocol":"workflow.control.v1"}`)
	})
	core := httptest.NewServer(routes)
	defer core.Close()
	store, err := credentials.NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: core.URL, Username: "fixture", AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600, SavedAt: float64(time.Now().Unix())}); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) agentintegration.Status {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), self, append([]string{"internal", "agent", "deepseek-harness"}, args...)...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		body, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v: %s %v", args, stderr.String(), err)
		}
		var status agentintegration.Status
		if err := json.Unmarshal(body, &status); err != nil {
			t.Fatalf("%v: %s %v", args, body, err)
		}
		if status.State != agentintegration.Enabled || status.Connection == nil {
			t.Fatalf("%v: %s\n%s", args, body, stderr.String())
		}
		if digest() != original {
			t.Fatalf("CLI changed during %v", args)
		}
		return status
	}
	confirm := func(status agentintegration.Status, build string) workflowhost.Pairing {
		t.Helper()
		if status.Connection.RuntimeState != "waiting" || status.Connection.ControlReady {
			t.Fatalf("expected %s installed and awaiting restart: %+v", build, status.Connection)
		}
		const script = `
import { randomUUID } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
const [, dir, expected] = process.argv
const { connectorBuild } = await import(pathToFileURL(join(dir, 'lib', 'index.js')))
const source = await readFile(join(dir, 'lib', 'client.js'), 'utf8')
let browser
new Function('window', source)({ __ModuleLoader__: { load: ({ factory }) => { browser = factory(() => ({})) } } })
if (connectorBuild !== expected || browser?.connectorBuild !== expected) throw new Error('incorrect DSH entry point build')
console.log(JSON.stringify({ build: connectorBuild, client_build: browser.connectorBuild, process_instance: randomUUID(), process_started_at: Math.round(Date.now() - process.uptime()*1000) }))
`
		loaded, err := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script, filepath.Join(profile, "node_modules", filepath.FromSlash(dshFixturePackage)), build).CombinedOutput()
		if err != nil {
			t.Fatalf("load Node and browser entries: %s %v", loaded, err)
		}
		var runtime workflowhost.DSHLoad
		if err := json.Unmarshal(loaded, &runtime); err != nil {
			t.Fatal(err)
		}
		config, err := connectorconfig.Load(connectorconfig.Path(home, "deepseek-harness", profile))
		if err != nil {
			t.Fatal(err)
		}
		account, err := store.AccountScope()
		if err != nil {
			t.Fatal(err)
		}
		pair, err := workflowhost.Load(home, strings.TrimSuffix(filepath.Base(config.PairingFile), ".json"), account)
		if err != nil {
			t.Fatal(err)
		}
		if err := workflowhost.RecordRuntimeStatus(home, pair, "", true, runtime); err != nil {
			t.Fatal(err)
		}
		status = call("status")
		if status.Connection.RuntimeState != "connected" || !status.Connection.ControlReady || status.Connection.PresentationStatus != "available" {
			t.Fatalf("fresh process did not confirm %s: %+v", build, status.Connection)
		}
		t.Logf("installed and loaded Node and client build %s", build)
		return pair
	}
	requireInstalls := func(stage string, releases ...workflowconnector.Manifest) {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(dshHome, "native-installations"))
		if err != nil {
			t.Fatal(err)
		}
		invocations := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
		if len(invocations) != len(releases) {
			t.Fatalf("%s: got %d native installations, want %d: %q", stage, len(invocations), len(releases), invocations)
		}
		for i, release := range releases {
			want := filepath.Join(home, "dsh-workflow", "connectors", release.Release, release.Entry)
			if invocations[i] != want {
				t.Fatalf("%s: native installation %d used %q, want %q", stage, i+1, invocations[i], want)
			}
		}
	}
	checkSelection := func(stage string, expected workflowconnector.Manifest) {
		t.Helper()
		active, err := workflowconnector.Resolve(home, workflowconnector.DSHHost)
		if err != nil || active.Release != expected.Release || active.Version != expected.Version {
			t.Errorf("%s: active DSH release is %+v, want build %s (%s): %v", stage, active, expected.Version, expected.Release, err)
		}
	}

	v1 := connectortest.WriteDSHPackage(t, self, "0.1.0+0000000000000001")
	if _, err := workflowconnector.Read(home, workflowconnector.DSHHost); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first connect requires a home without a selected DSH release: %v", err)
	}
	pair := confirm(call("connect"), v1.Version)
	requireInstalls("first connect", v1)
	checkSelection("first connect", v1)
	pairPath := filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")
	originalPairing, err := os.ReadFile(pairPath)
	if err != nil {
		t.Fatal(err)
	}
	requirePairing := func(stage string) {
		t.Helper()
		current, err := os.ReadFile(pairPath)
		if err != nil || !bytes.Equal(current, originalPairing) {
			t.Fatalf("%s: existing pairing changed: %v", stage, err)
		}
	}

	v2 := connectortest.WriteDSHPackage(t, self, "0.2.0+0000000000000002")
	stage := "ordinary reconnect with v2 supplied"
	if recoverLegacy {
		// Older CLIs retained the native archive without recording a home selection.
		if err := os.Remove(workflowconnector.RecordPath(home, workflowconnector.DSHHost)); err != nil {
			t.Fatal(err)
		}
		stage = "legacy reconnect with v2 supplied"
	}
	status := call("connect")
	requireInstalls(stage, v1)
	checkSelection(stage, v1)
	if status.Connection.RuntimeState != "connected" || !status.Connection.ControlReady {
		t.Fatalf("%s lost v1 readiness: %+v", stage, status.Connection)
	}
	requirePairing(stage)
	if recoverLegacy {
		return
	}

	if err := workflowconnector.Provision(t.Context(), home, self, true); err != nil {
		t.Fatalf("explicit deployment did not accept v2: %v", err)
	}
	checkSelection("explicit deployment", v2)
	requireInstalls("explicit deployment", v1)

	if after := confirm(call("connect"), v2.Version); after != pair {
		t.Fatalf("reconnect after deployment changed pairing: %s", pair.ConnectorID)
	}
	requireInstalls("reconnect after explicit deployment", v1, v2)
	requirePairing("reconnect after explicit deployment")

	if status := call("connect"); status.Connection.RuntimeState != "connected" || !status.Connection.ControlReady {
		t.Fatalf("same-release reconnect lost verified readiness: %+v", status.Connection)
	}
	requireInstalls("final reconnect", v1, v2)
	requirePairing("final reconnect")
}
