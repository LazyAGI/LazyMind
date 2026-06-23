package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type RuntimeManager struct {
	runner         CommandRunner
	execPath       string
	now            func() time.Time
	out            io.Writer
	errOut         io.Writer
	probeAPI       func(port int, timeout time.Duration) bool
	pollInterval   time.Duration
	upTimeout      time.Duration
	downTimeout    time.Duration
	compose        *ComposeManager
	processCompose *ProcessComposeManager
	localProxy     *LocalProxyManager
}

func NewRuntimeManager(r CommandRunner, execPath string) *RuntimeManager {
	processCompose := NewProcessComposeManager(r, execPath)
	return &RuntimeManager{
		runner:         r,
		execPath:       execPath,
		now:            time.Now,
		out:            io.Discard,
		errOut:         io.Discard,
		probeAPI:       processCompose.ProbeAPI,
		pollInterval:   2 * time.Second,
		upTimeout:      envDuration(localUpTimeoutEnvVar, time.Duration(defaultLocalUpTimeout)*time.Second),
		downTimeout:    envDuration(localDownTimeoutEnvVar, time.Duration(defaultLocalDownTimeout)*time.Second),
		compose:        NewComposeManager(r),
		processCompose: processCompose,
		localProxy:     NewLocalProxyManager(r),
	}
}

func (m *RuntimeManager) SetOutput(out, errOut io.Writer) {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	m.out = out
	m.errOut = errOut
}

func randomHexToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (m *RuntimeManager) Up(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	state, err := readOrNewState(paths, cfg)
	if err != nil {
		return err
	}
	if m.isExistingRuntimeRunning(ctx, state, paths) {
		return m.reportExistingRuntime(ctx, state, paths)
	}

	releaseLock, err := acquireUpLock(paths)
	if err != nil {
		return err
	}
	defer releaseLock()

	state, err = readOrNewState(paths, cfg)
	if err != nil {
		return err
	}
	if m.isExistingRuntimeRunning(ctx, state, paths) {
		return m.reportExistingRuntime(ctx, state, paths)
	}

	token, err := randomHexToken()
	if err != nil {
		return err
	}
	if err := os.WriteFile(paths.RunDirTokenFile, []byte(token), 0o600); err != nil {
		return err
	}

	generatedFile, err := os.Create(paths.GeneratedConfig)
	if err != nil {
		return err
	}
	if err := m.processCompose.WriteGeneratedConfig(generatedFile, paths.RepoRoot, cfg.Profile, paths.LogFilePath, paths.LocalProxyLog, paths.RunDirTokenFile, cfg.ProcessComposePort); err != nil {
		_ = generatedFile.Close()
		return err
	}
	if err := generatedFile.Close(); err != nil {
		return err
	}

	state.Profile = cfg.Profile
	state.RepoRoot = cfg.RepoRoot
	state.RuntimeRoot = cfg.RuntimeRoot
	state.ProcessCompose.APIPort = cfg.ProcessComposePort
	state.ProcessCompose.APIRoot = "http://127.0.0.1:" + strconv.Itoa(cfg.ProcessComposePort)
	state.ProcessCompose.TokenFile = paths.RunDirTokenFile
	state = newStateWithServiceStatus(state, "starting")
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		return err
	}

	if err := m.processCompose.Up(ctx, cfg, paths); err != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return err
	}

	if !m.waitForProcessComposeAPI(ctx, cfg.ProcessComposePort, 15*time.Second) {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return fmt.Errorf("process-compose API did not become ready on port %d", cfg.ProcessComposePort)
	}

	logCtx, stopLogs := context.WithCancel(ctx)
	logErrCh := make(chan error, 1)
	go func() {
		logErrCh <- m.processCompose.FollowLogs(logCtx, cfg, paths, m.out, m.errOut)
	}()

	waitErr := m.waitForComposeTerminalState(ctx, cfg, paths)
	stopLogs()
	select {
	case logErr := <-logErrCh:
		if logErr != nil && waitErr == nil {
			waitErr = logErr
		}
	case <-time.After(2 * time.Second):
	}
	if waitErr != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		if ps, psErr := m.compose.ComposePS(context.Background(), paths.RepoRoot); psErr == nil && strings.TrimSpace(ps) != "" {
			_, _ = io.WriteString(m.errOut, ps)
			if !strings.HasSuffix(ps, "\n") {
				_, _ = io.WriteString(m.errOut, "\n")
			}
		}
		return waitErr
	}

	state = newStateWithServiceStatus(state, "running")
	state.OverallStatus = "ready"
	state.UpdatedAt = m.now().UTC().Format(time.RFC3339)
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		return err
	}
	m.printReadySummary(cfg)
	return nil
}

