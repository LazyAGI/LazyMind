package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestRuntimeUpdateScopeIncludesResolvedExternalStores(t *testing.T) {
	f := newUpdateStateFixture(t)
	external := filepath.Join(t.TempDir(), "outside.db")
	f.cfg.UpdateStorage.Databases["auth"] = external
	scope, err := runtimeUpdateScope(f.cfg, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	contains := func(want string) bool {
		for _, got := range scope.DataRoots {
			if got == want {
				return true
			}
		}
		return false
	}
	if !contains(f.paths.RuntimeRoot) || !contains(external) || scope.Installation != f.paths.ResourcesRoot {
		t.Fatalf("incomplete guarded scope %+v", scope)
	}
	local := f.cfg
	local.Profile = "local"
	scope, err = runtimeUpdateScope(local, f.paths)
	if err != nil || scope.Installation != "" {
		t.Fatalf("local source checkout treated as installed Desktop: %+v %v", scope, err)
	}
}

func TestRuntimeEntrypointsRejectPendingUpdateBeforeMutation(t *testing.T) {
	f := newUpdateStateFixture(t)
	root := t.TempDir()
	saved := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = saved })
	writePendingAdmissionJournal(t, root, admissionPlan(t, f, root))
	before, err := os.ReadFile(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]string{{"startup-guard"}, {"up"}, {"down"}, {"warmup", "--maintenance", installerWarmupMaintenanceMode}, {"reset", "--scope", "all"}, {"service", "--name", "file-watcher", "--action", "start"}, {"internal", "core-run"}, {"internal", "algorithm-run", "--service", "chat"}, {"guard", "--owner-pid", "1"}, {"install-rag"}}
	for _, base := range cases {
		t.Run(strings.Join(base, "-"), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			runner := &fakeRunner{t: t, handlers: []func(Command) (CommandResult, error){func(Command) (CommandResult, error) {
				t.Fatal("guarded command executed a child")
				return CommandResult{}, nil
			}}}
			cli := NewCLI(io.Discard, io.Discard, runner, "unit-manager")
			// A missing guard must fail the test without probing live services.
			// The fixture contains no executables, and every command is trapped.
			cli.newManager = func(r CommandRunner, exe string) *RuntimeManager {
				m := NewRuntimeManager(r, exe)
				absent := func(int, time.Duration) bool { return false }
				m.probeAPI = absent
				m.probeLocalProxy = absent
				m.probeFrontend = absent
				m.probeAuth = absent
				m.probeChannelGateway = absent
				m.probeCore = absent
				m.probeScan = absent
				m.probeFileWatch = absent
				m.probeSQLiteServer = absent
				m.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
				m.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return false }
				return m
			}
			profile := "desktop"
			if base[0] == "install-rag" {
				profile = "local"
			}
			args := append(append([]string{}, base...), "--profile", profile, "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", f.cfg.OwnerToken)
			if err := cli.Run(ctx, args); desktopupdate.ErrorCode(err) != "update_in_progress" {
				t.Fatalf("entrypoint not guarded: %v", err)
			}
			runner.assertCommandCount(0)
			after, err := os.ReadFile(f.paths.StateFile)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("blocked entry changed runtime state")
			}
		})
	}
}
