package workflowconnector_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowconnector/connectortest"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestProvisionPreservesSameReleaseCodexRepair(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "lazymind")
	home := t.TempDir()
	connectortest.WriteMinimalPackage(t, executable, "codex")
	connectortest.WriteMinimalPackage(t, executable, "workbuddy")
	if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
		t.Fatal(err)
	}
	first, err := workflowconnector.Resolve(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	repair := []byte("export const fixed = true")
	if err := os.WriteFile(first.ControlPath(), repair, 0600); err != nil {
		t.Fatal(err)
	}
	edited, err := first.Revision()
	if err != nil || edited == first.Release {
		t.Fatalf("editable runtime revision: %q %v", edited, err)
	}

	if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
		t.Fatal(err)
	}
	reinstalled, err := workflowconnector.Resolve(home, "codex")
	if err != nil || reinstalled.Release != first.Release || reinstalled.EntryPath() != first.EntryPath() {
		t.Fatalf("same-release install changed the selection: %+v %v", reinstalled, err)
	}
	if revision, err := reinstalled.Revision(); err != nil || revision != edited {
		t.Fatalf("same-release install overwrote the repair: %q %v", revision, err)
	}
	if body, err := os.ReadFile(reinstalled.ControlPath()); err != nil || !bytes.Equal(body, repair) {
		t.Fatalf("installed control module did not preserve the repair: %q %v", body, err)
	}

	next := connectortest.WritePackage(t, executable, "codex", "0.2.0", map[string]string{
		"main.mjs": "import './adapter.mjs'", "adapter.mjs": "export const bundled = true", "presentation.mjs": "",
	})
	if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
		t.Fatal(err)
	}
	upgraded, err := workflowconnector.Resolve(home, "codex")
	if err != nil || upgraded.Release != next.Release || upgraded.ControlPath() == first.ControlPath() {
		t.Fatalf("explicit upgrade did not select a new control module: %+v %v", upgraded, err)
	}
	if body, err := os.ReadFile(first.ControlPath()); err != nil || !bytes.Equal(body, repair) {
		t.Fatalf("explicit upgrade overwrote the old release's repair: %q %v", body, err)
	}
}

func TestProvisionSelectsReleaseOnlyOnUpgrade(t *testing.T) {
	hosts := []string{"codex", "workbuddy", workflowconnector.DSHHost}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "lazymind")
			home := t.TempDir()
			write := func(target, version, build string) workflowconnector.Manifest {
				t.Helper()
				if target == workflowconnector.DSHHost {
					return connectortest.WriteDSHPackage(t, executable, version+"+"+build)
				}
				return connectortest.WritePackage(t, executable, target, version, map[string]string{
					"main.mjs":    "import './adapter.mjs' // " + version,
					"adapter.mjs": "", "presentation.mjs": "",
				})
			}
			provider := host
			if host == workflowconnector.DSHHost {
				provider = workflowhost.DSHProvider
			}
			pair, err := workflowhost.EnsureForProvider(home, provider, filepath.Join(home, "host-profile"), "test-account")
			if err != nil {
				t.Fatal(err)
			}
			pairPath := filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")
			originalPairing, err := os.ReadFile(pairPath)
			if err != nil {
				t.Fatal(err)
			}
			var first workflowconnector.Manifest
			for _, target := range hosts {
				manifest := write(target, "0.1.0", "0000000000000001")
				if target == host {
					first = manifest
				}
			}
			if err := workflowconnector.Provision(t.Context(), home, executable, false); err != nil {
				t.Fatal(err)
			}
			selected, err := workflowconnector.Resolve(home, host)
			if err != nil || selected.Release != first.Release || selected.Version != first.Version {
				t.Fatalf("first provision did not select v1: %+v %v", selected, err)
			}
			oldPath := selected.EntryPath()
			oldEntry, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}

			next := write(host, "0.2.0", "0000000000000002")
			if err := workflowconnector.Provision(t.Context(), home, executable, false); err != nil {
				t.Fatal(err)
			}
			selected, err = workflowconnector.Resolve(home, host)
			if err != nil || selected.Release != first.Release || selected.Version != first.Version || selected.EntryPath() != oldPath {
				t.Fatalf("non-upgrade provision changed the selected v1 with v2 supplied: %+v %v", selected, err)
			}

			if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
				t.Fatal(err)
			}
			selected, err = workflowconnector.Resolve(home, host)
			if err != nil || selected.Release != next.Release || selected.Version != next.Version || selected.EntryPath() == oldPath {
				t.Fatalf("explicit upgrade did not select v2: %+v %v", selected, err)
			}
			if body, err := os.ReadFile(oldPath); err != nil || !bytes.Equal(body, oldEntry) {
				t.Fatalf("explicit upgrade changed the previous release: %v", err)
			}
			if body, err := os.ReadFile(pairPath); err != nil || !bytes.Equal(body, originalPairing) {
				t.Fatalf("provisioning changed the existing pairing: %v", err)
			}
		})
	}
}

