package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A real stdio child used as both the host-owned Connector and runtime worker.
func TestRuntimeCleanupFixtureProcess(t *testing.T) {
	if os.Getenv("LAZYMIND_CLEANUP_FIXTURE") != "1" {
		return
	}
	if os.Getenv("LAZYMIND_CLEANUP_BACKGROUND") == "1" {
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	if pidFile := os.Getenv("LAZYMIND_CLEANUP_SPAWN_CHILD"); pidFile != "" {
		self, _ := os.Executable()
		child := exec.Command(self, "-test.run=^TestRuntimeCleanupFixtureProcess$")
		child.Env = withEnvOverrides(os.Environ(), "LAZYMIND_CLEANUP_SPAWN_CHILD=", "LAZYMIND_CLEANUP_BACKGROUND=1")
		if child.Start() != nil {
			os.Exit(2)
		}
		if os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600) != nil {
			os.Exit(2)
		}
		go func() { _ = child.Wait() }()
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request.ID,
			"result": map[string]any{"tools": []any{map[string]string{"name": "fixture.ping"}}},
		})
	}
	os.Exit(0)
}

type cleanupFixture struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *json.Decoder
	done chan error
}

func newCleanupFixture(t *testing.T, binary, owner string, configure func(*exec.Cmd)) *cleanupFixture {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(binary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy fixture: %v / %v", copyErr, closeErr)
	}
	cmd := exec.Command(binary, "-test.run=^TestRuntimeCleanupFixtureProcess$", "--", "mcp", "proxy")
	cmd.Env = append(os.Environ(), "LAZYMIND_CLEANUP_FIXTURE=1", runtimeProcessOwnerEnvVar+"="+owner)
	configureChildProcess(cmd, false)
	if configure != nil {
		configure(cmd)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fixture := &cleanupFixture{cmd: cmd, in: in, out: json.NewDecoder(out), done: make(chan error, 1)}
	go func() { fixture.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		select {
		case <-fixture.done:
		case <-time.After(5 * time.Second):
			t.Error("fixture did not exit")
		}
	})
	fixture.assertTools(t, 1)
	return fixture
}

func (f *cleanupFixture) assertTools(t *testing.T, id int) {
	t.Helper()
	if err := json.NewEncoder(f.in).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/list"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		var result struct {
			ID     int `json:"id"`
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		err := f.out.Decode(&result)
		if err == nil && (result.ID != id || len(result.Result.Tools) != 1 || result.Result.Tools[0].Name != "fixture.ping") {
			err = io.ErrUnexpectedEOF
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stdio tools/list failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdio tools/list timed out")
	}
}

func TestRuntimeCleanupPreservesHostConnector(t *testing.T) {
	for _, profile := range []string{"local", "desktop"} {
		t.Run(profile, func(t *testing.T) {
			root := t.TempDir()
			paths := RuntimePaths{RepoRoot: root, BuildRoot: filepath.Join(root, "build"), RuntimeRoot: filepath.Join(root, "data"), ResourcesRoot: filepath.Join(root, "resources"), ProcessRegistryFile: filepath.Join(root, "run", "processes.json")}
			bin := filepath.Join(paths.BuildRoot, "bin")
			if profile == "desktop" {
				bin = filepath.Join(paths.ResourcesRoot, "bin")
			}
			name := "lazymind"
			if filepath.Ext(os.Args[0]) == ".exe" {
				name += ".exe"
			}
			proxy := newCleanupFixture(t, filepath.Join(bin, name), paths.RuntimeRoot, nil)
			worker := newCleanupFixture(t, filepath.Join(bin, "runtime-worker"+filepath.Ext(name)), paths.RuntimeRoot, nil)
			unknown := newCleanupFixture(t, filepath.Join(bin, "unrelated"+filepath.Ext(name)), "", nil)
			registerLocalProcess(paths, "test-worker", worker.cmd.Process.Pid, nil, worker.cmd.Args)
			manager := NewRuntimeManager(&fakeRunner{t: t}, "")
			manager.SetOutput(io.Discard, io.Discard)
			for pass := 0; pass < 2; pass++ {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := manager.killStaleRuntimeProcesses(ctx, RuntimeConfig{}, paths)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				proxy.assertTools(t, pass+2)
				unknown.assertTools(t, pass+2)
			}
			if processAlive(worker.cmd.Process.Pid) {
				t.Fatal("registered runtime worker survived cleanup")
			}
		})
	}
}

func TestRuntimeCleanupFindsSavedChildAfterParentExit(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{RepoRoot: root, BuildRoot: root, RuntimeRoot: filepath.Join(root, "data"), ProcessRegistryFile: filepath.Join(root, "run", "processes.json")}
	pidFile := filepath.Join(root, "child.pid")
	name := "runtime-worker"
	if filepath.Ext(os.Args[0]) == ".exe" {
		name += ".exe"
	}
	parent := newCleanupFixture(t, filepath.Join(root, "bin", name), "", func(cmd *exec.Cmd) {
		cmd.Env = append(cmd.Env, "LAZYMIND_CLEANUP_SPAWN_CHILD="+pidFile)
	})
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(string(raw))
	if err != nil || childPID <= 0 {
		t.Fatalf("child PID %q: %v", raw, err)
	}
	childIdentity := processStartIdentity(childPID)
	t.Cleanup(func() {
		if childIdentity != 0 && processStartIdentity(childPID) == childIdentity {
			_ = forceKillProcessPID(childPID)
		}
	})
	registerLocalProcess(paths, "test-parent", parent.cmd.Process.Pid, nil, parent.cmd.Args)
	manager := NewRuntimeManager(&fakeRunner{t: t}, "")
	manager.SetOutput(io.Discard, io.Discard)
	if err := manager.rememberRuntimeProcesses(RuntimeConfig{}, paths); err != nil {
		t.Fatal(err)
	}
	if err := parent.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(parent.cmd.Process.Pid) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if processAlive(parent.cmd.Process.Pid) {
		t.Fatal("parent did not exit")
	}
	if err := manager.killStaleRuntimeProcesses(context.Background(), RuntimeConfig{}, paths); err != nil {
		t.Fatal(err)
	}
	if processStartIdentity(childPID) != 0 && processAlive(childPID) {
		t.Fatal("saved detached child survived cleanup")
	}
}