func (m *RuntimeManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	state, err := readOrNewState(paths, cfg)
	if err != nil {
		return err
	}
	if state.ProcessCompose.APIPort > 0 {
		cfg.ProcessComposePort = state.ProcessCompose.APIPort
	}
	var downErr error
	apiAlive := m.probeAPI(cfg.ProcessComposePort, 500*time.Millisecond)
	if apiAlive {
		downErr = m.processCompose.Down(ctx, cfg, paths)
	}
	if downErr != nil || !apiAlive {
		if fallbackErr := m.compose.ComposeDown(ctx, paths.RepoRoot, cfg.Profile); fallbackErr != nil {
			state = newStateWithServiceStatus(state, "failed")
			state.OverallStatus = "failed"
			_ = writeRuntimeState(paths.StateFile, state)
			if downErr != nil {
				return fmt.Errorf("process-compose down failed: %w; docker compose down fallback failed: %v", downErr, fallbackErr)
			}
			return fallbackErr
		}
	}
	if err := m.waitForRuntimeStopped(ctx, cfg, paths); err != nil {
		if ps, psErr := m.compose.ComposePS(context.Background(), paths.RepoRoot); psErr == nil && strings.TrimSpace(ps) != "" {
			_, _ = io.WriteString(m.errOut, ps)
			if !strings.HasSuffix(ps, "\n") {
				_, _ = io.WriteString(m.errOut, "\n")
			}
		}
		return err
	}
	state = newStateWithServiceStatus(state, "stopped")
	state.OverallStatus = "stopped"
	state.UpdatedAt = m.now().UTC().Format(time.RFC3339)
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		return err
	}
	_, _ = io.WriteString(m.out, "local runtime stopped\n")
	return nil
}

func (m *RuntimeManager) isExistingRuntimeRunning(ctx context.Context, state RuntimeState, paths RuntimePaths) bool {
	_ = ctx
	_ = paths
	svc := state.Services[processComposeServiceName]
	claimsRunning := state.OverallStatus == "ready" || state.OverallStatus == "running" || state.OverallStatus == "starting" ||
		svc.Status == "running" || svc.Status == "starting"
	return claimsRunning && state.ProcessCompose.APIPort > 0 && m.probeAPI(state.ProcessCompose.APIPort, 500*time.Millisecond)
}

func (m *RuntimeManager) reportExistingRuntime(ctx context.Context, state RuntimeState, paths RuntimePaths) error {
	state = newStateWithServiceStatus(state, "running")
	state.OverallStatus = "ready"
	state.UpdatedAt = m.now().UTC().Format(time.RFC3339)
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(m.out, "local runtime already running\nprocess-compose: %s\n", state.ProcessCompose.APIRoot)
	if ps, err := m.compose.ComposePS(ctx, paths.RepoRoot); err == nil && strings.TrimSpace(ps) != "" {
		_, _ = io.WriteString(m.out, ps)
		if !strings.HasSuffix(ps, "\n") {
			_, _ = io.WriteString(m.out, "\n")
		}
	}
	return nil
}

