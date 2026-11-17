//go:build windows

package connectorlauncher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func forward(executable string, args []string) (int, error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := exec.CommandContext(ctx, executable, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Env = os.Environ()
	command.Env = append(command.Env, "LAZYMIND_CONNECTOR_LAUNCHER_JOB=1")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// Closing the launcher's job handle also terminates the forwarded process
	// tree when the Agent terminates the launcher before stdin reaches EOF.
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 1, err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return 1, err
	}
	if err := command.Start(); err != nil {
		return 1, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(command.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		if err != nil {
			// A command may have finished between Start and job assignment.
			var exitCode uint32
			if probeErr := windows.GetExitCodeProcess(process, &exitCode); probeErr == nil && exitCode != 259 {
				err = nil
			}
		}
		_ = windows.CloseHandle(process)
	}
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return 1, err
	}
	err = command.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
