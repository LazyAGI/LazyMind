//go:build darwin

package main

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDarwinProcessArgumentsRetainEnvironmentAndSpacedArguments(t *testing.T) {
	raw := make([]byte, 4)
	binary.NativeEndian.PutUint32(raw, 3)
	raw = append(raw, []byte("/bin/worker\x00\x00/bin/worker\x00--config\x00/runtime with spaces/config\x00LAZYMIND_RUNTIME_ROOT=/runtime with spaces\x00\x00")...)
	args, env := parseDarwinProcessArguments(raw)
	if len(args) != 3 || args[2] != "/runtime with spaces/config" || len(env) != 1 {
		t.Fatalf("args=%q env=%q", args, env)
	}
}

func TestDarwinScannerDoesNotClaimAnotherRuntimeOrSharedWorker(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, ResourcesRoot: "/bin", BuildRoot: "/bin", RuntimeRoot: filepath.Join(root, "desktop")}
	start := func(runtimeRoot, profile string) int {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(self, "-test.run=^TestDarwinScanChildProcess$")
		cmd.Env = append(os.Environ(), runtimeRootEnvVar+"="+runtimeRoot, runtimeProfileEnvVar+"="+profile, "LAZYMIND_SCAN_TEST_HELPER=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd.Process.Pid
	}
	own := start(paths.RuntimeRoot, "desktop")
	other := start(filepath.Join(root, "compose"), "local")
	shared := start(paths.RuntimeRoot, "bridge")
	records, err := scanLocalRuntimeProcesses(paths)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.PID == other || record.PID == shared {
			t.Fatal("claimed another deployment's process")
		}
		found = found || record.PID == own
	}
	if !found {
		args, env := darwinProcessArguments(own)
		rootEnv := ""
		for _, entry := range env {
			if strings.HasPrefix(entry, runtimeRootEnvVar+"=") {
				rootEnv = entry
			}
		}
		t.Fatalf("did not discover owned orphan: args=%q env_count=%d root=%q", args, len(env), rootEnv)
	}
}

func TestDarwinScanChildProcess(t *testing.T) {
	if os.Getenv("LAZYMIND_SCAN_TEST_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
}
