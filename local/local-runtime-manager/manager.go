package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
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
	probeAuth      func(port int, timeout time.Duration) bool
	probeCore      func(port int, timeout time.Duration) bool
	waitHostReady  func(context.Context, RuntimeConfig) error
	runtimeReady   func(context.Context, RuntimeConfig, RuntimePaths) bool
	pollInterval   time.Duration
	upTimeout      time.Duration
	downTimeout    time.Duration
	compose        *ComposeManager
	processCompose *ProcessComposeManager
	localProxy     *LocalProxyManager
	authService    *AuthServiceManager
	coreService    *CoreServiceManager
	frontend       *FrontendManager
	algorithm      *AlgorithmServiceManager
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
		probeAuth:      authServiceHealthAlive,
		probeCore:      coreServiceHealthAlive,
		waitHostReady:  waitForHostAlgorithmReadiness,
		runtimeReady:   nil,
		pollInterval:   2 * time.Second,
		upTimeout:      envDuration(localUpTimeoutEnvVar, time.Duration(defaultLocalUpTimeout)*time.Second),
		downTimeout:    envDuration(localDownTimeoutEnvVar, time.Duration(defaultLocalDownTimeout)*time.Second),
		compose:        NewComposeManager(r),
		processCompose: processCompose,
		localProxy:     NewLocalProxyManager(r),
		authService:    NewAuthServiceManager(r),
		coreService:    NewCoreServiceManager(r),
		frontend:       NewFrontendManager(r),
		algorithm:      NewAlgorithmServiceManager(r),
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
	freshCfg := cfg
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := writeServiceEndpointFiles(paths, serviceEndpointsFromConfig(cfg)); err != nil {
		return err
	}
	if err := ensureComposeBindPermissions(paths.RepoRoot); err != nil {
		return err
	}
	state, err := readOrNewState(paths, cfg)
	if err != nil {
		return err
	}
	stateCfg := applyStateConfig(freshCfg, state)
	if m.isExistingRuntimeRunning(ctx, state, stateCfg, paths) {
		return m.reportExistingRuntime(ctx, state, paths)
	}
	if err := m.stopStaleRuntimeIfNeeded(ctx, state, stateCfg, paths); err != nil {
		return err
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
	stateCfg = applyStateConfig(freshCfg, state)
	if m.isExistingRuntimeRunning(ctx, state, stateCfg, paths) {
		return m.reportExistingRuntime(ctx, state, paths)
	}
	if err := m.stopStaleRuntimeIfNeeded(ctx, state, stateCfg, paths); err != nil {
		return err
	}
	cfg = freshCfg

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
	if err := m.processCompose.WriteGeneratedConfig(generatedFile, paths.RepoRoot, cfg.Profile, paths, cfg, paths.RunDirTokenFile, cfg.ProcessComposePort); err != nil {
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
	state.Config = snapshotRuntimeConfig(cfg)
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
	if err := m.waitForAuthServiceHealthy(ctx, cfg.AuthService.Port, m.upTimeout, paths.AuthServicePIDFile); err != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return err
	}
	if err := m.waitForCoreHealthy(ctx, cfg.LocalProxy.CoreHostPort, m.upTimeout); err != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return err
	}
	if waitErr := m.waitHostReady(ctx, cfg); waitErr != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
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

func (m *RuntimeManager) waitForAuthServiceHealthy(ctx context.Context, port int, timeout time.Duration, pidFile string) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	sawPIDFile := false
	for {
		if m.probeAuth(port, time.Second) {
			return nil
		}
		alive, err := upLockProcessAlive(pidFile)
		if err == nil {
			sawPIDFile = true
			if !alive {
				return fmt.Errorf("auth-service process exited before becoming healthy")
			}
		} else if sawPIDFile && os.IsNotExist(err) {
			return fmt.Errorf("auth-service process exited before becoming healthy")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("auth-service health check timed out on port %d", port)
		case <-ticker.C:
		}
	}
}

func (m *RuntimeManager) waitForCoreHealthy(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if m.probeCore(port, time.Second) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("core health check timed out on port %d", port)
		case <-ticker.C:
		}
	}
}

