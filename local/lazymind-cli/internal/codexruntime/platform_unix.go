//go:build !windows

package codexruntime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func platformProcess(pid int) (*ProcessInfo, error) {
	if pid < 1 {
		return nil, errors.New("invalid PID")
	}
	out, err := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "ppid=,uid=,lstart=,comm=").Output()
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parts := strings.Fields(string(out))
	if len(parts) < 8 {
		return nil, nil
	}
	uid, err := strconv.Atoi(parts[1])
	if err != nil || uid != os.Getuid() {
		return nil, errors.New("process belongs to another user")
	}
	parent, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, err
	}
	return &ProcessInfo{pid, parent, strings.Join(parts[7:], " "), strings.Join(parts[1:], " ")}, nil
}
func platformRunning(executable string) (bool, error) {
	out, err := exec.Command("/bin/ps", "-axo", "comm=").Output()
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == executable {
			return true, nil
		}
	}
	return false, nil
}
func checkPrivate(file string) error {
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("expected a private owned path: %s", file)
	}
	return nil
}
func makePrivate(file string) error {
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
		return errors.New("refusing to change unowned path")
	}
	mode := os.FileMode(0600)
	if info.IsDir() {
		mode = 0700
	}
	return os.Chmod(file, mode)
}
func detachedCommand(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func platformEnvironment(values map[string]*string, types map[string]uint32) (*environmentSnapshot, error) {
	return nil, errors.New("GUI environment is managed by the macOS LaunchAgent")
}