func TestMissingCodexControlModuleRejectsResolveAndReload(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "lazymind")
	home := t.TempDir()
	connectortest.WriteMinimalPackage(t, executable, "codex")
	if err := workflowconnector.Ensure(t.Context(), home, executable, "codex"); err != nil {
		t.Fatal(err)
	}
	installed, err := workflowconnector.Resolve(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(installed.ControlPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowconnector.Resolve(home, "codex"); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), installed.ControlPath()) {
		t.Fatalf("resolve did not report the missing control module: %v", err)
	}
	if _, err := workflowconnector.RequestReload(home, "codex"); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), installed.ControlPath()) {
		t.Fatalf("reload did not report the missing control module: %v", err)
	}
}

func TestDamagedCodexControlModuleLeavesWorkBuddyResolvable(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "lazymind")
	home := t.TempDir()
	connectortest.WriteMinimalPackage(t, executable, "codex")
	workBuddy := connectortest.WritePackage(t, executable, "workbuddy", "0.1.0", map[string]string{
		"main.mjs": "import './adapter.mjs'", "adapter.mjs": "", "presentation.mjs": "",
	})
	if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
		t.Fatal(err)
	}
	codex, err := workflowconnector.Resolve(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(codex.ControlPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(codex.ControlPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowconnector.Resolve(home, "codex"); err == nil || !strings.Contains(err.Error(), codex.ControlPath()) {
		t.Fatalf("resolve accepted the damaged Codex control module: %v", err)
	}
	installed, err := workflowconnector.Resolve(home, "workbuddy")
	if err != nil || installed.Release != workBuddy.Release || installed.Version != workBuddy.Version {
		t.Fatalf("damaged Codex module affected the selected WorkBuddy release: %+v %v", installed, err)
	}
	if revision, err := installed.Revision(); err != nil || revision != workBuddy.Release {
		t.Fatalf("resolved WorkBuddy modules changed: %q %v", revision, err)
	}
}

func TestProvisionReportsInvalidHostRecordAndInstallsOtherHost(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "lazymind")
	for _, host := range []string{"codex", "workbuddy"} {
		connectortest.WriteMinimalPackage(t, executable, host)
	}
	home := t.TempDir()
	record := workflowconnector.RecordPath(home, "codex")
	if err := os.MkdirAll(filepath.Dir(record), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowconnector.Provision(t.Context(), home, executable, true); err == nil || !strings.Contains(err.Error(), record) {
		t.Fatalf("bad host record error: %v", err)
	}
	if body, err := os.ReadFile(record); err != nil || string(body) != "damaged" {
		t.Fatalf("bad host record overwritten: %s %v", body, err)
	}
	if _, err := workflowconnector.Resolve(home, "workbuddy"); err != nil {
		t.Fatalf("unaffected host did not provision: %v", err)
	}
}

func TestWorkerStopsUsingLoadedModulesAfterBrokenEdit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("requires Node")
	}
	home := t.TempDir()
	executable := filepath.Join(t.TempDir(), "lazymind")
	connectortest.WritePackage(t, executable, "codex", "0.1.0", map[string]string{
		"adapter.mjs":      "export const selected = 'original'",
		"presentation.mjs": "",
		"main.mjs": `import { existsSync, writeFileSync } from 'node:fs'
import { selected } from './adapter.mjs'
const [stop, status] = process.argv.slice(2)
writeFileSync(status, JSON.stringify({ selected, revision: process.env.LAZYMIND_WORKFLOW_CONNECTOR_REVISION }))
const timer = setInterval(() => {
  if (existsSync(stop)) {
    writeFileSync(status, 'cleaned ' + selected)
    clearInterval(timer)
  }
}, 10)
`,
	})
	if err := workflowconnector.Ensure(t.Context(), home, executable, "codex"); err != nil {
		t.Fatal(err)
	}
	installed, err := workflowconnector.Resolve(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(home, "worker.json")
	worker, err := installed.StartWorker(t.Context(), home, "reload-marker", func(ctx context.Context, stopFile string) *exec.Cmd {
		return exec.CommandContext(ctx, node, installed.EntryPath(), stopFile, status)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if worker != nil {
			worker.Stop()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, err := os.ReadFile(status)
		if err == nil && strings.Contains(string(body), installed.Release) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not load packaged modules: %s %v", body, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(installed.ControlPath(), []byte("invalid JavaScript!"), 0600); err != nil {
		t.Fatal(err)
	}
	graceful := worker.Stop()
	worker = nil
	if !graceful {
		t.Fatal("worker did not finish cleanup gracefully")
	}
	if body, err := os.ReadFile(status); err != nil || string(body) != "cleaned original" {
		t.Fatalf("worker did not clean up using loaded code: %s %v", body, err)
	}
}

func TestInstalledHeartbeatCanUseCanonicalPath(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "lazymind")
	connectortest.WriteMinimalPackage(t, executable, "codex")
	realHome := filepath.Join(t.TempDir(), "real home")
	if err := os.Mkdir(realHome, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "Connector alias")
	aliasKind := "directory symlink"
	if runtime.GOOS == "windows" {
		// Directory junctions do not require the Windows symlink privilege.
		aliasKind = "directory junction"
		cmd := exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", alias, realHome)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create Windows directory junction: %v (%s)", err, output)
		}
	} else if err := os.Symlink(realHome, alias); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(alias); err != nil {
			t.Errorf("remove Connector alias: %v", err)
		}
	})
	if err := workflowconnector.Ensure(t.Context(), alias, executable, "codex"); err != nil {
		t.Fatalf("install through %s %q: %v", aliasKind, alias, err)
	}
	installed, err := workflowconnector.Resolve(alias, "codex")
	if err != nil {
		t.Fatalf("resolve through %s %q: %v", aliasKind, alias, err)
	}
	direct, err := workflowconnector.Resolve(realHome, "codex")
	if err != nil {
		t.Fatalf("resolve junction or symlink target %q directly: %v", realHome, err)
	}
	if filepath.Clean(direct.EntryPath()) == filepath.Clean(installed.EntryPath()) {
		t.Fatalf("expected different entry paths through %s %q and its target %q, got %q",
			aliasKind, alias, realHome, installed.EntryPath())
	}
	heartbeat := workflowconnector.Runtime{
		ConnectorEntry: direct.EntryPath(), ConnectorVersion: installed.Version, ConnectorRevision: installed.Release,
	}
	if !installed.Matches(heartbeat) {
		t.Fatalf("%s entry %q rejected heartbeat reporting the same file via %q",
			aliasKind, installed.EntryPath(), heartbeat.ConnectorEntry)
	}
	heartbeat.ConnectorEntry = direct.ControlPath()
	if installed.Matches(heartbeat) {
		t.Fatalf("%s entry %q accepted heartbeat reporting different module %q",
			aliasKind, installed.EntryPath(), heartbeat.ConnectorEntry)
	}
	t.Logf("accepted the selected entry through a %s and rejected a different module", aliasKind)
}

