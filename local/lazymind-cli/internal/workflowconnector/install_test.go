package workflowconnector_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowconnector/connectortest"
)

func TestProvisionSelectsReleaseAndPreservesLocalChangesAndPairing(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "bin", "lazymind")
	home := t.TempDir()
	for _, host := range []string{"codex", "workbuddy"} {
		connectortest.WriteMinimalPackage(t, executable, host)
	}
	pairing := filepath.Join(home, "existing-pairing.json")
	originalPairing := []byte(`{"enabled":true,"connector_id":"existing"}`)
	if err := os.WriteFile(pairing, originalPairing, 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
		t.Fatal(err)
	}
	first, err := workflowconnector.Resolve(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first.ControlPath(), []byte("export const fixed = true"), 0600); err != nil {
		t.Fatal(err)
	}
	edited, err := first.Revision()
	if err != nil || edited == first.Release {
		t.Fatalf("editable runtime revision: %q %v", edited, err)
	}
	if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
		t.Fatal(err)
	}
	if reinstalled, err := workflowconnector.Resolve(home, "codex"); err != nil || reinstalled.EntryPath() != first.EntryPath() {
		t.Fatalf("same-release install changed the entry path: %+v %v", reinstalled, err)
	}
	if revision, err := first.Revision(); err != nil || revision != edited {
		t.Fatalf("same-release install overwrote the repair: %q %v", revision, err)
	}
	files := map[string]string{"main.mjs": "import './adapter.mjs'", "adapter.mjs": "", "presentation.mjs": ""}
	next := connectortest.WritePackage(t, executable, "codex", "0.2.0", files)
	if err := workflowconnector.Provision(t.Context(), home, executable, true); err != nil {
		t.Fatal(err)
	}
	installed, err := workflowconnector.Resolve(home, "codex")
	if err != nil || installed.Release != next.Release || installed.EntryPath() == first.EntryPath() {
		t.Fatalf("new active Connector: %+v %v", installed, err)
	}
	if revision, err := first.Revision(); err != nil || revision != edited {
		t.Fatalf("upgrade modified previous release: %q %v", revision, err)
	}
	if body, err := os.ReadFile(pairing); err != nil || !bytes.Equal(body, originalPairing) {
		t.Fatalf("pairing changed during upgrade: %s %v", body, err)
	}
	if err := os.Remove(installed.ControlPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowconnector.Resolve(home, "codex"); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), installed.ControlPath()) {
		t.Fatalf("missing module path: %v", err)
	}
	if _, err := workflowconnector.RequestReload(home, "codex"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("requested reload with incomplete package: %v", err)
	}
	if _, err := workflowconnector.Resolve(home, "workbuddy"); err != nil {
		t.Fatalf("other host has no active package: %v", err)
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
	realHome := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realHome, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := workflowconnector.Ensure(t.Context(), alias, executable, "codex"); err != nil {
		t.Fatal(err)
	}
	installed, err := workflowconnector.Resolve(alias, "codex")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(installed.EntryPath())
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := workflowconnector.Runtime{
		ConnectorEntry: canonical, ConnectorVersion: installed.Version, ConnectorRevision: installed.Release,
	}
	if !installed.Matches(heartbeat) {
		t.Fatal("canonical heartbeat was rejected")
	}
	heartbeat.ConnectorEntry = installed.ControlPath()
	if installed.Matches(heartbeat) {
		t.Fatal("heartbeat reported a different file")
	}
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
