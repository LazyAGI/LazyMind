//go:build !windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestDSHStandaloneCLIReconnectsWithBundledRelease(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to exercise both DSH entry points")
	}
	root := t.TempDir()
	home, dshHome := filepath.Join(root, "LazyMind home"), filepath.Join(root, "DSH home")
	profile := filepath.Join(dshHome, "profiles", "web")
	bin := filepath.Join(root, "bin")
	self, dsh := filepath.Join(bin, "lazymind"), filepath.Join(bin, "dsh")
	const plugin = "@lazymind/dsh-workflow"
	write := func(path, data string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(profile, "package.json"), `{"dsh":{"profile":{"bundles":["@lazymind/dsh-workflow"]}},"dependencies":{"@lazymind/dsh-workflow":"file:old.tgz"}}`, 0600)
	write(filepath.Join(profile, "node_modules", "@deepseek-ai", "dsh-session", "package.json"), `{"version":"0.2.0"}`, 0600)
	write(dsh, `#!/bin/sh
set -eu
test "$1 $2 $3 $4 $5" = "plugin --profile web add --workspace-root"
shift 6
test "$#" -eq 1
target="$DSH_HOME/profiles/web/node_modules/@lazymind/dsh-workflow"
mkdir -p "$target"
tar -xzf "$1" -C "$target" --strip-components 1
printf '%s\n' "$1" >> "$DSH_HOME/native-installations"
`, 0700)
	write(filepath.Join(bin, "pnpm"), "#!/bin/sh\nexit 0\n", 0700)
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
			t.Fatalf("%v: %s", args, body)
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
		loaded, err := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script, filepath.Join(profile, "node_modules", plugin), build).CombinedOutput()
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
	v1 := connectortest.WriteDSHPackage(t, self, "0.1.0+0000000000000001")
	if err := workflowconnector.Provision(t.Context(), home, self, true); err != nil {
		t.Fatal(err)
	}
	pair := confirm(call("connect"), v1.Version)
	v2 := connectortest.WriteDSHPackage(t, self, "0.2.0+0000000000000002")
	if after := confirm(call("connect"), v2.Version); after != pair {
		t.Fatalf("reconnect changed pairing: %s", pair.ConnectorID)
	}
	if status := call("connect"); !status.Connection.ControlReady {
		t.Fatalf("same-release reconnect lost verified readiness: %+v", status.Connection)
	}
	body, err := os.ReadFile(filepath.Join(dshHome, "native-installations"))
	if err != nil {
		t.Fatal(err)
	}
	invocations := strings.FieldsFunc(string(body), func(r rune) bool { return r == '\n' })
	if len(invocations) != 2 || filepath.Base(filepath.Dir(invocations[0])) != v1.Release ||
		filepath.Base(filepath.Dir(invocations[1])) != v2.Release {
		t.Fatalf("native installation calls should be v1, v2: %q", invocations)
	}
}
