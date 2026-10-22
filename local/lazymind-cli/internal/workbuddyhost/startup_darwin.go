package workbuddyhost

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
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
	return updateStartupEnvironment(home, port, enabled, launchEnvironment{bounded})
}
