//go:build darwin

package main

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDarwinProcessArgumentsRetainEnvironmentAndSpacedArguments(t *testing.T) {
	raw := make([]byte, 4)
	binary.NativeEndian.PutUint32(raw, 3)
	raw = append(raw, []byte("/bin/worker\x00\x00/bin/worker\x00--config\x00/runtime with spaces/config\x00LAZYMIND_RUNTIME_PROCESS_OWNER=/runtime with spaces\x00\x00")...)
	args, owner := parseDarwinProcessArgs(raw)
	if len(args) != 3 || args[2] != "/runtime with spaces/config" || owner != "/runtime with spaces" {
		t.Fatalf("args=%q owner=%q", args, owner)
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
		if profile == "bridge" {
			body, err := os.ReadFile(self)
			if err != nil {
				t.Fatal(err)
			}
			self = filepath.Join(root, "lazymind")
			if err := os.WriteFile(self, body, 0700); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(self, "-test.run=^TestDarwinScanChildProcess$")
		cmd.Env = append(os.Environ(), runtimeRootEnvVar+"="+runtimeRoot, runtimeProcessOwnerEnvVar+"="+runtimeRoot, runtimeProfileEnvVar+"="+profile, "LAZYMIND_SCAN_TEST_HELPER=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd.Process.Pid
	}
	own := start(paths.RuntimeRoot, "desktop")
	other := start(filepath.Join(root, "compose"), "local")
	shared := start(paths.RuntimeRoot, "bridge")
	inventory, err := scanLocalRuntimeProcesses(paths)
	if err != nil {
		t.Fatal(err)
	}
	records := ownedRuntimeProcesses(paths, nil, inventory, nil)
	found := false
	for _, record := range records {
		if record.PID == other || record.PID == shared {
			t.Fatal("claimed another deployment's process")
		}
		found = found || record.PID == own
	}
	if !found {
		for _, record := range inventory {
			if record.PID == own {
				t.Fatalf("did not claim owned orphan: %+v", record)
			}
		}
		t.Fatal("did not discover owned orphan")
	}
}

func TestDarwinScanChildProcess(t *testing.T) {
	if os.Getenv("LAZYMIND_SCAN_TEST_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

func TestParseDarwinProcessArgsSeparatesArgumentsAndOwnership(t *testing.T) {
	args := []string{"/repo/bin/python", "-c", "", runtimeProcessOwnerEnvVar + "=/spoofed"}
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, uint32(len(args)))
	raw = append(raw, []byte("/repo/bin/python\x00\x00\x00")...)
	for _, arg := range args {
		raw = append(raw, append([]byte(arg), 0)...)
	}
	raw = append(raw, []byte("OTHER=value\x00"+runtimeProcessOwnerEnvVar+"=/owned\x00")...)
	got, owner := parseDarwinProcessArgs(raw)
	if !reflect.DeepEqual(got, args) || owner != "/owned" {
		t.Fatalf("args=%q owner=%q", got, owner)
	}
	for _, invalid := range [][]byte{nil, {1, 0, 0, 0}, raw[:len(raw)/2]} {
		if _, owner := parseDarwinProcessArgs(invalid); owner != "" {
			t.Fatalf("malformed args supplied owner %q", owner)
		}
	}
}
