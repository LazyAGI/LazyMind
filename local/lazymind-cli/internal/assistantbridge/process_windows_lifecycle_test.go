//go:build windows

package assistantbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"lazymind/agentconnector/internal/connectorlauncher"
)

const (
	lifecycleRoleKey = "LAZYMIND_TEST_LAUNCHER_LIFECYCLE_ROLE"
	lifecycleRootKey = "LAZYMIND_TEST_LAUNCHER_LIFECYCLE_ROOT"
	lifecycleHelper  = "^TestWindowsLauncherDetachedServiceHelper$"
)

type detachedLifecycleReady struct {
	PID         int    `json:"pid"`
	LauncherJob string `json:"launcher_job"`
}

// Exercise the actual launcher process, not just its creation flags: closing
// the foreground MCP job must leave the shared, detached service alive.
func TestWindowsDetachedServiceSurvivesLauncherExit(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "state")
	t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_HOME", home)
	var daemon windows.Handle
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(root, "stop"), nil, 0600)
		if daemon != 0 {
			if state, err := windows.WaitForSingleObject(daemon, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
				_ = windows.TerminateProcess(daemon, 1)
				_, _ = windows.WaitForSingleObject(daemon, 5000)
			}
			_ = windows.CloseHandle(daemon)
		}
	})

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := connectorlauncher.Publish(t.Context(), home, self)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, launcher, "-test.run="+lifecycleHelper)
	command.Env = append(os.Environ(), lifecycleRoleKey+"=foreground", lifecycleRootKey+"="+root)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		log, _ := os.ReadFile(filepath.Join(root, "daemon.log"))
		t.Fatalf("foreground helper failed: %v; stdout=%q stderr=%q daemon=%q", err, stdout.String(), stderr.String(), log)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("foreground helper wrote protocol output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	body, err := os.ReadFile(filepath.Join(root, "ready"))
	if err != nil {
		t.Fatal(err)
	}
	var ready detachedLifecycleReady
	if err := json.Unmarshal(body, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.PID <= 0 || ready.LauncherJob != "" {
		t.Fatalf("invalid detached service identity/environment: %+v", ready)
	}
	daemon, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(ready.PID))
	if err != nil {
		t.Fatalf("detached service did not survive foreground launcher exit: %v", err)
	}
	if state, err := windows.WaitForSingleObject(daemon, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("detached service exited with the launcher: state=%d err=%v", state, err)
	}
	const request = "respond after the MCP launcher has exited"
	if err := os.WriteFile(filepath.Join(root, "request"), []byte(request), 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitDetachedLifecycleFile(filepath.Join(root, "response"), request, 5*time.Second); err != nil {
		t.Fatalf("detached service stopped responding after launcher exit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "stop"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitDetachedLifecycleFile(filepath.Join(root, "stopped"), "stopped", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if state, err := windows.WaitForSingleObject(daemon, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("detached service failed to stop: state=%d err=%v", state, err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(daemon, &code); err != nil || code != 0 {
		t.Fatalf("detached service did not exit cleanly: code=%d err=%v", code, err)
	}
}

func TestWindowsLauncherDetachedServiceHelper(t *testing.T) {
	role := os.Getenv(lifecycleRoleKey)
	if role == "" {
		return
	}
	if handled, code, err := connectorlauncher.Forward(os.Args[1:]); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	}
	root := os.Getenv(lifecycleRootKey)
	if root == "" {
		_, _ = fmt.Fprintln(os.Stderr, "missing lifecycle helper directory")
		os.Exit(1)
	}
	var err error
	switch role {
	case "foreground":
		err = runDetachedLifecycleForeground(root)
	case "daemon":
		err = runDetachedLifecycleDaemon(root)
	default:
		err = fmt.Errorf("unknown lifecycle helper role %q", role)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runDetachedLifecycleForeground(root string) error {
	if os.Getenv("LAZYMIND_CONNECTOR_LAUNCHER_JOB") != "1" {
		return errors.New("foreground helper was not started through the launcher")
	}
	// The launcher assigns its child after Start. Do not accidentally pass by
	// starting the daemon in that window, before the foreground job applies.
	deadline := time.Now().Add(5 * time.Second)
	for {
		limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil)
		want := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK)
		if err == nil && limits.BasicLimitInformation.LimitFlags&want == want {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("foreground helper did not join the launcher job: flags=%#x err=%v", limits.BasicLimitInformation.LimitFlags, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.Create(filepath.Join(root, "daemon.log"))
	if err != nil {
		return err
	}
	defer log.Close()
	command := exec.Command(self, "-test.run="+lifecycleHelper)
	command.SysProcAttr = detachedProcessAttributes()
	command.Env = append(detachedServiceEnvironment(filepath.Join(root, "state")), lifecycleRoleKey+"=daemon")
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		return fmt.Errorf("start detached service: %w", err)
	}
	_ = command.Process.Release()
	return waitDetachedLifecycleFile(filepath.Join(root, "ready"), "", 5*time.Second)
}

func runDetachedLifecycleDaemon(root string) error {
	ready := detachedLifecycleReady{PID: os.Getpid(), LauncherJob: os.Getenv("LAZYMIND_CONNECTOR_LAUNCHER_JOB")}
	if ready.LauncherJob != "" {
		return errors.New("detached service inherited the foreground launcher marker")
	}
	body, err := json.Marshal(ready)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "ready.tmp"), body, 0600); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(root, "ready.tmp"), filepath.Join(root, "ready")); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, "stop")); err == nil {
			return os.WriteFile(filepath.Join(root, "stopped"), []byte("stopped"), 0600)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		request, err := os.ReadFile(filepath.Join(root, "request"))
		if err == nil && string(request) != last {
			if err := os.WriteFile(filepath.Join(root, "response"), request, 0600); err != nil {
				return err
			}
			last = string(request)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("detached lifecycle helper timed out")
}

func waitDetachedLifecycleFile(path, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil && (want == "" || string(body) == want) {
			return nil
		}
		// Publishing the signal can briefly deny opens on Windows.
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s=%q", filepath.Base(path), want)
}

func TestWaitDetachedLifecycleFileRetriesSharingViolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready")
	if err := os.WriteFile(path, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	locked := os.NewFile(uintptr(handle), path)
	defer locked.Close()
	if err := waitDetachedLifecycleFile(path, "ready", 50*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out waiting for") {
		t.Fatalf("sharing violation must remain pending until the deadline: %v", err)
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitDetachedLifecycleFile(path, "ready", time.Second); err != nil {
		t.Fatalf("released signal did not become readable: %v", err)
	}
}
