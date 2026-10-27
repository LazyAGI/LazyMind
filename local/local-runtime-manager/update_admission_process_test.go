package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

type admissionChildInput struct {
	Root string
	Args []string
}

func TestRuntimeAdmissionEntryChild(t *testing.T) {
	mode := os.Getenv("LAZYMIND_UNIT_ADMISSION_CHILD")
	if mode == "" {
		return
	}
	var request admissionChildInput
	if json.Unmarshal([]byte(os.Getenv("LAZYMIND_UNIT_ADMISSION_INPUT")), &request) != nil {
		os.Exit(2)
	}
	resolveRuntimeRecoveryRoot = func() (string, error) { return request.Root, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	runner := &fakeRunner{t: t, handlers: []func(Command) (CommandResult, error){func(Command) (CommandResult, error) { os.Exit(3); return CommandResult{}, nil }}}
	output := io.Writer(os.Stdout)
	if mode == "writer" {
		output = io.Discard
	}
	cli := NewCLI(output, io.Discard, runner, "synthetic-unit-manager")
	if mode == "writer" {
		cli.newManager = func(r CommandRunner, exe string) *RuntimeManager {
			m := NewRuntimeManager(r, exe)
			var entered sync.Once
			m.probeAPI = func(int, time.Duration) bool {
				entered.Do(func() {
					actor, err := desktopupdate.CurrentProcessIdentity()
					if err != nil {
						_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error": desktopupdate.ErrorCode(err)})
						os.Exit(4)
					}
					_ = json.NewEncoder(os.Stdout).Encode(struct {
						Status string                        `json:"status"`
						Actor  desktopupdate.ProcessIdentity `json:"actor"`
					}{"entered", actor})
					_, _ = bufio.NewReader(os.Stdin).ReadByte()
				})
				return true
			}
			m.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return true }
			m.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { os.Exit(5); return nil, nil }
			return m
		}
	}
	if err := cli.Run(ctx, request.Args); err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error": desktopupdate.ErrorCode(err)})
		os.Exit(6)
	}
	if len(runner.calls) != 0 {
		os.Exit(7)
	}
	os.Exit(0)
}

func admissionPlan(t *testing.T, f *updateStateFixture, root string) desktopupdate.TransactionPlan {
	t.Helper()
	scope, err := runtimeUpdateScope(f.cfg, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	return desktopupdate.TransactionPlan{TrustDomain: "fixture", Audience: "fixture-personal", Channel: "personal-test", Mode: "unsigned-test", Platform: "darwin", Arch: "arm64", SigningIdentity: "adhoc", UpdaterProtocol: 1, Scope: scope, SourceID: "source", TargetID: "target", SourceVersion: "1.0.0", TargetVersion: "1.1.0", SourceManifestSHA256: strings.Repeat("a", 64), SourcePackageSHA256: strings.Repeat("b", 64), TargetPackageSHA256: strings.Repeat("c", 64), PolicySequence: 1, PolicyDigest: strings.Repeat("d", 64), DataProfile: "fixture", TargetDataProfile: "fixture", Units: []desktopupdate.SnapshotUnit{{Path: f.paths.DataDir}}, Reservations: []desktopupdate.InstallSpaceReservation{{Directory: root, InstallBytes: 1024, RecoveryBytes: 1024}}, OwnerToken: f.cfg.OwnerToken}
}

func admissionCommand(t *testing.T, f *updateStateFixture, root, mode string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "startup-guard"
	if mode == "writer" {
		command = "up"
	}
	args := []string{command, "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", f.cfg.OwnerToken}
	raw, _ := json.Marshal(admissionChildInput{Root: root, Args: args})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestRuntimeAdmissionEntryChild$")
	cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_ADMISSION_CHILD="+mode, "LAZYMIND_UNIT_ADMISSION_INPUT="+string(raw))
	return cmd
}

func TestRuntimeStartupGuardRegistersItsLivingCoordinatorAfterReturning(t *testing.T) {
	f := newUpdateStateFixture(t)
	root := t.TempDir()
	plan := admissionPlan(t, f, root)
	cmd := admissionCommand(t, f, root, "coordinator")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("guard entry: %s %v", output, err)
	}
	var result struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(output, &result) != nil || result.Status != "allowed" {
		t.Fatalf("guard did not authorize: %s", output)
	}
	foreign := plan
	foreign.OwnerToken = "another-owner"
	if _, err = desktopupdate.BeginUpdateTransaction(context.Background(), root, foreign); desktopupdate.ErrorCode(err) != "runtime_busy" {
		t.Fatalf("short-lived guard lost its living parent: %v", err)
	}
	tx, err := desktopupdate.BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	self, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	coordinators := tx.Journal().SourceCoordinators
	if len(coordinators) != 1 || coordinators[0] != self {
		t.Fatalf("guard registered wrong process, expected its parent: %+v / %+v", coordinators, self)
	}
	if err = tx.AbortBeforeInstall(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeNormalUpEntryKeepsWriterRegistrationUntilExit(t *testing.T) {
	f := newUpdateStateFixture(t)
	root := t.TempDir()
	plan := admissionPlan(t, f, root)
	cmd := admissionCommand(t, f, root, "writer")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill() })
	line, err := bufio.NewReader(out).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status string                        `json:"status"`
		Actor  desktopupdate.ProcessIdentity `json:"actor"`
	}
	if json.Unmarshal(line, &result) != nil || result.Status != "entered" {
		t.Fatalf("normal entry failed: %s", line)
	}
	if result.Actor.PID != cmd.Process.Pid || result.Actor.StartID == 0 {
		t.Fatal("not the actual long-lived CLI process")
	}
	foreign := plan
	foreign.OwnerToken = "another-owner"
	if _, err = desktopupdate.BeginUpdateTransaction(context.Background(), root, foreign); desktopupdate.ErrorCode(err) != "runtime_busy" {
		t.Fatalf("normal up entry omitted registration: %v", err)
	}
	tx, err := desktopupdate.BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if processes := tx.Journal().SourceProcesses; len(processes) != 1 || processes[0] != result.Actor {
		t.Fatalf("writer identity not captured: %+v", processes)
	}
	if err = tx.BeginDrain(); err != nil {
		t.Fatal(err)
	}
	if err = tx.ConfirmStopped(context.Background()); desktopupdate.ErrorCode(err) != "processes_running" {
		t.Fatalf("live CLI was declared stopped: %v", err)
	}
	_, _ = in.Write([]byte("done\n"))
	_ = in.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = tx.ConfirmStopped(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = tx.AbortBeforeInstall(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAndHelperUseSharedRecoveryBase(t *testing.T) {
	base, err := desktopupdate.RecoveryBase()
	if err != nil {
		t.Fatal(err)
	}
	root, err := defaultRuntimeRecoveryRoot()
	if err != nil || root != filepath.Join(base, "LazyMindUpdater") {
		t.Fatalf("runtime selected another recovery root: %q %v", root, err)
	}
	source, err := os.ReadFile("cmd/update-helper/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("desktopupdate.RecoveryBase")) {
		t.Fatal("helper still resolves a separate service HOME root")
	}
}
