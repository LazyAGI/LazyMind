package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ProcessComposeManager struct {
	runner   CommandRunner
	execPath string
}

func NewProcessComposeManager(r CommandRunner, execPath string) *ProcessComposeManager {
	return &ProcessComposeManager{runner: r, execPath: execPath}
}

func (m *ProcessComposeManager) WriteGeneratedConfig(w io.Writer, repoRoot string, profile string, logPath string, tokenPath string, apiPort int) error {
	commandForComposeUp := quoteShellArg(m.execPath) + " internal compose-up --profile " + profile
	commandForComposeDown := quoteShellArg(m.execPath) + " internal compose-down --profile " + profile
	commandForComposeReady := quoteShellArg(m.execPath) + " internal compose-ready --profile " + profile

	_, err := fmt.Fprintf(w, "version: \"0.5\"\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "is_strict: true\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "ordered_shutdown: true\n\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "processes:\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "  %s:\n", processComposeServiceName)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "    working_dir: %s\n", quoteYAMLString(repoRoot))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "    command: %s\n", quoteYAMLString(commandForComposeUp))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "    shutdown:\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "      command: %s\n", quoteYAMLString(commandForComposeDown))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "      timeout_seconds: 60\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "    readiness_probe:\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "      exec:\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "        command: %s\n", quoteYAMLString(commandForComposeReady))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "      initial_delay_seconds: 5\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "      period_seconds: 5\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "      timeout_seconds: 30\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "      success_threshold: 1\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "    log_location: %s\n", quoteYAMLString(logPath))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "    namespace: container\n")
	if err != nil {
		return err
	}
	_ = tokenPath
	_ = apiPort
	return err
}

func (m *ProcessComposeManager) Up(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	args := []string{
		"--config", filepath.ToSlash(paths.GeneratedConfig),
		"-D",
		"-t=false",
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"--ordered-shutdown",
		"up",
	}
	res, err := m.runner.Run(ctx, Command{Name: processComposeCommand(paths.RepoRoot), Args: args, Dir: paths.RepoRoot})
	if err != nil {
		return fmt.Errorf("process-compose up failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ProcessComposeManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	args := []string{"--config", filepath.ToSlash(paths.GeneratedConfig)}
	args = append(args,
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"down",
	)
	res, err := m.runner.Run(ctx, Command{Name: processComposeCommand(paths.RepoRoot), Args: args, Dir: paths.RepoRoot})
	if err != nil {
		return fmt.Errorf("process-compose down failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ProcessComposeManager) ConfigDryRun(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	args := []string{
		"--config", filepath.ToSlash(paths.GeneratedConfig),
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"--dry-run",
		"up",
	}
	res, err := m.runner.Run(ctx, Command{Name: processComposeCommand(paths.RepoRoot), Args: args, Dir: paths.RepoRoot})
	if err != nil {
		return fmt.Errorf("process-compose dry-run failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ProcessComposeManager) ProbeAPI(port int, timeout time.Duration) bool {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/api/v1/processes"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	_ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req = req.WithContext(_ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

func quoteShellArg(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n'
	}) == -1 {
		return value
	}
	return strconv.Quote(value)
}

func quoteYAMLString(value string) string {
	return strconv.Quote(value)
}

func processComposeCommand(repoRoot string) string {
	candidate := filepath.Join(repoRoot, localProcessComposeBin)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return "process-compose"
}
