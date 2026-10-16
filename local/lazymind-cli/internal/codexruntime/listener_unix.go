//go:build !windows

package codexruntime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func listenerIdentity(c *runtimeConfig) (any, error) {
	endpoint := filepath.Join(c.RuntimeDir, "daemon.sock")
	info, err := os.Lstat(endpoint)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !ownedByCurrentUser(info) {
		return nil, errors.New("endpoint belongs to another user")
	}
	entry := info.Sys().(*syscall.Stat_t)
	target := endpoint
	if info.Mode()&os.ModeSymlink != 0 {
		link, e := os.Readlink(endpoint)
		if e != nil {
			return nil, e
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(c.RuntimeDir, link)
		}
		target = filepath.Clean(link)
		native := "/private/tmp/codex-daemon-" + strconv.Itoa(os.Getuid())
		if filepath.Dir(target) != native {
			return nil, errors.New("unexpected native socket target")
		}
		parent, e := os.Lstat(native)
		if e != nil || !parent.IsDir() || !ownedByCurrentUser(parent) || parent.Mode().Perm()&0022 != 0 {
			return nil, errors.New("unsafe native socket directory")
		}
	} else if info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("endpoint is neither Unix socket nor native link")
	}
	socket, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return map[string]any{"endpoint": []uint64{uint64(entry.Dev), entry.Ino}, "target": target, "socket": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	if socket.Mode()&os.ModeSocket == 0 || !ownedByCurrentUser(socket) || socket.Mode().Perm()&0077 != 0 {
		return nil, errors.New("unsafe socket")
	}
	s := socket.Sys().(*syscall.Stat_t)
	return map[string]any{"endpoint": []uint64{uint64(entry.Dev), entry.Ino}, "target": target, "socket": []uint64{uint64(s.Dev), s.Ino}}, nil
}
func verifyListener(c *runtimeConfig, r *backendRecord) error {
	current, err := listenerIdentity(c)
	if err != nil {
		return err
	}
	if current == nil || current.(map[string]any)["socket"] == nil || !equalRecord(current, r.Socket) {
		return errors.New("backend socket identity changed")
	}
	target := current.(map[string]any)["target"].(string)
	out, err := exec.Command("/usr/sbin/lsof", "-a", "-p", strconv.Itoa(r.Process.PID), "-U", "-Fn").Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "n"+target {
			return nil
		}
	}
	return errors.New("socket listener does not belong to backend")
}
func removeStaleListener(c *runtimeConfig, r *backendRecord) error {
	current, err := listenerIdentity(c)
	if err != nil {
		return err
	}
	if current == nil {
		// A dangling native link still needs ownership evidence; do not remove blindly.
		if _, err = os.Lstat(filepath.Join(c.RuntimeDir, "daemon.sock")); err == nil {
			return errors.New("stale endpoint cannot be verified")
		}
		return nil
	}
	previous, ok := r.Socket.(map[string]any)
	now := current.(map[string]any)
	if !ok || !equalRecord(now["endpoint"], previous["endpoint"]) || now["target"] != previous["target"] || (now["socket"] != nil && !equalRecord(now["socket"], previous["socket"])) {
		return errors.New("unknown socket; refusing removal")
	}
	if now["socket"] == nil {
		return os.Remove(filepath.Join(c.RuntimeDir, "daemon.sock"))
	}
	target := current.(map[string]any)["target"].(string)
	out, err := exec.Command("/usr/sbin/lsof", "-t", target).Output()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || len(out) != 0 {
		return errors.New("stale endpoint may still be held")
	}
	if err = os.Remove(target); err != nil {
		return err
	}
	endpoint := filepath.Join(c.RuntimeDir, "daemon.sock")
	if endpoint != target {
		return os.Remove(endpoint)
	}
	return nil
}