func TestProvisionIsolatesDamagedConnectorPackages(t *testing.T) {
	for _, damaged := range []string{"codex", workflowconnector.DSHHost} {
		t.Run(damaged, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "bin", "lazymind")
			home := t.TempDir()
			write := func(host, version, build string) workflowconnector.Manifest {
				if host == workflowconnector.DSHHost {
					return connectortest.WriteDSHPackage(t, executable, version+"+"+build)
				}
				return connectortest.WritePackage(t, executable, host, version, map[string]string{
					"main.mjs": "// build " + build, "adapter.mjs": "", "presentation.mjs": "",
				})
			}
			first := write(damaged, "0.1.0", "0000000000000001")
			for _, host := range []string{"codex", "workbuddy", workflowconnector.DSHHost} {
				if host != damaged {
					write(host, "0.1.0", "0000000000000001")
				}
			}
			pkg, err := workflowconnector.ReadPackage(executable)
			if err != nil {
				t.Fatal(err)
			}
			staged := filepath.Join(t.TempDir(), "bin", "lazymind")
			if err := pkg.Stage(staged); err != nil {
				t.Fatal(err)
			}
			if err := workflowconnector.Provision(t.Context(), home, staged, false); err != nil {
				t.Fatal(err)
			}
			previous, err := workflowconnector.Resolve(home, damaged)
			if err != nil || previous.Release != first.Release {
				t.Fatalf("initial release: %+v %v", previous, err)
			}
			oldEntry, err := os.ReadFile(previous.EntryPath())
			if err != nil {
				t.Fatal(err)
			}
			second := write(damaged, "0.2.0", "0000000000000002")
			healthy := map[string]workflowconnector.Manifest{}
			for _, host := range []string{"codex", "workbuddy", workflowconnector.DSHHost} {
				if host != damaged {
					healthy[host] = write(host, "0.2.0", "0000000000000002")
				}
			}
			path := filepath.Join(filepath.Dir(executable), "workflow-connectors", damaged, second.Entry)
			if err := os.WriteFile(path, oldEntry, 0600); err != nil {
				t.Fatal(err)
			}
			diagnostic := "release digest mismatch"
			if damaged == workflowconnector.DSHHost {
				diagnostic = "invalid DSH Connector package metadata"
			}
			if err := workflowconnector.Provision(t.Context(), home, executable, true); err == nil || !strings.Contains(err.Error(), diagnostic) {
				t.Fatalf("bad package was not identified: %v", err)
			}
			for host, expected := range healthy {
				if unaffected, err := workflowconnector.Resolve(home, host); err != nil || unaffected.Release != expected.Release {
					t.Fatalf("bad package prevented %s from updating: %+v %v", host, unaffected, err)
				}
			}
			if selected, err := workflowconnector.Resolve(home, damaged); err != nil || selected.Release != first.Release {
				t.Fatalf("bad package displaced the good release: %+v %v", selected, err)
			}
			write(damaged, "0.2.0", "0000000000000002")
			if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
				t.Fatal(err)
			}
			if selected, err := workflowconnector.Resolve(home, damaged); err != nil || selected.Release != second.Release {
				t.Fatalf("repaired package did not update the release: %+v %v", selected, err)
			}
			if body, err := os.ReadFile(previous.EntryPath()); err != nil || !bytes.Equal(body, oldEntry) {
				t.Fatalf("updating the distribution changed the previous package: %v", err)
			}
		})
	}
}

