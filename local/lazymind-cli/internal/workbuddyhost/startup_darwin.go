package workbuddyhost

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lazymind/agentconnector/internal/desktopsetup"
)

type launchEnvironment struct{ ctx context.Context }

func (e launchEnvironment) Get() (string, error) {
	output, err := exec.CommandContext(e.ctx, "/bin/launchctl", "getenv", desktopDebugPortEnv).CombinedOutput()
	// launchctl returns 1 with no output for an unset variable.
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 && len(output) == 0 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read WorkBuddy launch environment: %s: %w", output, err)
	}
	return strings.TrimSpace(string(output)), nil
}
func (e launchEnvironment) Set(value string) error {
	return e.run("setenv", desktopDebugPortEnv, value)
}
func (e launchEnvironment) Unset() error { return e.run("unsetenv", desktopDebugPortEnv) }
func (e launchEnvironment) run(args ...string) error {
	output, err := exec.CommandContext(e.ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("configure WorkBuddy launch environment: %s: %w", output, err)
	}
	return nil
}
func configureDesktopStartup(ctx context.Context, home string, port int, enabled bool) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	unlock, err := desktopsetup.Lock(home, "workbuddy")
	if err != nil {
		return err
	}
	defer unlock()
	if err := configureLoginEnvironment(bounded, home, port, enabled); err != nil {
		return err
	}
	return updateStartupEnvironment(home, port, enabled, launchEnvironment{bounded})
}

var startupUserHome = os.UserHomeDir
var startupLoginRun = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
}

func loginEnvironmentPaths(home string) (string, string, error) {
	user, err := startupUserHome()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(home))
	label := fmt.Sprintf("com.lazymind.workbuddy-startup.%x", sum[:6])
	return filepath.Join(user, "Library", "LaunchAgents", label+".plist"), label, nil
}
func configureLoginEnvironment(ctx context.Context, home string, port int, enabled bool) error {
	path, label, err := loginEnvironmentPaths(home)
	if err != nil {
		return err
	}
	files, err := desktopsetup.Load(filepath.Join(home, "workbuddy-workflow", "login-files.json"))
	if err != nil {
		return err
	}
	if !enabled {
		if len(files.Entries) == 0 {
			return nil
		}
		service := "gui/" + strconv.Itoa(os.Getuid()) + "/" + label
		if _, err := startupLoginRun(ctx, "print", service); err == nil {
			if output, err := startupLoginRun(ctx, "bootout", service); err != nil {
				return fmt.Errorf("remove WorkBuddy login configuration: %s: %w", output, err)
			}
		}
		return files.Restore()
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid WorkBuddy startup port")
	}
	if _, err := os.Stat(path); err == nil && len(files.Entries) == 0 {
		return fmt.Errorf("WorkBuddy login configuration already exists outside this installation")
	}
	// Independent of LazyMind/account startup. The persisted choice also prevents
	// re-enabling after a partially failed disconnect. No desktop is launched here.
	choice := filepath.Join(home, "workbuddy-workflow", "enabled.json")
	script := "if [ -f " + desktopsetup.ShellQuote(choice) + " ]; then [ \"$(cat " + desktopsetup.ShellQuote(choice) + ")\" = true ] || exit 0; fi; exec /bin/launchctl setenv " + desktopDebugPortEnv + " " + strconv.Itoa(port)
	var escaped strings.Builder
	_ = xml.EscapeText(&escaped, []byte(script))
	body := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + label + `</string><key>ProgramArguments</key><array><string>/bin/sh</string><string>-c</string><string>` + escaped.String() + `</string></array><key>RunAtLoad</key><true/></dict></plist>`
	return files.Put(path, []byte(body), 0600)
}

func checkDesktopStartup(ctx context.Context, home string, port int) error {
	files, err := desktopsetup.Load(filepath.Join(home, "workbuddy-workflow", "login-files.json"))
	if err != nil {
		return err
	}
	if err = files.Check(); err != nil {
		return err
	}
	current, err := (launchEnvironment{ctx}).Get()
	if err != nil {
		return err
	}
	if current != strconv.Itoa(port) {
		return fmt.Errorf("WorkBuddy startup environment is unavailable")
	}
	return nil
}
