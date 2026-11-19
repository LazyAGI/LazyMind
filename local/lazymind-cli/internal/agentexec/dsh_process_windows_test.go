package agentexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDSHProcessHelper(t *testing.T) {
	if os.Getenv("LAZYMIND_DSH_PROCESS_TEST") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

func TestDSHDesktopRunningWindows(t *testing.T) {
	root := t.TempDir()
	cli := filepath.Join(root, "resources", "runtime", "cli", "bin", "dsh.cmd")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	application := filepath.Join(root, "DeepSeek Harness.exe")
	if err := os.WriteFile(application, body, 0700); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(application, "-test.run=^TestDSHProcessHelper$")
	child.Env = append(os.Environ(), "LAZYMIND_DSH_PROCESS_TEST=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if running, err := DSHDesktopRunning(context.Background(), cli); err != nil || !running {
		t.Fatalf("live process: running=%v err=%v", running, err)
	}
	other := filepath.Join(t.TempDir(), "resources", "runtime", "cli", "bin", "dsh.cmd")
	if running, err := DSHDesktopRunning(context.Background(), other); err != nil || running {
		t.Fatalf("other installation: running=%v err=%v", running, err)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if running, err := DSHDesktopRunning(context.Background(), cli); err != nil || running {
		t.Fatalf("exited process: running=%v err=%v", running, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DSHDesktopRunning(ctx, cli); err == nil {
		t.Fatal("cancelled inspection accepted")
	}
}