func TestDSHConnectionPreservesDamagedSelection(t *testing.T) {
	for _, damaged := range []string{"record", "archive record", "archive", "missing archive"} {
		t.Run(damaged, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "lazymind")
			home := t.TempDir()
			connectortest.WriteDSHPackage(t, executable, "0.1.0+0000000000000001")
			if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
				t.Fatal(err)
			}
			selected, err := workflowconnector.Resolve(home, workflowconnector.DSHHost)
			if err != nil {
				t.Fatal(err)
			}
			record := workflowconnector.RecordPath(home, workflowconnector.DSHHost)
			damagedPath := selected.EntryPath()
			switch damaged {
			case "record":
				damagedPath = record
			case "archive record":
				damagedPath = filepath.Join(selected.Dir, "connector.json")
			}
			if damaged == "missing archive" {
				if err := os.Remove(damagedPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(damagedPath, []byte("damaged"), 0600); err != nil {
				t.Fatal(err)
			}
			oldRecord, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			connectortest.WriteDSHPackage(t, executable, "0.2.0+0000000000000002")

			if _, err := workflowconnector.DSHRelease(t.Context(), home, executable); err == nil || !strings.Contains(err.Error(), damagedPath) {
				t.Errorf("connect did not report the damaged selection at %s: %v", damagedPath, err)
			}
			if err := workflowconnector.Provision(t.Context(), home, executable, false); err == nil || !strings.Contains(err.Error(), damagedPath) {
				t.Errorf("non-upgrade provision did not report the damaged selection at %s: %v", damagedPath, err)
			}
			if body, err := os.ReadFile(record); err != nil || !bytes.Equal(body, oldRecord) {
				t.Errorf("the active record was overwritten: %s %v", body, err)
			}
			body, err := os.ReadFile(damagedPath)
			if damaged == "missing archive" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing archive was unexpectedly created: %v", err)
				}
			} else if err != nil || string(body) != "damaged" {
				t.Fatalf("damaged selection was overwritten: %s %v", body, err)
			}
		})
	}
}

