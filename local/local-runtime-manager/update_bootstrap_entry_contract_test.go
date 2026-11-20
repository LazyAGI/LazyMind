package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUpdateBootstrapActivatesAfterExpandingBundledPython(t *testing.T) {
	f := actualUpdateFormatFixture(t, 2)
	installBootstrapFixtureRAG(t, f)
	root := t.TempDir()
	previous := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = previous })
	record := bootstrapRecord(t, f, root, "installing", nil)
	tx := record["transactionId"].(string)
	python := filepath.Join(f.paths.ResourcesRoot, "deps", "python", "algorithm", "Scripts", "python.exe")
	writePythonPayloadArchive(t, f.paths.ResourcesRoot, map[string]string{
		"runtimes/python/cpython/python.exe":       "runtime fixture",
		"deps/python/algorithm/Scripts/python.exe": "unrelocated",
	})
	if _, err := os.Stat(python); !os.IsNotExist(err) {
		t.Fatalf("target interpreter was already expanded: %v", err)
	}
	// The Core subprocess stands in for actual component verification. It can
	// only reach success with the newly expanded interpreter and live Up lock.
	reply := `{"status":"activated","transactionId":` + strconv.Quote(tx) + `,"componentMode":"sync-target"}`
	stub := `package main
import "os"
func main() {
	if len(os.Args) != 3 || os.Args[1] != "--component-maintenance" || os.Args[2] != "activate" { os.Exit(2) }
	python, err := os.ReadFile(` + strconv.Quote(python) + `)
	_, lockErr := os.Stat(` + strconv.Quote(f.paths.UpLockFile) + `)
	if err != nil || lockErr != nil || string(python) != "relocated" {
		os.Stdout.WriteString("{\"status\":\"blocked\",\"reason\":\"component_target_verification_failed\"}")
		return
	}
	os.Stdout.WriteString(` + strconv.Quote(reply) + `)
}`
	filename := filepath.Join(t.TempDir(), "core.go")
	if err := os.WriteFile(filename, []byte(stub), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", f.paths.CoreBin, filename).CombinedOutput(); err != nil {
		t.Fatalf("build Core test process: %v %s", err, output)
	}
	before, err := os.ReadFile(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	runner := &fakeRunner{t: t}
	cli := NewCLI(&output, &output, runner, "fixture-manager")
	cli.newManager = func(r CommandRunner, exe string) *RuntimeManager {
		m := NewRuntimeManager(r, exe)
		m.relocatePythonVenvs = func(cfg RuntimeConfig, paths RuntimePaths) error {
			if cfg.Profile != "desktop" || !sameRuntimePath(paths.ResourcesRoot, f.paths.ResourcesRoot) {
				t.Fatal("relocation reached a different runtime")
			}
			if raw, err := os.ReadFile(python); err != nil || string(raw) != "unrelocated" {
				t.Fatalf("venv relocation ran before expansion: %q %v", raw, err)
			}
			return os.WriteFile(python, []byte("relocated"), 0600)
		}
		return m
	}
	args := []string{"update-bootstrap", "--action", "activate", "--transaction-id", tx,
		"--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot,
		"--runtime-root", f.paths.RuntimeRoot, "--owner-token", "fixture-target-owner"}
	if err := cli.Run(t.Context(), args); err != nil {
		t.Fatalf("activation command: %v %s", err, output.String())
	}
	var result struct{ Status string }
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &result); err != nil || result.Status != "activated" {
		t.Fatalf("activation did not reach Core with a usable target interpreter: %s (%v)", strings.TrimSpace(output.String()), err)
	}
	if after, err := os.ReadFile(f.paths.StateFile); err != nil || !bytes.Equal(before, after) || len(runner.calls) != 0 {
		t.Fatalf("activation started services or changed runtime state: %v", err)
	}
	if _, err := os.Stat(f.paths.UpLockFile); !os.IsNotExist(err) {
		t.Fatalf("activation left the startup lock held: %v", err)
	}
}

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