func (m *RuntimeManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	state, err := readOrNewState(paths, cfg)
	if err != nil {
		return err
	}
	cfg = applyStateConfig(cfg, state)
	if state.ProcessCompose.APIPort > 0 {
		cfg.ProcessComposePort = state.ProcessCompose.APIPort
	}
	var downErr error
	apiAlive := m.probeAPI(cfg.ProcessComposePort, 500*time.Millisecond)
	if apiAlive {
		downCtx, cancel := context.WithTimeout(ctx, m.downTimeout)
		defer cancel()
		downErr = m.processCompose.Down(downCtx, cfg, paths)
	}
	if downErr != nil || !apiAlive {
		if downErr != nil {
			_ = m.killStaleRuntimeProcesses(context.Background(), paths.RepoRoot)
		}
		if err := m.frontend.Down(ctx, cfg, paths); err != nil && downErr == nil {
			downErr = err
		}
		if err := m.localProxy.Down(ctx, cfg, paths); err != nil && downErr == nil {
			downErr = err
		}
		if fallbackErr := m.compose.ComposeDown(ctx, paths.RepoRoot, cfg.Profile); fallbackErr != nil {
			state = newStateWithServiceStatus(state, "failed")
			state.OverallStatus = "failed"
			_ = writeRuntimeState(paths.StateFile, state)
			if downErr != nil {
				return fmt.Errorf("process-compose down failed: %w; docker compose down fallback failed: %v", downErr, fallbackErr)
			}
			return fallbackErr
		}
		downErr = nil
	}
	for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
		if err := m.algorithm.Down(ctx, paths, spec.Name); err != nil && downErr == nil {
			downErr = err
		}
	}
	if err := m.coreService.Down(ctx, cfg, paths); err != nil && downErr == nil {
		downErr = err
	}
	if downErr != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return downErr
	}
	if err := m.authService.Down(ctx, cfg, paths); err != nil {
		return err
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

func (m *RuntimeManager) isExistingRuntimeRunning(ctx context.Context, state RuntimeState, cfg RuntimeConfig, paths RuntimePaths) bool {
	return claimsRuntimeRunning(state) && state.ProcessCompose.APIPort > 0 &&
		m.probeAPI(state.ProcessCompose.APIPort, 500*time.Millisecond) &&
		m.checkRuntimeReady(ctx, cfg, paths)
}

func (m *RuntimeManager) stopStaleRuntimeIfNeeded(ctx context.Context, state RuntimeState, cfg RuntimeConfig, paths RuntimePaths) error {
	if !claimsRuntimeRunning(state) {
		return nil
	}
	if state.ProcessCompose.APIPort <= 0 || !m.probeAPI(state.ProcessCompose.APIPort, 500*time.Millisecond) {
		return nil
	}
	if m.checkRuntimeReady(ctx, cfg, paths) {
		return nil
	}
	staleCfg := cfg
	staleCfg.ProcessComposePort = state.ProcessCompose.APIPort
	downCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := m.processCompose.Down(downCtx, staleCfg, paths)
	if err != nil {
		_ = m.killStaleRuntimeProcesses(context.Background(), paths.RepoRoot)
	}
	return nil
}

func (m *RuntimeManager) killStaleRuntimeProcesses(ctx context.Context, repoRoot string) error {
	pattern := regexp.QuoteMeta(repoRoot) + "/(local/bin/process-compose|\\.lazymind-local/bin/local-proxy|\\.lazymind-local/python/\\.venv/bin/python|\\.lazymind-local/venvs/auth-service/bin/python|local/local-runtime-manager/lazymind-local internal)"
	_, err := m.runner.Run(ctx, Command{Name: "pkill", Args: []string{"-f", pattern}, Dir: repoRoot})
	if err != nil {
		return nil
	}
	time.Sleep(time.Second)
	return nil
}

func (m *RuntimeManager) checkRuntimeReady(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) bool {
	if m.runtimeReady != nil {
		return m.runtimeReady(ctx, cfg, paths)
	}
	statuses, err := m.compose.ComposeStatus(ctx, paths.RepoRoot)
	if err != nil {
		return false
	}
	state, _ := classifyComposeReadiness(statuses)
	if state != composeReadinessReady {
		return false
	}
	if !httpOK(ctx, fmt.Sprintf("http://127.0.0.1:%d/_local/healthz", cfg.LocalProxy.Port), 500*time.Millisecond) {
		return false
	}
	if !httpOK(ctx, fmt.Sprintf("http://127.0.0.1:%d/", cfg.FrontendPort), 500*time.Millisecond) {
		return false
	}
	if !m.probeAuth(cfg.AuthService.Port, 500*time.Millisecond) {
		return false
	}
	if !m.probeCore(cfg.LocalProxy.CoreHostPort, 500*time.Millisecond) {
		return false
	}
	for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
		if !httpOK(ctx, fmt.Sprintf("http://127.0.0.1:%d%s", spec.Port, spec.HealthPath), 500*time.Millisecond) {
			return false
		}
	}
	return true
}

