//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func configureChildProcess(cmd *exec.Cmd, sessionLeader bool) {
	if sessionLeader {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interruptProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGINT); err == nil {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGINT)
}

func forceKillProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	return syscall.Kill(pid, syscall.SIGKILL)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func nativeProcessGroupID(pid int) int {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return 0
	}
	return pgid
}

func processStartIdentity(pid int) uint64 { return 0 }

func attachProcessJob(_ RuntimePaths, _ string, _ *os.Process) (func(), error) {
	return func() {}, nil
}

func terminateProcessJob(_ RuntimePaths, _ string) error { return os.ErrNotExist }

func stopSupervisorProcess(pid int) error { return interruptProcess(pid) }
