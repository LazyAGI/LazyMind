package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
	return desktopupdate.TransactionPlan{TrustDomain: "fixture", Audience: "fixture-personal", Channel: "personal-test", Mode: "unsigned-test", Platform: "darwin", Arch: "arm64", SigningIdentity: "adhoc", UpdaterProtocol: 1, Scope: scope, SourceID: "source", TargetID: "target", SourceVersion: "1.0.0", TargetVersion: "1.1.0", SourceManifestSHA256: strings.Repeat("a", 64), SourcePackageSHA256: strings.Repeat("b", 64), TargetPackageSHA256: strings.Repeat("c", 64), PolicySequence: 1, PolicyDigest: strings.Repeat("d", 64), DataProfile: "fixture", TargetDataProfile: "fixture", Units: []desktopupdate.SnapshotUnit{{Path: f.paths.DataDir}}, Reservations: []desktopupdate.InstallSpaceReservation{{Directory: root, InstallBytes: 1024, RecoveryBytes: 1024}}}
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
	self, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	records := readAdmissionRecords(t, root)
	digest := sha256.Sum256([]byte(f.cfg.OwnerToken))
	if len(records) != 1 || records[0].Role != "coordinator" || records[0].Actor != self || records[0].OwnerDigest != hex.EncodeToString(digest[:]) {
		t.Fatalf("guard did not preserve its parent's registration: %+v", records)
	}
}

func TestRuntimeNormalUpEntryKeepsWriterRegistrationUntilExit(t *testing.T) {
	f := newUpdateStateFixture(t)
	root := t.TempDir()
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
	records := readAdmissionRecords(t, root)
	if len(records) != 1 || records[0].Role != "writer" || records[0].Actor != result.Actor {
		t.Fatalf("normal up entry omitted its actual writer registration: %+v", records)
	}
	if alive, err := desktopupdate.SameProcess(result.Actor); err != nil || !alive {
		t.Fatalf("live CLI was declared stopped: %v %v", alive, err)
	}
	_, _ = in.Write([]byte("done\n"))
	_ = in.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if records := readAdmissionRecords(t, root); len(records) != 0 {
		t.Fatalf("exited CLI left a writer lease: %+v", records)
	}
	if alive, err := desktopupdate.SameProcess(result.Actor); err != nil || alive {
		t.Fatalf("exited CLI still alive: %v %v", alive, err)
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

// Journal input is a persisted fixture, not a call to a production transaction writer.
func writePendingAdmissionJournal(t *testing.T, root string, plan desktopupdate.TransactionPlan) {
	t.Helper()
	plan.Scope.Installation = canonicalAdmissionFixturePath(t, plan.Scope.Installation)
	roots := map[string]string{}
	for _, name := range plan.Scope.DataRoots {
		actual := canonicalAdmissionFixturePath(t, name)
		key := actual
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		roots[key] = actual
	}
	plan.Scope.DataRoots = nil
	for _, actual := range roots {
		plan.Scope.DataRoots = append(plan.Scope.DataRoots, actual)
	}
	sort.Strings(plan.Scope.DataRoots)
	journal := desktopupdate.UpdateJournal{Version: 1, ID: strings.Repeat("1", 32), Stage: "PREPARED", Revision: 1, Plan: plan, ResolvedScope: plan.Scope}
	raw, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "transactions", "v1", journal.ID, "journal.json")
	if err = os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func canonicalAdmissionFixturePath(t *testing.T, name string) string {
	t.Helper()
	actual, err := filepath.EvalSymlinks(name)
	if err == nil {
		return actual
	}
	if !os.IsNotExist(err) || filepath.Dir(name) == name {
		t.Fatal(err)
	}
	return filepath.Join(canonicalAdmissionFixturePath(t, filepath.Dir(name)), filepath.Base(name))
}

type admissionRecordFixture struct {
	Role        string
	OwnerDigest string
	Actor       desktopupdate.ProcessIdentity
	Scope       desktopupdate.UpdateScope
}

func readAdmissionRecords(t *testing.T, root string) []admissionRecordFixture {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(root, "startup", "v1", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []admissionRecordFixture
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var record admissionRecordFixture
		if err = json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}
func requireRegisteredDataRoot(t *testing.T, root, expected string) {
	t.Helper()
	records := readAdmissionRecords(t, root)
	want := canonicalAdmissionFixturePath(t, expected)
	for _, record := range records {
		for _, name := range record.Scope.DataRoots {
			if canonicalAdmissionFixturePath(t, name) == want {
				return
			}
		}
	}
	t.Fatalf("registration omitted expected shared root %s", expected)
}