func (m *RuntimeManager) waitForProcessComposeAPI(ctx context.Context, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if m.probeAPI(port, 500*time.Millisecond) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (m *RuntimeManager) waitForComposeTerminalState(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	timeout := m.upTimeout
	if timeout <= 0 {
		timeout = time.Duration(defaultLocalUpTimeout) * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	var lastReason string
	var lastReport time.Time
	for {
		statuses, err := m.compose.ComposeStatus(ctx, paths.RepoRoot)
		if err != nil {
			lastReason = err.Error()
		} else {
			state, reason := classifyComposeReadiness(statuses)
			lastReason = reason
			switch state {
			case composeReadinessReady:
				return nil
			case composeReadinessFailed:
				return fmt.Errorf("compose startup failed: %s", reason)
			}
		}
		if !m.probeAPI(cfg.ProcessComposePort, 500*time.Millisecond) {
			return fmt.Errorf("process-compose API stopped before compose services became ready: %s", lastReason)
		}
		if lastReport.IsZero() || time.Since(lastReport) >= 15*time.Second {
			_, _ = fmt.Fprintf(m.errOut, "waiting for compose services: %s\n", lastReason)
			lastReport = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out after %s waiting for compose services: %s", timeout, lastReason)
		case <-ticker.C:
		}
	}
}

func (m *RuntimeManager) waitForRuntimeStopped(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	timeout := m.downTimeout
	if timeout <= 0 {
		timeout = time.Duration(defaultLocalDownTimeout) * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		apiAlive := cfg.ProcessComposePort > 0 && m.probeAPI(cfg.ProcessComposePort, 500*time.Millisecond)
		hasContainers, err := m.compose.ComposeHasContainers(ctx, paths.RepoRoot)
		if err == nil && !apiAlive && !hasContainers {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if err != nil {
				return fmt.Errorf("timed out after %s waiting for local runtime to stop: %w", timeout, err)
			}
			return fmt.Errorf("timed out after %s waiting for local runtime to stop", timeout)
		case <-ticker.C:
		}
	}
}

func (m *RuntimeManager) printReadySummary(cfg RuntimeConfig) {
	_, _ = fmt.Fprintf(m.out, "local runtime ready\n")
	_, _ = fmt.Fprintf(m.out, "process-compose: http://127.0.0.1:%d\n", cfg.ProcessComposePort)
	_, _ = fmt.Fprintf(m.out, "frontend: http://localhost:%d\n", cfg.FrontendPort)
	_, _ = fmt.Fprintf(m.out, "status: local/local-runtime-manager/lazymind-local status --json --profile %s\n", cfg.Profile)
}

func acquireUpLock(paths RuntimePaths) (func(), error) {
	for {
		f, err := os.OpenFile(paths.UpLockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if os.IsExist(err) {
				alive, readErr := upLockProcessAlive(paths.UpLockFile)
				if readErr != nil || alive {
					return nil, fmt.Errorf("local runtime startup is already in progress (lock: %s)", paths.UpLockFile)
				}
				_ = os.Remove(paths.UpLockFile)
				continue
			}
			return nil, err
		}

		released := false
		release := func() {
			if released {
				return
			}
			released = true
			_ = f.Close()
			_ = os.Remove(paths.UpLockFile)
		}
		if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
			release()
			return nil, err
		}
		if err := f.Close(); err != nil {
			release()
			return nil, err
		}
		return func() {
			if released {
				return
			}
			released = true
			_ = os.Remove(paths.UpLockFile)
		}, nil
	}
}

func upLockProcessAlive(lockFile string) (bool, error) {
	raw, err := os.ReadFile(lockFile)
	if err != nil {
		return false, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return false, nil
	}
	err = syscall.Kill(pid, 0)
	if err == nil || err == syscall.EPERM {
		return true, nil
	}
	if err == syscall.ESRCH {
		return false, nil
	}
	return true, err
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func (m *RuntimeManager) Status(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, asJSON bool) (string, error) {
	_ = ctx
	state, err := readOrNewState(paths, cfg)
	if err != nil {
		return "", err
	}
	if state.Profile == "" {
		state.Profile = cfg.Profile
	}
	if state.RepoRoot == "" {
		state.RepoRoot = cfg.RepoRoot
	}
	if state.RuntimeRoot == "" {
		state.RuntimeRoot = cfg.RuntimeRoot
	}

	resp := StatusResponse{
		Runtime:        "local",
		Profile:        state.Profile,
		OverallStatus:  state.OverallStatus,
		RepoRoot:       state.RepoRoot,
		RuntimeRoot:    state.RuntimeRoot,
		ProcessCompose: state.ProcessCompose,
		Services:       state.Services,
	}
	if resp.Services == nil {
		resp.Services = map[string]RuntimeServiceState{}
	}
	if _, ok := resp.Services[processComposeServiceName]; !ok {
		resp.Services[processComposeServiceName] = RuntimeServiceState{
			Kind:   "docker-compose",
			Status: "unknown",
		}
	}

	if m.probeAPI(state.ProcessCompose.APIPort, 500*time.Millisecond) {
		resp.OverallStatus = "ready"
		s := resp.Services[processComposeServiceName]
		s.Status = "running"
		resp.Services[processComposeServiceName] = s
	} else {
		if resp.OverallStatus == "ready" || resp.OverallStatus == "running" || resp.OverallStatus == "starting" {
			resp.OverallStatus = "stale"
		} else if resp.OverallStatus == "" {
			resp.OverallStatus = "stopped"
		}
		s := resp.Services[processComposeServiceName]
		if s.Status == "running" || s.Status == "starting" {
			s.Status = "stale"
		} else if s.Status == "" || s.Status == "unknown" {
			s.Status = "stopped"
		}
		resp.Services[processComposeServiceName] = s
	}

	if !asJSON {
		return m.humanStatus(resp), nil
	}
	b, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (m *RuntimeManager) humanStatus(resp StatusResponse) string {
	lines := []string{
		fmt.Sprintf("runtime: %s", resp.Runtime),
		fmt.Sprintf("profile: %s", resp.Profile),
		fmt.Sprintf("overallStatus: %s", resp.OverallStatus),
		fmt.Sprintf("repoRoot: %s", resp.RepoRoot),
		fmt.Sprintf("runtimeRoot: %s", resp.RuntimeRoot),
	}
	for name, svc := range resp.Services {
		lines = append(lines, fmt.Sprintf("%s.kind=%s status=%s", name, svc.Kind, svc.Status))
	}
	return strings.Join(lines, "\n") + "\n"
}
