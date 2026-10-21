package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The packaged Electron binary stays available under an internal name because
// LazyMind intentionally reuses it as Node for managed export workers.
const electronExecutableName = "LazyMindApp"

func main() {
	launcherPath, err := os.Executable()
	if err != nil {
		failLaunch("resolve the LazyMind launcher path", err)
	}
	electronPath := electronExecutablePath(launcherPath, electronExecutableName)
	environment, removedRunAsNode := sanitizedEnvironment(os.Environ())
	if removedRunAsNode {
		logLaunch("removed inherited %s before starting %s", runAsNodeVariable, electronExecutableName)
	}

	arguments := append([]string{electronPath}, os.Args[1:]...)
	if err := syscall.Exec(electronPath, arguments, environment); err != nil {
		failLaunch(fmt.Sprintf("start %s", electronExecutableName), err)
	}
}

func failLaunch(operation string, err error) {
	message := fmt.Sprintf("LazyMind could not start (%s): %v", operation, err)
	logLaunch("%s", message)
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func logLaunch(format string, args ...any) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	logPath := filepath.Join(home, "Library", "Logs", "LazyMind", "desktop-launcher.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
