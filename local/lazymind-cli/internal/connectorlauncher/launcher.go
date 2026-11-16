// Package connectorlauncher keeps Agent registrations independent of the desktop
// installation directory. The executable is installed once; only its target
// record changes when a new desktop installation starts.
package connectorlauncher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lazymind/agentconnector/internal/localfile"
)

const directory = "connector-launcher"

type target struct {
	Version    int    `json:"version"`
	Home       string `json:"home"`
	Executable string `json:"executable"`
}

func Path(home string) string {
	name := "lazymind"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, directory, name)
}

func targetPath(launcher string) string { return filepath.Join(filepath.Dir(launcher), "target.json") }

// Ensure does not replace an existing live target: an older process inspecting
// its settings must not undo the target published by a newer installation.
func Ensure(ctx context.Context, home, self string) (string, error) {
	return ensure(ctx, home, self, false)
}

// Publish is called by an explicit installation lifecycle (startup/connect),
// never by periodic status or recovery checks.
func Publish(ctx context.Context, home, self string) (string, error) {
	return ensure(ctx, home, self, true)
}

func ensure(ctx context.Context, home, self string, publish bool) (string, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(self) {
		return "", errors.New("launcher home and executable must be absolute")
	}
	launcher := Path(home)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(launcher), 0700); err != nil {
		return "", err
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	unlock, err := localfile.LockContext(bounded, launcher+".lock")
	if err != nil {
		return "", err
	}
	defer unlock()

	previous, recordErr := readTargetFile(launcher)
	if recordErr != nil && !errors.Is(recordErr, os.ErrNotExist) {
		return "", recordErr
	}
	info, fileErr := os.Lstat(launcher)
	if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
		return "", fileErr
	}
	if fileErr == nil && (!info.Mode().IsRegular() || recordErr != nil) {
		return "", errors.New("the fixed launcher path is not owned by LazyMind")
	}
	if samePath(self, launcher) {
		if recordErr != nil {
			return "", errors.New("launcher has no current LazyMind target")
		}
		return launcher, nil
	}
	if isLauncherPath(self) {
		return "", errors.New("a launcher cannot target another LazyMind launcher")
	}
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		return "", fmt.Errorf("resolve current LazyMind executable: %w", err)
	}
	if isLauncherPath(resolved) || sameFile(resolved, launcher) {
		return "", errors.New("a launcher cannot target itself")
	}
	selfInfo, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !selfInfo.Mode().IsRegular() {
		return "", errors.New("current LazyMind executable is not a regular file")
	}

	current := target{Version: 1, Home: filepath.Clean(home), Executable: resolved}
	writeTarget := recordErr != nil
	if recordErr == nil {
		_, targetErr := os.Stat(previous.Executable)
		writeTarget = (publish || errors.Is(targetErr, os.ErrNotExist)) && !samePath(previous.Executable, resolved)
	}
	// Writing the ownership record before the first copy makes an interrupted
	// first installation retryable without overwriting an unrelated executable.
	if writeTarget {
		body, _ := json.MarshalIndent(current, "", "  ")
		if err := localfile.Write(targetPath(launcher), append(body, '\n'), 0600); err != nil {
			return "", err
		}
	}
	if errors.Is(fileErr, os.ErrNotExist) {
		if err := copyExecutable(resolved, launcher); err != nil {
			return "", err
		}
	}
	return launcher, nil
}

func readTargetFile(launcher string) (target, error) {
	var record target
	body, err := os.ReadFile(targetPath(launcher))
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return record, fmt.Errorf("read LazyMind launcher target: %w", err)
	}
	if record.Version != 1 || !filepath.IsAbs(record.Home) || !samePath(Path(record.Home), launcher) || !filepath.IsAbs(record.Executable) {
		return record, errors.New("invalid LazyMind launcher ownership record")
	}
	resolved, _ := filepath.EvalSymlinks(record.Executable)
	if isLauncherPath(record.Executable) || isLauncherPath(resolved) || sameFile(record.Executable, launcher) {
		return record, errors.New("invalid recursive LazyMind launcher target")
	}
	return record, nil
}

func copyExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(destination), ".launcher-*")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	if err = output.Chmod(0700); err == nil {
		_, err = io.Copy(output, input)
	}
	if err == nil {
		err = output.Sync()
	}
	closeErr := output.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return localfile.Replace(output.Name(), destination)
}

func isLauncherPath(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	parent, name := filepath.Base(filepath.Dir(path)), filepath.Base(path)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(parent, directory) && strings.EqualFold(name, filepath.Base(Path("")))
	}
	return parent == directory && name == filepath.Base(Path(""))
}

func samePath(left, right string) bool {
	if resolved, err := filepath.EvalSymlinks(left); err == nil {
		left = resolved
	}
	if resolved, err := filepath.EvalSymlinks(right); err == nil {
		right = resolved
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func sameFile(left, right string) bool {
	a, err := os.Stat(left)
	if err != nil {
		return false
	}
	b, err := os.Stat(right)
	return err == nil && os.SameFile(a, b)
}

// Forward must run before normal CLI initialization. A missing target is an
// error, rather than permission to run the old CLI copied into the launcher.
func Forward(args []string) (handled bool, code int, err error) {
	self, err := os.Executable()
	if err != nil {
		return false, 0, err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if !isLauncherPath(self) {
		return false, 0, nil
	}
	record, err := readTargetFile(self)
	if err != nil {
		return true, 1, err
	}
	code, err = forward(record.Executable, args)
	return true, code, err
}
