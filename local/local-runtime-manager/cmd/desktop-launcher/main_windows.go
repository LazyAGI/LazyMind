package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// The packaged Electron binary stays available under an internal name because
// LazyMind intentionally reuses it as Node for managed export workers.
const electronExecutableName = "LazyMindApp.exe"

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

	command := exec.Command(electronPath, os.Args[1:]...)
	command.Env = environment
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		failLaunch(fmt.Sprintf("start %s", electronExecutableName), err)
	}
}

func failLaunch(operation string, err error) {
	message := fmt.Sprintf("LazyMind could not start (%s): %v", operation, err)
	logLaunch("%s", message)
	text, textErr := windows.UTF16PtrFromString(message)
	caption, captionErr := windows.UTF16PtrFromString("LazyMind startup failed")
	if textErr == nil && captionErr == nil {
		_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
	}
	os.Exit(1)
}

func logLaunch(format string, args ...any) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return
	}
	logPath := filepath.Join(localAppData, "LazyMind", "Logs", "desktop", "desktop-launcher.log")
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