func TestDSHRecoversSingleRetainedRelease(t *testing.T) {
	for _, caller := range []string{"non-upgrade provision", "unbundled standalone connect"} {
		t.Run(caller, func(t *testing.T) {
			t.Setenv("LAZYMIND_RUNTIME_RESOURCES_ROOT", "")
			executable := filepath.Join(t.TempDir(), "lazymind")
			home := t.TempDir()
			first := connectortest.WriteDSHPackage(t, executable, "0.1.0+0000000000000001")
			if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(workflowconnector.RecordPath(home, workflowconnector.DSHHost)); err != nil {
				t.Fatal(err)
			}

			if caller == "non-upgrade provision" {
				connectortest.WriteDSHPackage(t, executable, "0.2.0+0000000000000002")
				if err := workflowconnector.Provision(t.Context(), home, executable, false); err != nil {
					t.Fatal(err)
				}
			} else {
				unbundled := filepath.Join(t.TempDir(), "lazymind")
				if _, err := workflowconnector.DSHRelease(t.Context(), t.TempDir(), unbundled); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "DeepSeek Harness Connector installation record") {
					t.Fatalf("unbundled connect without any installation returned %v", err)
				}
				installed, err := workflowconnector.DSHRelease(t.Context(), home, unbundled)
				if err != nil || installed.Release != first.Release || installed.Version != first.Version {
					t.Fatalf("unbundled connect did not reuse retained v1: %+v %v", installed, err)
				}
			}
			selected, err := workflowconnector.Resolve(home, workflowconnector.DSHHost)
			if err != nil || selected.Release != first.Release || selected.Version != first.Version {
				t.Fatalf("v1 was not restored as the active release: %+v %v", selected, err)
			}
		})
	}
}

func TestDSHLegacySelectionRejectsAmbiguousRetainedReleases(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "lazymind")
	home := t.TempDir()
	for _, build := range []string{"0.1.0+0000000000000001", "0.2.0+0000000000000002"} {
		connectortest.WriteDSHPackage(t, executable, build)
		if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
			t.Fatal(err)
		}
	}
	record := workflowconnector.RecordPath(home, workflowconnector.DSHHost)
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	next := connectortest.WriteDSHPackage(t, executable, "0.3.0+0000000000000003")

	if _, err := workflowconnector.DSHRelease(t.Context(), home, executable); err == nil || !strings.Contains(err.Error(), "multiple retained") {
		t.Errorf("connect did not report an ambiguous selection: %v", err)
	}
	if err := workflowconnector.Provision(t.Context(), home, executable, false); err == nil || !strings.Contains(err.Error(), "multiple retained") {
		t.Errorf("non-upgrade provision did not report an ambiguous selection: %v", err)
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an active record was written without a clear selection: %v", err)
	}
	if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
		t.Fatal(err)
	}
	selected, err := workflowconnector.Resolve(home, workflowconnector.DSHHost)
	if err != nil || selected.Release != next.Release || selected.Version != next.Version {
		t.Fatalf("explicit upgrade did not resolve the selection: %+v %v", selected, err)
	}
}

