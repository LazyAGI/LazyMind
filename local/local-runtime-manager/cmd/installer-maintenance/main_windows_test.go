package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winprocess"
)

func TestLocalAppDataTargetIsFixedChild(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Local")
	got, err := localAppDataTarget(base)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "LazyMind"); got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
	if _, err := localAppDataTarget("relative"); err == nil {
		t.Fatal("relative Local AppData root was accepted")
	}
}

func TestProcessAliveDetectsCurrentProcess(t *testing.T) {
	if !processAlive(uint32(os.Getpid())) {
		t.Fatal("current process was not detected as alive")
	}
}

func TestCheckStoppedValidatesRegistryProcessStartIdentity(t *testing.T) {
	root := t.TempDir()
	cmd := startInstallerMaintenanceHelperProcess(t)
	startID := processStartIdentity(uint32(cmd.Process.Pid))
	if startID == 0 {
		t.Fatal("could not read child process start identity")
	}
	writeRegistry := func(id uint64) {
		t.Helper()
		registry := processRegistry{Processes: []processRecord{{
			Service: "test-runtime",
			PID:     cmd.Process.Pid,
			StartID: id,
		}}}
		raw, err := json.Marshal(registry)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "run"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "run", "processes.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writeRegistry(startID + 1)
	if err := checkStopped(root); err != nil {
		t.Fatalf("stale reused PID blocked setup: %v", err)
	}
	writeRegistry(startID)
	if err := checkStopped(root); err == nil {
		t.Fatal("matching live runtime process did not block setup")
	}
}

func TestForceStopTerminatesRegisteredProcess(t *testing.T) {
	root := t.TempDir()
	cmd := startInstallerMaintenanceHelperProcess(t)
	startID := processStartIdentity(uint32(cmd.Process.Pid))
	if startID == 0 {
		t.Fatal("could not read child process start identity")
	}
	registry := processRegistry{Processes: []processRecord{{
		Service: "test-runtime",
		PID:     cmd.Process.Pid,
		StartID: startID,
	}}}
	raw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run", "processes.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	stopped, err := forceStop(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0].PID != cmd.Process.Pid {
		t.Fatalf("stopped = %#v, want pid %d", stopped, cmd.Process.Pid)
	}
	if stopped[0].MatchReason != "registered-pid-start-id" {
		t.Fatalf("match reason = %q", stopped[0].MatchReason)
	}
	if processAlive(uint32(cmd.Process.Pid)) {
		t.Fatal("registered child process is still alive")
	}
	if _, err := os.Stat(filepath.Join(root, "run", "processes.json")); !os.IsNotExist(err) {
		t.Fatalf("process registry was not removed: %v", err)
	}
}

func TestInstallerMaintenanceHelperProcess(t *testing.T) {
	if os.Getenv("LAZYMIND_INSTALLER_HELPER_PROCESS") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

func startInstallerMaintenanceHelperProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallerMaintenanceHelperProcess$")
	cmd.Env = append(os.Environ(), "LAZYMIND_INSTALLER_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func TestParseCommandOptions(t *testing.T) {
	installDir := filepath.Join(`C:\Users\test\AppData\Local\Programs`, "LazyMind")
	opts, err := parseCommandOptions([]string{"force-stop", "--install-dir", installDir})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Command != "force-stop" || opts.InstallDir != filepath.Clean(installDir) {
		t.Fatalf("options = %#v", opts)
	}
	if _, err := parseCommandOptions(nil); err == nil {
		t.Fatal("missing command was accepted")
	}
	if _, err := parseCommandOptions([]string{"force-stop", "--install-dir"}); err == nil {
		t.Fatal("missing install directory was accepted")
	}
	if _, err := parseCommandOptions([]string{"force-stop", "--unknown"}); err == nil {
		t.Fatal("unknown argument was accepted")
	}
}

func TestMatchLazyMindProcessMatchesExecutableUnderInstallDir(t *testing.T) {
	installDir := filepath.Join(`C:\Users\test\AppData\Local\Programs`, "LazyMind")
	process := processInfo(1002, filepath.Join(installDir, "resources", "runtime", "bin", "auth-service.exe"))
	roots := compactRoots(installDir)
	reason, matched := matchLazyMindProcess(process, processRecord{}, false, true, roots)
	if !matched || reason != "executable-under-owned-root" {
		t.Fatalf("matched=%v reason=%q", matched, reason)
	}
}

func TestExecutableRootMatchRequiresPathBoundary(t *testing.T) {
	installDir := filepath.Join(`C:\Users\test\AppData\Local\Programs`, "LazyMind")
	lookalike := installDir + "-tools" + string(filepath.Separator) + "worker.exe"
	process := processInfo(1004, lookalike)
	roots := compactRoots(installDir)
	reason, matched := matchLazyMindProcess(process, processRecord{}, false, true, roots)
	if matched {
		t.Fatalf("lookalike root matched with reason %q", reason)
	}
}

func TestRepoRootDoesNotAuthorizeArbitraryExecutable(t *testing.T) {
	installDir := filepath.Join(`C:\Users\test\AppData\Local\Programs`, "LazyMind")
	repoRoot := filepath.Join(`C:\Users\test\src`, "LazyMind")
	process := processInfo(1005, filepath.Join(repoRoot, "tools", "unrelated.exe"))
	reason, matched := matchLazyMindProcess(process, processRecord{}, false, true, compactRoots(installDir))
	if matched {
		t.Fatalf("arbitrary repo executable matched with reason %q", reason)
	}
}

func TestRegisteredProcessRequiresMatchingStartIdentity(t *testing.T) {
	process := processInfo(1006, `C:\Python311\python.exe`)
	record := processRecord{Service: "auth-service", PID: 1006, StartID: 456}
	if reason, matched := matchLazyMindProcess(process, record, true, false, nil); matched {
		t.Fatalf("reused registered PID matched with reason %q", reason)
	}
	process.StartID = 456
	if reason, matched := matchLazyMindProcess(process, record, true, false, nil); !matched || reason != "registered-pid-start-id" {
		t.Fatalf("matching registered PID: matched=%v reason=%q", matched, reason)
	}
}

func TestCrossSessionMatchRequiresRegistrationOrTrustedExecutableRoot(t *testing.T) {
	installDir := filepath.Join(`C:\Users\test\AppData\Local\Programs`, "LazyMind")
	roots := compactRoots(installDir)
	owned := processInfo(1007, filepath.Join(installDir, "resources", "runtime", "bin", "local-runtime-manager.exe"))
	if reason, matched := matchLazyMindProcess(owned, processRecord{}, false, false, roots); !matched || reason != "executable-under-owned-root" {
		t.Fatalf("cross-session owned process: matched=%v reason=%q", matched, reason)
	}
	untrusted := processInfo(1008, `D:\OtherUser\LazyMind.exe`)
	if reason, matched := matchLazyMindProcess(untrusted, processRecord{}, false, false, roots); matched {
		t.Fatalf("cross-session name-only process matched with reason %q", reason)
	}
}

func TestTrustedInstallDirAcceptsOnlyDefaultPerUserPath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, appDataLeaf)
	expected := filepath.Join(base, "Programs", appDataLeaf)
	if got, err := trustedInstallDir(root, expected); err != nil || got != expected {
		t.Fatalf("trusted install dir = %q, err=%v", got, err)
	}
	if _, err := trustedInstallDir(root, filepath.Join(base, "Programs", "Other")); err == nil {
		t.Fatal("unexpected install directory was trusted")
	}
	if _, err := trustedInstallDir(root, `C:\Windows`); err == nil {
		t.Fatal("system directory was trusted as the install directory")
	}
}

func TestReadProcessRegistryRetriesTransientPartialWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "run", "processes.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"processes":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		time.Sleep(registryReadDelay + 10*time.Millisecond)
		done <- os.WriteFile(path, []byte(`{"processes":[{"service":"core","pid":42,"startId":99}]}`), 0o600)
	}()
	registry, err := readProcessRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(registry.Processes) != 1 || registry.Processes[0].StartID != 99 {
		t.Fatalf("registry = %#v", registry)
	}
}

func processInfo(pid uint32, executable string) winprocess.Info {
	return winprocess.Info{
		ProcessID:      pid,
		StartID:        123,
		ExecutablePath: stringPointer(executable),
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestPurgeLocalDataDoesNotTouchSiblingData(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "LazyMind")
	documents := filepath.Join(base, "Documents", "LazyMind")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(documents, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(documents, "keep.txt")
	if err := os.WriteFile(filepath.Join(target, "remove.txt"), []byte("remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := purgeLocalData(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("purged target still exists or stat failed unexpectedly: %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("sibling user data changed: content=%q err=%v", got, err)
	}
}
