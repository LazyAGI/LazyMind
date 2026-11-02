package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUpdateProcessIdentityChild(t *testing.T) {
	if os.Getenv("LAZYMIND_UNIT_IDENTITY_CHILD") != "1" {
		return
	}
	actor, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		os.Exit(2)
	}
	_ = json.NewEncoder(os.Stdout).Encode(actor)
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	os.Exit(0)
}

func updateIdentityChild(t *testing.T) desktopupdate.ProcessIdentity {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestUpdateProcessIdentityChild$")
	cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_IDENTITY_CHILD=1")
	configureChildProcess(cmd, false)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Wait() })
	var actor desktopupdate.ProcessIdentity
	if err := json.NewDecoder(out).Decode(&actor); err != nil {
		t.Fatal(err)
	}
	if actor.PID != cmd.Process.Pid || actor.StartID == 0 {
		t.Fatal("invalid child identity")
	}
	return actor
}

func TestUpdateProcessRegistryRetainsNativeIdentity(t *testing.T) {
	actor := updateIdentityChild(t)
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, RuntimeRoot: root, ProcessRegistryFile: filepath.Join(root, "processes.json")}
	registerLocalProcess(paths, coreProcessName, actor.PID, nil, []string{"unit-child"})
	registry, err := readLocalProcessRegistry(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Processes) != 1 || registry.Processes[0].StartID != actor.StartID {
		t.Fatalf("service registration lost its OS identity: %+v", registry.Processes)
	}
	if got := processStartIdentity(actor.PID); got != actor.StartID {
		t.Fatalf("unstable identity: %d", got)
	}
	if got := dedupeProcessRecords(registry.Processes, paths); len(got) != 1 {
		t.Fatal("live registered process omitted")
	}
	registry.Processes[0].StartID++
	if got := dedupeProcessRecords(registry.Processes, paths); len(got) != 0 {
		t.Fatal("reused PID accepted as registered process")
	}
}

func TestUpdateProcessDiscoveryDoesNotReplaceStaleIdentityWithPIDFile(t *testing.T) {
	actor := updateIdentityChild(t)
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, RuntimeRoot: root, ProcessRegistryFile: filepath.Join(root, "processes.json"), CorePIDFile: filepath.Join(root, "core.pid")}
	if err := os.WriteFile(paths.CorePIDFile, []byte(fmt.Sprint(actor.PID)), 0600); err != nil {
		t.Fatal(err)
	}
	registry := localProcessRegistry{Version: 1, RepoRoot: root, Processes: []LocalProcessRecord{{Service: coreProcessName, PID: actor.PID, StartID: actor.StartID + 1, RepoRoot: root, RuntimeRoot: root}}}
	if err := writeLocalProcessRegistry(paths, registry); err != nil {
		t.Fatal(err)
	}
	records, err := discoverLocalRuntimeProcessesChecked(paths, RuntimeConfig{}, func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatal("PID-only file overrode known stale service identity")
	}
}

func TestUpdateProcessNativeScanRetainsIdentityWithoutRegistry(t *testing.T) {
	// The Windows scanner excludes descendants of a configured Desktop owner;
	// this isolated child has no product owner or service registry.
	t.Setenv(desktopOwnerPIDEnvVar, "")
	actor := updateIdentityChild(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, RuntimeRoot: root, BuildRoot: filepath.Dir(exe)}
	records, err := scanLocalRuntimeProcesses(paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.PID == actor.PID {
			if record.StartID != actor.StartID {
				t.Fatalf("native scan lost OS identity: got %d, want %d", record.StartID, actor.StartID)
			}
			return
		}
	}
	t.Fatal("native scan did not find isolated test child")
}
