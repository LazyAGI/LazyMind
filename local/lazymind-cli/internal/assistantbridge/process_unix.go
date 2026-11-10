//go:build !windows

package assistantbridge

import (
	"os/exec"
	"syscall"
)

func ownerExited(pid int) bool { return syscall.Kill(pid, 0) == syscall.ESRCH }

func detachedProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

func startBridgeProcess(command *exec.Cmd) (<-chan error, func(), error) {
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	return done, func() { _ = command.Process.Kill() }, nil
}
