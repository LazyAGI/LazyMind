package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateBootstrapMalformedIntentCannotBeIgnoredByStartupGuard(t *testing.T) {
	f := newUpdateStateFixture(t)
	root := t.TempDir()
	original := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = original })
	var output bytes.Buffer
	runner := &fakeRunner{t: t}
	cli := NewCLI(&output, &output, runner, "fixture-manager")
	args := []string{"startup-guard", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot,
		"--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot,
		"--owner-token", f.cfg.OwnerToken}
	if err := cli.Run(context.Background(), args); err != nil {
		t.Fatalf("no-record startup control is not valid: %v", err)
	}
	intent := filepath.Join(root, "native-v2", "active.json")
	if err := os.MkdirAll(filepath.Dir(intent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intent, []byte("{\"version\":2,"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := cli.Run(context.Background(), args); err == nil {
		t.Fatalf("startup ignored a corrupt active native transaction: %s", output.String())
	}
	if len(runner.calls) != 0 {
		t.Fatal("startup validation launched services")
	}
}

func TestUpdateBootstrapDirectUpChecksIntentBeforeProbingServices(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		name := "no-record-control"
		if corrupt {
			name = "corrupt-v2-record"
		}
		t.Run(name, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			root := t.TempDir()
			original := resolveRuntimeRecoveryRoot
			resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
			t.Cleanup(func() { resolveRuntimeRecoveryRoot = original })
			if corrupt {
				filename := filepath.Join(root, "native-v2", "active.json")
				if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filename, []byte("{broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			runner := &fakeRunner{t: t}
			cli := NewCLI(&output, &output, runner, "fixture-manager")
			probed := false
			cli.newManager = func(r CommandRunner, exe string) *RuntimeManager {
				manager := NewRuntimeManager(r, exe)
				manager.probeAPI = func(int, time.Duration) bool { probed = true; return true }
				manager.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return true }
				manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
				return manager
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := cli.Run(ctx, []string{"up", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot,
				"--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot,
				"--owner-token", f.cfg.OwnerToken})
			if corrupt {
				if err == nil || probed || len(runner.calls) != 0 {
					t.Fatalf("direct up crossed unverified intent: err=%v probed=%v calls=%d", err, probed, len(runner.calls))
				}
			} else if !probed {
				t.Fatalf("control did not reach the real Up entry: %v %s", err, output.String())
			}
		})
	}
}
