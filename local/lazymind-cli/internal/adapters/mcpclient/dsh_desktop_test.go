package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
)

type dshTestProbe struct{}

func (dshTestProbe) Probe(context.Context) (mcpbridge.ProbeResult, error) {
	return mcpbridge.ProbeResult{}, nil
}

const desktopManifest = `{"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-web-app"]}}}`

func desktopAdapterFixture(t *testing.T) *Adapter {
	t.Helper()
	root := t.TempDir()
	setTestHome(t, root)
	t.Setenv("DSH_HOME", filepath.Join(root, "中文 DSH"))
	t.Setenv("LAZYMIND_DSH_PROFILE", "../ignored-in-desktop-mode")
	app := filepath.Join(root, "桌面 DSH.app")
	resources := filepath.Join(app, "Contents", "Resources")
	launcher, script := "dsh", "#!/bin/sh\necho 0.2.0-rc.2\n"
	if runtime.GOOS == "windows" {
		resources = filepath.Join(root, "桌面 DSH", "resources")
		launcher, script = "dsh.cmd", "@echo off\r\necho 0.2.0-rc.2\r\n"
	}
	cli := filepath.Join(resources, "runtime", "cli", "bin", launcher)
	writeTestFile(t, cli, script)
	if err := os.Chmod(cli, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(resources, "app.asar"), "fixture")
	t.Setenv("LAZYMIND_DSH_DESKTOP_PATH", cli)
	writeTestFile(t, filepath.Join(dshHome(), "profiles", "desktop", "package.json"), desktopManifest)
	a := testAdapter(DeepSeekHarness)
	selfName, selfScript := "lazymind", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		selfName, selfScript = "lazymind.cmd", "@echo off\r\nexit /b 0\r\n"
	}
	a.self = filepath.Join(root, selfName)
	writeTestFile(t, a.self, selfScript)
	if err := os.Chmod(a.self, 0700); err != nil {
		t.Fatal(err)
	}
	// Ownership checks resolve both executable paths, so the fixture must be
	// discoverable by the current platform, not merely exist on disk.
	if !agentexec.SameExecutable(a.self, a.self) {
		t.Fatal("LazyMind fixture is not a resolvable executable")
	}
	a.home = filepath.Join(root, "LazyMind home")
	a.bridge = dshTestProbe{}
	a.dshInspector = func(context.Context, string) (dshRuntimeInspection, error) {
		return dshRuntimeInspection{Version: "0.2.0-rc.2", Supported: true, Profiles: []dshRuntimeProfile{
			{Name: "desktop", MCPVersion: "0.2.0-rc.2", MCPPath: "/bundled/mcp"},
			{Name: "web", MCPVersion: "0.2.0-rc.2", MCPPath: "/bundled/mcp"},
		}}, nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/core/workflow-control/capabilities" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_ready":true,"protocol":"workflow.control.v1"}`))
	}))
	t.Cleanup(server.Close)
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: server.URL, Username: "fixture", AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600, SavedAt: float64(time.Now().Unix())}); err != nil {
		t.Fatal(err)
	}
	return a
}

func installDSHFixture(t *testing.T, name string) {
	t.Helper()
	profile := filepath.Join(dshHome(), "profiles", name)
	writeTestFile(t, filepath.Join(profile, "package.json"), `{"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-web-app","@lazymind/dsh-workflow"]}}}`)
	for _, pkg := range []string{dshWorkflowPackage} {
		writeTestFile(t, filepath.Join(profile, "node_modules", pkg, "package.json"), `{"version":"0.1.1"}`)
	}
}

func TestDesktopStatusIsReadOnlyAndRequiresValidInitialization(t *testing.T) {
	a := desktopAdapterFixture(t)
	if s := a.Status(context.Background()); s.State != agentintegration.Ready || !s.CanConnect {
		t.Fatalf("desktop-only status: %+v", s)
	}
	if pathExists(filepath.Join(dshHome(), "profiles", "web")) || pathExists(a.dshRecordPath()) {
		t.Fatal("status wrote installation state")
	}
	manifest := filepath.Join(dshHome(), "profiles", "desktop", "package.json")
	for _, body := range []string{"", `{}`, `{broken`} {
		writeTestFile(t, manifest, body)
		if s := a.Status(context.Background()); s.State != agentintegration.Failed || s.CanConnect || s.Issue == nil || s.Issue.Code != "dependency_environment" {
			t.Fatalf("bad manifest %q: %+v", body, s)
		}
	}
}

func TestDesktopPartialInstallRetryAndDisconnect(t *testing.T) {
	a := desktopAdapterFixture(t)
	untouched := filepath.Join(dshHome(), "profiles", "headless", "cordis.patch.yml")
	writeTestFile(t, untouched, "# headless\n[]\n")
	webPatch := filepath.Join(dshHome(), "profiles", "web", "cordis.patch.yml")
	const original = "# 用户配置\n- id: unrelated\n  disabled: false\n"
	writeTestFile(t, webPatch, original)
	failWeb := true
	calls := map[string]int{}
	a.dshRunner = func(_ context.Context, name string, args ...string) error {
		calls[name]++
		if len(args) != 2 || args[0] != "add" || !strings.HasSuffix(args[1], ".tgz") {
			t.Fatalf("expected only the local Workflow archive, got %v", args)
		}
		if name == "web" && failWeb {
			return errors.New("simulated package failure")
		}
		installDSHFixture(t, name)
		return nil
	}
	s := a.Connect(context.Background())
	if s.State != agentintegration.Failed || !s.CanConnect || !s.CanDisconnect || s.Profiles[0].State != agentintegration.Enabled {
		t.Fatalf("partial: %+v", s)
	}
	if body, _ := os.ReadFile(webPatch); string(body) != original {
		t.Fatalf("failed target not restored: %s", body)
	}
	desktopPatch := a.forDSHProfile("desktop", "").dshConfigPath()
	first, _ := os.ReadFile(desktopPatch)
	restarted := *a
	if s := restarted.Status(context.Background()); s.State != agentintegration.Failed {
		t.Fatalf("lost partial result after restart: %+v", s)
	}
	failWeb = false
	if s := restarted.Connect(context.Background()); s.State != agentintegration.Enabled {
		t.Fatalf("retry: %+v", s)
	}
	if calls["desktop"] != 1 || calls["web"] != 2 {
		t.Fatalf("retry reinstalled successful target: %v", calls)
	}
	if body, _ := os.ReadFile(desktopPatch); string(body) != string(first) {
		t.Fatal("retry changed successful target")
	}
	if s := restarted.Connect(context.Background()); s.State != agentintegration.Enabled || calls["desktop"] != 1 || calls["web"] != 2 {
		t.Fatal("repeated enable was not idempotent")
	}
	desktopDoc, _ := readYAMLDocument(desktopPatch)
	webDoc, _ := readYAMLDocument(webPatch)
	pairFiles := []string{}
	for _, doc := range []*yaml.Node{desktopDoc, webDoc} {
		for _, row := range yamlSequence(doc).Content {
			if scalarValue(row, "id") == dshWorkflowRow {
				connection, err := connectorconfig.Load(scalarValue(mappingValue(row, "config"), "connectorConfig"))
				if err != nil {
					t.Fatal(err)
				}
				pairFiles = append(pairFiles, connection.PairingFile)
			}
		}
	}
	if len(pairFiles) != 2 || pairFiles[0] == pairFiles[1] {
		t.Fatalf("profiles share pairing: %v", pairFiles)
	}
	if s := restarted.Disconnect(context.Background()); s.State != agentintegration.Ready || s.CanDisconnect {
		t.Fatalf("disconnect: %+v", s)
	}
	for _, file := range pairFiles {
		body, _ := os.ReadFile(file)
		if !strings.Contains(string(body), `"enabled":false`) {
			t.Fatal("pairing still enabled")
		}
	}
	for _, name := range dshDesktopProfiles {
		target := a.forDSHProfile(name, "")
		doc, _ := readYAMLDocument(target.dshConfigPath())
		if findDSHEntry(doc) != nil || target.dshWorkflowConfigured() {
			t.Fatal("connection remains enabled")
		}
		if _, err := dshModuleVersion(filepath.Dir(target.dshConfigPath()), dshWorkflowPackage); err != nil {
			t.Fatal("disconnect removed package")
		}
	}
	if body, _ := os.ReadFile(webPatch); !strings.Contains(string(body), "unrelated") {
		t.Fatal("removed unrelated config")
	}
	if body, _ := os.ReadFile(untouched); string(body) != "# headless\n[]\n" {
		t.Fatal("modified headless")
	}
	if pathExists(filepath.Join(dshHome(), "cordis.patch.yml")) {
		t.Fatal("wrote global patch")
	}
}

func TestDesktopConflictsPreventAllInstallation(t *testing.T) {
	for _, where := range []string{"global", "web"} {
		t.Run(where, func(t *testing.T) {
			a := desktopAdapterFixture(t)
			calls := 0
			a.dshRunner = func(context.Context, string, ...string) error { calls++; return nil }
			path := filepath.Join(dshHome(), "cordis.patch.yml")
			if where == "web" {
				path = a.forDSHProfile("web", "").dshConfigPath()
			}
			const manual = "- insert:\n    - id: custom\n      config:\n        serverName: lazymind\n    - id: another\n"
			writeTestFile(t, path, manual)
			if s := a.Connect(context.Background()); s.State != agentintegration.Conflict || s.Issue == nil || s.Issue.Code != "operation_failed" {
				t.Fatalf("conflict: %+v", s)
			}
			if calls != 0 {
				t.Fatal("installed despite preflight conflict")
			}
			if body, _ := os.ReadFile(path); string(body) != manual {
				t.Fatal("changed manual entry")
			}
		})
	}
}

func TestDesktopConfigFailureRestoresPairingAndAllowsRetry(t *testing.T) {
	a := desktopAdapterFixture(t)
	path := a.forDSHProfile("web", "").dshConfigPath()
	const original = "- id: user-setting\n  disabled: false\n"
	writeTestFile(t, path, original)
	a.dshRunner = func(_ context.Context, name string, _ ...string) error {
		installDSHFixture(t, name)
		if name == "web" {
			writeTestFile(t, connectorconfig.Path(a.home, string(DeepSeekHarness), filepath.Dir(path)), "{broken")
		}
		return nil
	}
	if s := a.Connect(context.Background()); s.State != agentintegration.Failed {
		t.Fatalf("configuration failure: %+v", s)
	}
	if body, _ := os.ReadFile(path); string(body) != original {
		t.Fatal("patch not restored")
	}
	files, _ := filepath.Glob(filepath.Join(a.home, "workflow-hosts", "*.json"))
	if len(files) != 1 {
		t.Fatalf("failed profile left a pairing: %v", files)
	}
	if err := os.Remove(connectorconfig.Path(a.home, string(DeepSeekHarness), filepath.Dir(path))); err != nil {
		t.Fatal(err)
	}
	a.dshRunner = func(_ context.Context, name string, _ ...string) error { installDSHFixture(t, name); return nil }
	if s := a.Connect(context.Background()); s.State != agentintegration.Enabled {
		t.Fatalf("repair: %+v", s)
	}
}

func TestDesktopDisconnectOccupiedProfileCanBeRetried(t *testing.T) {
	a := desktopAdapterFixture(t)
	a.dshRunner = func(_ context.Context, name string, _ ...string) error { installDSHFixture(t, name); return nil }
	if s := a.Connect(context.Background()); s.State != agentintegration.Enabled {
		t.Fatalf("connect: %+v", s)
	}
	web := a.forDSHProfile("web", "")
	before, _ := os.ReadFile(web.dshConfigPath())
	writeTestFile(t, web.dshConfigPath()+".lock", "occupied")
	s := a.Disconnect(context.Background())
	if s.State != agentintegration.Failed || !s.CanDisconnect || s.RetryAction != "disconnect" || s.Issue == nil || s.Issue.Code != "operation_failed" {
		t.Fatalf("partial disconnect: %+v", s)
	}
	restarted := *a
	if s := restarted.Status(context.Background()); s.RetryAction != "disconnect" || !s.CanDisconnect {
		t.Fatalf("lost disconnect intent after refresh: %+v", s)
	}
	if a.forDSHProfile("desktop", "").dshWorkflowConfigured() {
		t.Fatal("successful disconnect was not retained")
	}
	if body, _ := os.ReadFile(web.dshConfigPath()); string(body) != string(before) {
		t.Fatal("modified occupied profile")
	}
	if err := os.Remove(web.dshConfigPath() + ".lock"); err != nil {
		t.Fatal(err)
	}
	if s := restarted.Disconnect(context.Background()); s.State != agentintegration.Ready || s.CanDisconnect || s.RetryAction != "" {
		t.Fatalf("retry disconnect: %+v", s)
	}
}

func TestDesktopDisconnectRecoversExitedPatchWriter(t *testing.T) {
	a := desktopAdapterFixture(t)
	a.dshRunner = func(_ context.Context, name string, _ ...string) error { installDSHFixture(t, name); return nil }
	if s := a.Connect(context.Background()); s.State != agentintegration.Enabled {
		t.Fatalf("connect: %+v", s)
	}
	web := a.forDSHProfile("web", "")
	writeTestFile(t, web.dshConfigPath()+".lock", exitedDSHTestProcess(t))
	if s := a.Disconnect(context.Background()); s.State != agentintegration.Ready || s.CanDisconnect {
		t.Fatalf("disconnect after writer exit: %+v", s)
	}
	for _, name := range dshDesktopProfiles {
		target := a.forDSHProfile(name, "")
		doc, err := readYAMLDocument(target.dshConfigPath())
		if err != nil || findDSHEntry(doc) != nil || pathExists(target.dshConfigPath()+".lock") {
			t.Fatalf("profile %s was not cleaned: %v", name, err)
		}
	}
}

func TestDesktopDisconnectInspectsRuntimeOnceAndReadsFreshConfig(t *testing.T) {
	a := desktopAdapterFixture(t)
	a.dshRunner = func(_ context.Context, name string, _ ...string) error { installDSHFixture(t, name); return nil }
	if s := a.Connect(context.Background()); s.State != agentintegration.Enabled {
		t.Fatalf("connect: %+v", s)
	}
	inspect := a.dshInspector
	calls := 0
	a.dshInspector = func(ctx context.Context, cli string) (dshRuntimeInspection, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("runtime inspection lost the operation deadline")
		}
		if calls > 1 {
			return dshRuntimeInspection{}, errors.New("a second inspection would exceed the caller's budget")
		}
		return inspect(ctx, cli)
	}
	s := a.Disconnect(context.Background())
	if calls != 1 || s.State != agentintegration.Ready || s.CanDisconnect {
		t.Fatalf("disconnect: inspections=%d, status=%+v", calls, s)
	}
	for _, profile := range s.Profiles {
		if profile.State != agentintegration.Ready {
			t.Fatalf("stale enabled profile after disconnect: %+v", profile)
		}
	}
}

func TestDesktopStatusAndDisconnectRespectCancellation(t *testing.T) {
	for _, action := range []string{"status", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			a := desktopAdapterFixture(t)
			a.dshRunner = func(_ context.Context, name string, _ ...string) error { installDSHFixture(t, name); return nil }
			if s := a.Connect(context.Background()); s.State != agentintegration.Enabled {
				t.Fatalf("connect: %+v", s)
			}
			patch := a.forDSHProfile("desktop", "").dshConfigPath()
			before, _ := os.ReadFile(patch)
			ctx, cancel := context.WithCancel(context.Background())
			a.dshInspector = func(probeCtx context.Context, _ string) (dshRuntimeInspection, error) {
				cancel()
				select {
				case <-probeCtx.Done():
					return dshRuntimeInspection{}, probeCtx.Err()
				case <-time.After(time.Second):
					t.Fatal("runtime inspection ignored caller cancellation")
					return dshRuntimeInspection{}, nil
				}
			}
			defer cancel()
			if action == "status" {
				a.Status(ctx)
			} else {
				a.Disconnect(ctx)
			}
			if after, _ := os.ReadFile(patch); string(after) != string(before) {
				t.Fatal("cancelled operation changed the managed config")
			}
		})
	}
}

func TestDesktopPairingFailureDoesNotUndoOtherProfile(t *testing.T) {
	a := desktopAdapterFixture(t)
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Dir(a.forDSHProfile("web", "").dshConfigPath())
	sum := sha256.Sum256([]byte(account + "\x00" + profile))
	lockPath := filepath.Join(a.home, "workflow-hosts", "dsh-"+hex.EncodeToString(sum[:16])+".json.lock")
	a.dshRunner = func(_ context.Context, name string, _ ...string) error {
		installDSHFixture(t, name)
		if name == "web" {
			// A directory at the lock-file path fails pairing on every platform,
			// unlike POSIX permission bits, which Windows does not enforce.
			if err := os.MkdirAll(lockPath, 0700); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	s := a.Connect(context.Background())
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if s.State != agentintegration.Failed || s.Profiles[0].State != agentintegration.Enabled {
		t.Fatalf("pairing failure: %+v", s)
	}
	doc, err := readYAMLDocument(a.forDSHProfile("web", "").dshConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if findDSHEntry(doc) != nil {
		t.Fatal("published MCP before pairing succeeded")
	}
	a.dshRunner = func(_ context.Context, name string, _ ...string) error { installDSHFixture(t, name); return nil }
	if s := a.Connect(context.Background()); s.State != agentintegration.Enabled {
		t.Fatalf("pairing retry: %+v", s)
	}
}

// Opt-in: install only the local Workflow archive with the official Desktop runtime.
// The profile, credentials, Core fixture, and archive are all temporary.
func TestInstalledDSHDesktopCLISmoke(t *testing.T) {
	cli := os.Getenv("DSH_DESKTOP_TEST_CLI")
	if cli == "" {
		t.Skip("set DSH_DESKTOP_TEST_CLI to opt into installed-runtime smoke test")
	}
	cli, err := agentexec.ResolveDSHDesktopCLI(cli)
	if err != nil {
		t.Fatal(err)
	}
	a := desktopAdapterFixture(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("LAZYMIND_DSH_DESKTOP_PATH", cli)
	a.dshInspector = nil
	writeTestFile(t, filepath.Join(dshHome(), "profiles", "desktop", "pnpm-workspace.yaml"), "packages:\n  - .\nnodeLinker: hoisted\nautoInstallPeers: false\n")
	before, err := inspectDSHRuntime(context.Background(), cli)
	if err != nil || dshInspectionIssue(before, err, cli) != nil {
		t.Fatalf("runtime inspection: %+v %v", before, err)
	}
	if pathExists(filepath.Join(dshHome(), "profiles", "web")) {
		t.Fatal("fixture unexpectedly contains Web")
	}
	for _, name := range dshDesktopProfiles {
		if err := a.forDSHProfile(name, cli).changeDSHProfile(context.Background(), true); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if s := a.Status(context.Background()); s.State != agentintegration.Enabled {
		t.Fatalf("installed runtime result: %+v", s)
	}
	// Reproduce an old store in a disposable profile after a valid installation.
	modulesPath := filepath.Join(dshHome(), "profiles", "web", "node_modules", ".modules.yaml")
	original, err := os.ReadFile(modulesPath)
	if err != nil {
		t.Fatal(err)
	}
	var modules map[string]any
	if err := yaml.Unmarshal(original, &modules); err != nil {
		t.Fatal(err)
	}
	modules["storeDir"] = filepath.Join(t.TempDir(), "old-store", "v10")
	changed, _ := yaml.Marshal(modules)
	writeTestFile(t, modulesPath, string(changed))
	s := a.Status(context.Background())
	if s.Issue == nil || s.Issue.Code != "dependency_environment" || s.Issue.Path != filepath.Dir(filepath.Dir(modulesPath)) || s.CanConnect {
		t.Fatalf("old store was not blocked: %+v", s)
	}
	if body, _ := os.ReadFile(modulesPath); string(body) != string(changed) {
		t.Fatal("inspection repaired dependencies")
	}
	writeTestFile(t, modulesPath, string(original))
	if s := a.Disconnect(context.Background()); s.State != agentintegration.Ready {
		t.Fatalf("cleanup: %+v", s)
	}
}

func TestDesktopPreflightBlocksBothTargetsWithoutWrites(t *testing.T) {
	for _, scenario := range []string{"old-version", "unreadable-cli", "desktop-environment", "web-environment", "incomplete-probe"} {
		t.Run(scenario, func(t *testing.T) {
			a := desktopAdapterFixture(t)
			good := a.dshInspector
			a.dshInspector = func(ctx context.Context, cli string) (dshRuntimeInspection, error) {
				result, _ := good(ctx, cli)
				switch scenario {
				case "old-version":
					result.Version = "0.2.0-rc.1"
					result.Supported = false
				case "unreadable-cli":
					return result, errors.New("cannot execute runtime")
				case "desktop-environment":
					result.Profiles[0].Diagnostic = "missing MCP"
				case "web-environment":
					result.Profiles[1].Diagnostic = "ERR_PNPM_UNEXPECTED_STORE"
				case "incomplete-probe":
					result.Profiles = nil
				}
				return result, nil
			}
			calls := 0
			a.dshRunner = func(context.Context, string, ...string) error { calls++; return nil }
			s := a.Connect(context.Background())
			code := "dependency_environment"
			if scenario == "old-version" {
				code = "unsupported_version"
			}
			if s.State != agentintegration.Failed || s.CanConnect || s.Issue == nil || s.Issue.Code != code {
				t.Fatalf("preflight: %+v", s)
			}
			if calls != 0 || pathExists(a.dshRecordPath()) || pathExists(filepath.Join(a.home, "workflow-hosts")) || pathExists(filepath.Join(dshHome(), "profiles", "web")) {
				t.Fatal("blocked preflight wrote state or installed packages")
			}
		})
	}
}

func TestDesktopCleanupRemainsAvailableAfterUnsupportedVersion(t *testing.T) {
	a := desktopAdapterFixture(t)
	a.dshRunner = func(_ context.Context, name string, _ ...string) error { installDSHFixture(t, name); return nil }
	if s := a.Connect(context.Background()); s.State != agentintegration.Enabled {
		t.Fatalf("connect: %+v", s)
	}
	a.dshInspector = func(context.Context, string) (dshRuntimeInspection, error) {
		return dshRuntimeInspection{Version: "0.2.0-rc.1"}, nil
	}
	s := a.Disconnect(context.Background())
	if s.CanDisconnect {
		t.Fatalf("cleanup blocked: %+v", s)
	}
	for _, name := range dshDesktopProfiles {
		doc, _ := readYAMLDocument(a.forDSHProfile(name, "").dshConfigPath())
		if findDSHEntry(doc) != nil {
			t.Fatal("managed entry remains")
		}
	}
}

func TestDesktopHistoricalDiagnosticsAreNotUserMessages(t *testing.T) {
	a := desktopAdapterFixture(t)
	record := `{"root":` + strconv.Quote(dshHome()) + `,"profiles":{"web":"ERR_PNPM_UNEXPECTED_STORE very long raw diagnostic"}}`
	writeTestFile(t, a.dshRecordPath(), record)
	s := a.Status(context.Background())
	if s.Issue == nil || s.Issue.Code != "dependency_environment" || s.Issue.Path != filepath.Join(dshHome(), "profiles", "web") {
		t.Fatalf("classification: %+v", s)
	}
	if strings.Contains(s.Message, "ERR_PNPM") || strings.Contains(s.Profiles[1].Message, "raw diagnostic") {
		t.Fatal("raw diagnostic exposed")
	}
}

func TestInstalledDSHInspectionIsReadOnly(t *testing.T) {
	cli := os.Getenv("DSH_DESKTOP_TEST_CLI")
	if cli == "" {
		t.Skip("requires installed Desktop")
	}
	a := desktopAdapterFixture(t)
	a.dshInspector = nil
	t.Setenv("LAZYMIND_DSH_DESKTOP_PATH", cli)
	snapshot := func() string {
		var entries []string
		err := filepath.Walk(dshHome(), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				entries = append(entries, path+":"+string(body))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(entries, "\n")
	}
	before := snapshot()
	result, err := inspectDSHRuntime(context.Background(), cli)
	if issue := dshInspectionIssue(result, err, cli); issue != nil {
		t.Fatalf("inspection: %+v %v", result, err)
	}
	if before != snapshot() || pathExists(filepath.Join(dshHome(), "profiles", "web")) {
		t.Fatal("inspection initialized a profile or changed files")
	}
	web := filepath.Join(dshHome(), "profiles", "web")
	writeTestFile(t, filepath.Join(web, "package.json"), `{"dependencies":{"@deepseek-ai/dsh-mcp-client":"0.1.6-alpha.1"},"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-web-app"]}}}`)
	writeTestFile(t, filepath.Join(web, "node_modules/@deepseek-ai/dsh-mcp-client/package.json"), `{"name":"@deepseek-ai/dsh-mcp-client","version":"0.1.6-alpha.1","main":"index.js"}`)
	before = snapshot()
	result, err = inspectDSHRuntime(context.Background(), cli)
	issue := dshInspectionIssue(result, err, cli)
	if issue == nil || issue.Code != "dependency_environment" || issue.Path != web {
		t.Fatalf("old local MCP was ignored: %+v %v", result, err)
	}
	if result.Profiles[1].MCPVersion != "0.1.6-alpha.1" {
		t.Fatalf("wrong MCP resolution: %+v", result)
	}
	if before != snapshot() {
		t.Fatal("inspection changed old environment")
	}
}