func TestDSHReleaseRequiresBundledMember(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_RESOURCES_ROOT", "")
	home := t.TempDir()
	distribution := filepath.Join(t.TempDir(), "lazymind")
	good := connectortest.WriteDSHPackage(t, distribution, "0.1.0+0000000000000001")
	if err := workflowconnector.Provision(t.Context(), home, distribution, true); err != nil {
		t.Fatal(err)
	}
	standalone := filepath.Join(t.TempDir(), "lazymind")
	installed, err := workflowconnector.DSHRelease(t.Context(), home, standalone)
	if err != nil || installed.Release != good.Release {
		t.Fatalf("standalone did not reuse a provisioned package: %+v %v", installed, err)
	}
	standalonePackage, err := workflowconnector.ReadPackage(standalone)
	if err != nil || standalonePackage.Identity() != "" {
		t.Fatalf("standalone identified as a distribution: %v", err)
	}
	assertMissing := func(executable string) {
		t.Helper()
		if _, err := workflowconnector.DSHRelease(t.Context(), home, executable); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "DeepSeek Harness") {
			t.Fatalf("missing bundled package at %s: %v", executable, err)
		}
	}
	// A missing host in a distribution must not select the older home archive.
	if err := os.RemoveAll(filepath.Join(filepath.Dir(distribution), "workflow-connectors", "dsh")); err != nil {
		t.Fatal(err)
	}
	assertMissing(distribution)
	pkg, err := workflowconnector.ReadPackage(distribution)
	if err == nil || pkg.Identity() == standalonePackage.Identity() {
		t.Fatalf("invalid distribution lost its package identity: %v", err)
	}
	staged := filepath.Join(t.TempDir(), "lazymind")
	if err := pkg.Stage(staged); err != nil {
		t.Fatal(err)
	}
	assertMissing(staged)

	// Electron identifies a packaged runtime even if the whole sidecar is absent.
	runtime := t.TempDir()
	bin := filepath.Join(runtime, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_RUNTIME_RESOURCES_ROOT", runtime)
	assertMissing(filepath.Join(bin, "lazymind"))
}

func TestBuiltDSHConnectorPackage(t *testing.T) {
	directory := os.Getenv("LAZYMIND_TEST_DSH_CONNECTOR")
	if directory == "" {
		t.Skip("requires a staged DSH Connector package")
	}
	self := filepath.Join(filepath.Dir(filepath.Dir(directory)), "lazymind")
	home := t.TempDir()
	installed, err := workflowconnector.DSHRelease(t.Context(), home, self)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	connectortest.UnpackDSHFixture(t, installed.EntryPath(), dir)
	if err := installed.CheckDSHInstall(dir); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	script := `
import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
const [, dir, expected] = process.argv
const server = await import(pathToFileURL(join(dir, 'lib', 'index.js')))
const source = await readFile(join(dir, 'lib', 'client.js'), 'utf8')
let browser
new Function('window', source)({ __ModuleLoader__: { load: ({ factory }) => { browser = factory(() => ({})) } } })
if (server.connectorBuild !== expected || browser?.connectorBuild !== expected) {
  throw Error(JSON.stringify({ expected, server: server.connectorBuild, browser: browser?.connectorBuild }))
}
`
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script, dir, installed.Version)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load both compiled DSH entries: %s %v", output, err)
	}
	t.Logf("Node and browser loaded build %s from archive release %s", installed.Version, installed.Release)
}
