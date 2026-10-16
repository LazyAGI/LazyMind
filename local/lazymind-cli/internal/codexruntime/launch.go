package codexruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// Resolve from the actual desktop process, not PATH or an obsolete package path.
// The Node runtime is deliberately not required to recover native CLI startup.
func windowsCLIForDesktop(desktop string) (string, error) {
	if !filepath.IsAbs(desktop) {
		return "", errors.New("absolute desktop path required")
	}
	app := filepath.Dir(desktop)
	selected, err := windowsDesktopExecutable(app)
	if err != nil || !strings.EqualFold(selected, desktop) {
		return "", errors.New("desktop does not match the installation entrypoint")
	}
	var candidates []string
	for _, relative := range []string{"resources/codex.exe", "resources/codex-cli/codex.exe", "resources/codex-cli/bin/codex.exe"} {
		file := filepath.Join(app, filepath.FromSlash(relative))
		if info, err := os.Lstat(file); err == nil && info.Mode().IsRegular() {
			candidates = append(candidates, file)
		}
	}
	if len(candidates) != 1 {
		return "", errors.New("expected one original bundled CLI")
	}
	return candidates[0], nil
}

func launcherDesktop(pid int, process func(int) (*ProcessInfo, error)) string {
	for depth := 0; pid > 0 && depth < 8; depth++ {
		info, err := process(pid)
		if err != nil || info == nil {
			break
		}
		if _, err := windowsCLIForDesktop(info.Executable); err == nil {
			return info.Executable
		}
		if info.PPID == pid {
			break
		}
		pid = info.PPID
	}
	return ""
}

func isAppServer(args []string) bool {
	found := false
	for _, arg := range args {
		switch arg {
		case "generate-json-schema", "generate-ts", "daemon", "proxy":
			return false
		case "app-server":
			found = true
		}
	}
	return found
}

func runtimeFilesValid(c *runtimeConfig) bool {
	files := []string{c.CLI, c.Node, c.DesktopExecutable, c.Helper, filepath.Join(c.BundleDir, "adapter.mjs")}
	for _, plugin := range c.Plugins {
		command, _ := plugin["command"].(string)
		files = append(files, command)
		if cwd, ok := plugin["cwd"].(string); ok {
			if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
				return false
			}
		}
		if args, ok := plugin["args"].([]any); ok && len(args) > 0 {
			if entry, ok := args[0].(string); ok && filepath.IsAbs(entry) {
				files = append(files, entry)
			}
		}
	}
	for _, file := range files {
		if !filepath.IsAbs(file) {
			return false
		}
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func sharedLaunchValid(c *runtimeConfig, desktop string, args []string) bool {
	return isAppServer(args) && strings.EqualFold(c.DesktopExecutable, desktop) && runtimeFilesValid(c)
}

func validateLaunchVersion(ctx context.Context, c *runtimeConfig) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.CLI, "--version")
	cmd.Env = officialCLIEnvironment(os.Environ(), c.CLI)
	output, err := cmd.Output()
	if err != nil || c.CLIVersion == "" || strings.TrimPrefix(strings.TrimSpace(string(output)), "codex-cli ") != c.CLIVersion {
		return errors.New("Codex CLI version changed; reconfigure shared mode")
	}
	return nil
}

func officialCLIEnvironment(env []string, cli string) []string {
	var clean []string
	for _, value := range env {
		key, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(key) {
		case "CODEX_CLI_PATH", "CODEX_APP_SERVER_FORCE_CLI", "CODEX_APP_SERVER_WS_URL", "CODEX_APP_SERVER_USE_LOCAL_DAEMON", "LAZYMIND_CODEX_DESKTOP_CONFIG", "LAZYMIND_RUNTIME_HELPER":
			continue
		}
		clean = append(clean, value)
	}
	return append(clean, "CODEX_CLI_PATH="+cli)
}

// Emergency recovery needs neither Codex nor Node. Preserve records so the
// normal disconnect flow can finish restoring generated plugin paths later.
func restoreWindowsStartup(file string) error {
	return restoreStartupSnapshot(file, platformEnvironment)
}

func restoreStartupSnapshot(file string, environment func(map[string]*string, map[string]uint32) (*environmentSnapshot, error)) error {
	stateFile := filepath.Join(filepath.Dir(file), "activation.json")
	if !filepath.IsAbs(file) {
		return errors.New("absolute runtime config required")
	}
	if err := checkPrivate(stateFile); err != nil {
		return err
	}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return err
	}
	var state struct {
		Config    string              `json:"config"`
		Before    environmentSnapshot `json:"before"`
		Disabling bool                `json:"disabling"`
	}
	if json.Unmarshal(data, &state) != nil || state.Config != file || len(state.Before.Values) != 4 {
		return errors.New("invalid activation snapshot")
	}
	current, err := environment(nil, nil)
	if err != nil {
		return err
	}
	launcher, force := filepath.Join(filepath.Dir(file), "launch-adapter.exe"), "1"
	desired := map[string]*string{"CODEX_CLI_PATH": &launcher, "CODEX_APP_SERVER_FORCE_CLI": &force, "CODEX_APP_SERVER_WS_URL": nil, "CODEX_APP_SERVER_USE_LOCAL_DAEMON": nil}
	for key, value := range desired {
		before, ok := state.Before.Values[key]
		if !ok {
			return errors.New("incomplete activation snapshot")
		}
		isBefore := reflect.DeepEqual(current.Values[key], before) && (before == nil || current.Types[key] == state.Before.Types[key])
		isDesired := reflect.DeepEqual(current.Values[key], value) && (value == nil || current.Types[key] == 1)
		if !isBefore && !isDesired {
			return errors.New("startup environment changed externally; refusing overwrite")
		}
	}
	state.Disabling = true
	if err := writePrivateJSON(stateFile, state); err != nil {
		return err
	}
	_, err = environment(state.Before.Values, state.Before.Types)
	return err
}

func windowsLaunchCommand(ctx context.Context, file string, args []string, desktop string) (*exec.Cmd, error) {
	config, configErr := readRuntime(file)
	// Find the current application's CLI before touching a possibly removed Node.
	if desktop == "" && configErr == nil {
		desktop = config.DesktopExecutable
	}
	cli, err := windowsCLIForDesktop(desktop)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve original Codex CLI; disconnect LazyMind shared mode: %w", err)
	}
	shared := configErr == nil && !startupRestoring(file) && sharedLaunchValid(config, desktop, args)
	if shared {
		if err := validateLaunchVersion(ctx, config); err != nil {
			shared = false
		}
	}
	if !shared {
		// Only a pre-start choice: never retry/replay a failed shared conversation.
		if configErr == nil && isAppServer(args) {
			_ = writePrivateJSON(filepath.Join(config.RuntimeDir, "startup-invalid.json"), map[string]string{"reason": "reconfigure_required"})
		}
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Env = officialCLIEnvironment(os.Environ(), cli)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd, nil
	}
	_ = os.Remove(filepath.Join(config.RuntimeDir, "startup-invalid.json"))
	cmd := exec.CommandContext(ctx, config.Node, append([]string{filepath.Join(config.BundleDir, "adapter.mjs")}, args...)...)
	cmd.Env = append(os.Environ(), "LAZYMIND_CODEX_DESKTOP_CONFIG="+file, "LAZYMIND_RUNTIME_HELPER="+config.Helper)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}
