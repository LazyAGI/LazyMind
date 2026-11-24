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