func claimsRuntimeRunning(state RuntimeState) bool {
	svc := state.Services[processComposeServiceName]
	return state.OverallStatus == "ready" || state.OverallStatus == "running" || state.OverallStatus == "starting" ||
		svc.Status == "running" || svc.Status == "starting"
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
		authAlive := false
		if _, statErr := os.Stat(paths.AuthServicePIDFile); statErr == nil && cfg.AuthService.Port > 0 {
			authAlive = m.probeAuth(cfg.AuthService.Port, 500*time.Millisecond)
		}
		if err == nil && !apiAlive && !hasContainers && !authAlive {
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
	cfg = applyStateConfig(cfg, state)
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
	if _, ok := resp.Services[localProxyProcessName]; !ok {
		resp.Services[localProxyProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	}
	if _, ok := resp.Services[authServiceProcessName]; !ok {
		resp.Services[authServiceProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	}
	if _, ok := resp.Services[frontendProcessName]; !ok {
		resp.Services[frontendProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	}
	if _, ok := resp.Services[coreProcessName]; !ok {
		resp.Services[coreProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	}
	for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
		if _, ok := resp.Services[spec.Name]; !ok {
			resp.Services[spec.Name] = RuntimeServiceState{
				Kind:   "host-process",
				Status: "unknown",
			}
		}
	}

	if m.probeAPI(state.ProcessCompose.APIPort, 500*time.Millisecond) {
		resp.OverallStatus = "ready"
		s := resp.Services[processComposeServiceName]
		s.Status = "running"
		resp.Services[processComposeServiceName] = s
		hostHealthy := true
		lp := resp.Services[localProxyProcessName]
		lp.Kind = "host-process"
		if httpOK(ctx, fmt.Sprintf("http://127.0.0.1:%d/_local/healthz", cfg.LocalProxy.Port), 500*time.Millisecond) {
			lp.Status = "running"
		} else {
			hostHealthy = false
		}
		resp.Services[localProxyProcessName] = lp
		auth := resp.Services[authServiceProcessName]
		auth.Kind = "host-process"
		if m.probeAuth(cfg.AuthService.Port, 500*time.Millisecond) {
			auth.Status = "running"
		} else {
			hostHealthy = false
		}
		resp.Services[authServiceProcessName] = auth
		frontend := resp.Services[frontendProcessName]
		frontend.Kind = "host-process"
		if httpOK(ctx, fmt.Sprintf("http://127.0.0.1:%d/", cfg.FrontendPort), 500*time.Millisecond) {
			frontend.Status = "running"
		} else {
			hostHealthy = false
		}
		resp.Services[frontendProcessName] = frontend
		core := resp.Services[coreProcessName]
		core.Kind = "host-process"
		if m.probeCore(cfg.LocalProxy.CoreHostPort, 500*time.Millisecond) {
			core.Status = "running"
		} else {
			hostHealthy = false
		}
		resp.Services[coreProcessName] = core
		for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
			svc := resp.Services[spec.Name]
			svc.Kind = "host-process"
			if httpOK(ctx, fmt.Sprintf("http://127.0.0.1:%d%s", spec.Port, spec.HealthPath), 500*time.Millisecond) {
				svc.Status = "running"
			} else if svc.Status == "running" || svc.Status == "starting" {
				svc.Status = "stale"
				hostHealthy = false
			} else if svc.Status == "" || svc.Status == "unknown" {
				svc.Status = "stopped"
				hostHealthy = false
			}
			resp.Services[spec.Name] = svc
		}
		if !hostHealthy {
			resp.OverallStatus = "stale"
		}
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
