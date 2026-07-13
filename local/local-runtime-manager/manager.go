package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type RuntimeManager struct {
	runner                    CommandRunner
	execPath                  string
	now                       func() time.Time
	out                       io.Writer
	errOut                    io.Writer
	probeAPI                  func(port int, timeout time.Duration) bool
	probeLocalProxy           func(port int, timeout time.Duration) bool
	probeFrontend             func(port int, timeout time.Duration) bool
	probeAuth                 func(port int, timeout time.Duration) bool
	probeCore                 func(port int, timeout time.Duration) bool
	probeScan                 func(port int, timeout time.Duration) bool
	probeFileWatch            func(port int, timeout time.Duration) bool
	waitHostReady             func(context.Context, RuntimeConfig) error
	runtimeReady              func(context.Context, RuntimeConfig, RuntimePaths) bool
	processScanner            localProcessScanner
	pollInterval              time.Duration
	upTimeout                 time.Duration
	downTimeout               time.Duration
	processComposeDownTimeout time.Duration
	processCompose            *ProcessComposeManager
	localProxy                *LocalProxyManager
	authService               *AuthServiceManager
	coreService               *CoreServiceManager
	scanControl               *ScanControlPlaneManager
	fileWatcher               *FileWatcherManager
	frontend                  *FrontendManager
	algorithm                 *AlgorithmServiceManager
	milvusLite                *MilvusLiteManager
}

const startupProgressInterval = 10 * time.Second

func NewRuntimeManager(r CommandRunner, execPath string) *RuntimeManager {
	processCompose := NewProcessComposeManager(r, execPath)
	return &RuntimeManager{
		runner:                    r,
		execPath:                  execPath,
		now:                       time.Now,
		out:                       io.Discard,
		errOut:                    io.Discard,
		probeAPI:                  processCompose.ProbeAPI,
		probeLocalProxy:           localProxyHealthAlive,
		probeFrontend:             frontendHealthAlive,
		probeAuth:                 authServiceHealthAlive,
		probeCore:                 coreServiceHealthAlive,
		probeScan:                 scanControlPlaneHealthAlive,
		probeFileWatch:            fileWatcherHealthAlive,
		waitHostReady:             waitForHostAlgorithmReadiness,
		runtimeReady:              nil,
		processScanner:            scanLocalRuntimeProcesses,
		pollInterval:              2 * time.Second,
		upTimeout:                 envDuration(localUpTimeoutEnvVar, time.Duration(defaultLocalUpTimeout)*time.Second),
		downTimeout:               envDuration(localDownTimeoutEnvVar, time.Duration(defaultLocalDownTimeout)*time.Second),
		processComposeDownTimeout: envDuration(processComposeDownTimeoutEnvVar, time.Duration(defaultProcessComposeDownTimeout)*time.Second),
		processCompose:            processCompose,
		localProxy:                NewLocalProxyManager(r),
		authService:               NewAuthServiceManager(r),
		coreService:               NewCoreServiceManager(r),
		scanControl:               NewScanControlPlaneManager(r),
		fileWatcher:               NewFileWatcherManager(r),
		frontend:                  NewFrontendManager(r),
		algorithm:                 NewAlgorithmServiceManager(r),
		milvusLite:                NewMilvusLiteManager(r),
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

func (m *RuntimeManager) progressf(format string, args ...any) {
	_, _ = fmt.Fprintf(m.out, format+"\n", args...)
}

func randomHexToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (m *RuntimeManager) Up(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := validateRequestedRuntimeOwner(cfg); err != nil {
		return err
	}
	freshCfg := cfg
	if err := ensureRuntimeDirs(cfg, paths); err != nil {
		return err
	}
	if err := relocateDesktopPythonVenvs(cfg, paths); err != nil {
		return err
	}
	state, err := readOrNewState(paths, cfg)
	if err != nil {
		return err
	}
	stateCfg := applyStateConfig(freshCfg, state)
	if claimsRuntimeRunning(state) && state.ProcessCompose.APIPort > 0 && m.probeAPI(state.ProcessCompose.APIPort, 500*time.Millisecond) {
		if err := activeRuntimeOwnershipError(state, cfg); err != nil {
			return err
		}
	}
	if m.isExistingRuntimeRunning(ctx, state, stateCfg, paths) {
		return m.reportExistingRuntime(ctx, state, paths)
	}
	if err := m.stopStaleRuntimeIfNeeded(ctx, state, stateCfg, paths); err != nil {
		return err
	}
	if err := m.killStaleRuntimeProcesses(ctx, cfg, paths); err != nil {
		return err
	}
	freshCfg, paths, err = NewRuntimeConfigWithOptions(RuntimeConfigOptions{
		Profile:       cfg.Profile,
		OwnerToken:    cfg.OwnerToken,
		RepoRoot:      paths.RepoRoot,
		RuntimeRoot:   cfg.RuntimeRoot,
		ResourcesRoot: cfg.ResourcesRoot,
	})
	if err != nil {
		return err
	}
	if err := ensureRuntimeDirs(freshCfg, paths); err != nil {
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
	if claimsRuntimeRunning(state) && state.ProcessCompose.APIPort > 0 && m.probeAPI(state.ProcessCompose.APIPort, 500*time.Millisecond) {
		if err := activeRuntimeOwnershipError(state, cfg); err != nil {
			return err
		}
	}
	if m.isExistingRuntimeRunning(ctx, state, stateCfg, paths) {
		return m.reportExistingRuntime(ctx, state, paths)
	}
	if err := m.stopStaleRuntimeIfNeeded(ctx, state, stateCfg, paths); err != nil {
		return err
	}
	if err := m.killStaleRuntimeProcesses(ctx, stateCfg, paths); err != nil {
		return err
	}
	freshCfg, paths, err = NewRuntimeConfigWithOptions(RuntimeConfigOptions{
		Profile:       cfg.Profile,
		OwnerToken:    cfg.OwnerToken,
		RepoRoot:      paths.RepoRoot,
		RuntimeRoot:   cfg.RuntimeRoot,
		ResourcesRoot: cfg.ResourcesRoot,
	})
	if err != nil {
		return err
	}
	if err := ensureRuntimeDirs(freshCfg, paths); err != nil {
		return err
	}
	cfg = freshCfg
	if err := validatePinnedLocalPorts(cfg); err != nil {
		return err
	}
	m.printPortResolutionSummary(cfg)
	if err := writeServiceEndpointFiles(paths, serviceEndpointsFromConfig(cfg)); err != nil {
		return err
	}
	if err := ensureLazyLLMSource(ctx, m.runner, paths.RepoRoot, cfg.Profile); err != nil {
		return err
	}

	token, err := randomHexToken()
	if err != nil {
		return err
	}
	if err := os.WriteFile(paths.RunDirTokenFile, []byte(token), 0o600); err != nil {
		return err
	}

	m.progressf("preparing local runtime directories and process-compose config")
	generatedFile, err := os.Create(paths.GeneratedConfig)
	if err != nil {
		return err
	}
	if err := m.processCompose.WriteGeneratedConfig(generatedFile, paths.RepoRoot, paths, cfg, paths.RunDirTokenFile, cfg.ProcessComposePort); err != nil {
		_ = generatedFile.Close()
		return err
	}
	if err := generatedFile.Close(); err != nil {
		return err
	}

	state.Profile = cfg.Profile
	state.OwnerToken = cfg.OwnerToken
	state.RepoRoot = cfg.RepoRoot
	state.ResourcesRoot = cfg.ResourcesRoot
	state.RuntimeRoot = cfg.RuntimeRoot
	state.ProcessCompose.APIPort = cfg.ProcessComposePort
	state.ProcessCompose.APIRoot = "http://127.0.0.1:" + strconv.Itoa(cfg.ProcessComposePort)
	state.ProcessCompose.TokenFile = paths.RunDirTokenFile
	state.Config = snapshotRuntimeConfig(cfg)
	state = newStateWithServiceStatus(state, "starting")
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		return err
	}

	m.progressf("starting process-compose supervisor on 127.0.0.1:%d", cfg.ProcessComposePort)
	if err := m.processCompose.Up(ctx, cfg, paths); err != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return err
	}

	m.progressf("waiting for process-compose API on 127.0.0.1:%d", cfg.ProcessComposePort)
	if !m.waitForProcessComposeAPI(ctx, cfg.ProcessComposePort, 15*time.Second) {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return fmt.Errorf("process-compose API did not become ready on port %d", cfg.ProcessComposePort)
	}
	m.progressf("process-compose API ready on 127.0.0.1:%d", cfg.ProcessComposePort)

	logCtx, stopLogs := context.WithCancel(ctx)
	logErrCh := make(chan error, 1)
	go func() {
		logErrCh <- m.processCompose.FollowLogs(logCtx, cfg, paths, m.out, m.errOut)
	}()

	stopLogs()
	select {
	case logErr := <-logErrCh:
		if logErr != nil {
			state = newStateWithServiceStatus(state, "failed")
			state.OverallStatus = "failed"
			_ = writeRuntimeState(paths.StateFile, state)
			return logErr
		}
	default:
	}
	if err := m.waitForLocalProxyHealthy(ctx, cfg.LocalProxy.Port, m.upTimeout); err != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return err
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
	if err := m.waitForScanControlPlaneHealthy(ctx, cfg.LocalProxy.ScanHostPort, m.upTimeout); err != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return err
	}
	if err := m.waitForFileWatcherHealthy(ctx, cfg.FileWatcher.Port, m.upTimeout); err != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return err
	}
	if waitErr := m.waitHostAlgorithmsReady(ctx, cfg); waitErr != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return waitErr
	}
	if err := m.waitForFrontendHealthy(ctx, cfg.FrontendPort, m.upTimeout); err != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return err
	}

	state = newStateWithServiceStatus(state, "running")
	state.OverallStatus = "ready"
	state.UpdatedAt = m.now().UTC().Format(time.RFC3339)
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		return err
	}
	m.printReadySummary(cfg)
	if cfg.Profile == "desktop" {
		return m.waitForDesktopRuntimeStop(ctx, paths)
	}
	return nil
}

func ensureRuntimeDirs(cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.FileWatcher.WatchHostDir, 0o755); err != nil {
		return fmt.Errorf("create local document scan directory: %w", err)
	}
	return nil
}

func (m *RuntimeManager) waitForDesktopRuntimeStop(ctx context.Context, paths RuntimePaths) error {
	m.progressf("desktop runtime monitor active")
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		state, err := readRuntimeState(paths.StateFile)
		if err == nil && (state.OverallStatus == "stopped" || state.OverallStatus == "failed") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *RuntimeManager) waitForAuthServiceHealthy(ctx context.Context, port int, timeout time.Duration, pidFile string) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	sawPIDFile := false
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	nextReport := m.now().Add(startupProgressInterval)
	m.progressf("waiting for auth-service health: %s", url)
	for {
		if m.probeAuth(port, time.Second) {
			m.progressf("auth-service ready: %s", url)
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
		if !m.now().Before(nextReport) {
			m.progressf("still waiting for auth-service health: %s", url)
			nextReport = m.now().Add(startupProgressInterval)
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

func (m *RuntimeManager) waitForLocalProxyHealthy(ctx context.Context, port int, timeout time.Duration) error {
	return m.waitForServiceProbeReady(ctx, m.probeLocalProxy, port, localProxyProcessName, "/_local/healthz", timeout)
}

func (m *RuntimeManager) waitForFrontendHealthy(ctx context.Context, port int, timeout time.Duration) error {
	return m.waitForServiceProbeReady(ctx, m.probeFrontend, port, frontendProcessName, "/", timeout)
}

func (m *RuntimeManager) waitForCoreHealthy(ctx context.Context, port int, timeout time.Duration) error {
	return m.waitForServiceProbeReady(ctx, m.probeCore, port, "core", "/health", timeout)
}

func (m *RuntimeManager) waitForScanControlPlaneHealthy(ctx context.Context, port int, timeout time.Duration) error {
	return m.waitForServiceProbeReady(ctx, m.probeScan, port, scanControlPlaneProcessName, "/health", timeout)
}

func (m *RuntimeManager) waitForFileWatcherHealthy(ctx context.Context, port int, timeout time.Duration) error {
	return m.waitForServiceProbeReady(ctx, m.probeFileWatch, port, fileWatcherProcessName, "/health", timeout)
}

func (m *RuntimeManager) waitForServiceProbeReady(ctx context.Context, probe func(int, time.Duration) bool, port int, service string, path string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	nextReport := m.now().Add(startupProgressInterval)
	m.progressf("waiting for %s health: %s", service, url)
	for {
		if probe(port, time.Second) {
			m.progressf("%s ready: %s", service, url)
			return nil
		}
		if !m.now().Before(nextReport) {
			m.progressf("still waiting for %s health: %s", service, url)
			nextReport = m.now().Add(startupProgressInterval)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%s health check timed out on port %d", service, port)
		case <-ticker.C:
		}
	}
}

func (m *RuntimeManager) waitHostAlgorithmsReady(ctx context.Context, cfg RuntimeConfig) error {
	m.progressf("waiting for host algorithm services")
	monitorCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.reportHostAlgorithmReadiness(monitorCtx, cfg)
	}()

	waitErr := m.waitHostReady(ctx, cfg)
	cancel()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}
	if waitErr != nil {
		return waitErr
	}
	m.progressf("host algorithm services ready")
	return nil
}

func (m *RuntimeManager) reportHostAlgorithmReadiness(ctx context.Context, cfg RuntimeConfig) {
	m.progressf("host algorithm status: %s", hostAlgorithmReadinessSummary(ctx, cfg))
	ticker := time.NewTicker(startupProgressInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.progressf("host algorithm status: %s", hostAlgorithmReadinessSummary(ctx, cfg))
		}
	}
}

func hostAlgorithmReadinessSummary(ctx context.Context, cfg RuntimeConfig) string {
	statuses := make([]string, 0, len(algorithmProcessSpecs(cfg.Algorithm))+2)
	if cfg.ModeProfile.VectorStore.ManagedProcess {
		statuses = append(statuses, readinessLabel(milvusLiteProcessName, tcpOK(ctx, "127.0.0.1", cfg.ModeProfile.VectorStore.Port, 500*time.Millisecond)))
	}
	for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
		url := fmt.Sprintf("http://127.0.0.1:%d%s", spec.Port, spec.HealthPath)
		statuses = append(statuses, readinessLabel(spec.Name, httpOK(ctx, url, 500*time.Millisecond)))
	}
	registrationURL := fmt.Sprintf("http://127.0.0.1:%d/algo/list", cfg.Algorithm.ProcessorPort)
	registrationCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	statuses = append(statuses, readinessLabel("algorithm-registration", algorithmRegistered(registrationCtx, registrationURL)))
	return strings.Join(statuses, ", ")
}

func readinessLabel(name string, ready bool) string {
	status := "waiting"
	if ready {
		status = "ready"
	}
	return name + "=" + status
}

func localProxyHealthAlive(port int, timeout time.Duration) bool {
	return httpOK(context.Background(), fmt.Sprintf("http://127.0.0.1:%d/_local/healthz", port), timeout)
}

func frontendHealthAlive(port int, timeout time.Duration) bool {
	return httpOK(context.Background(), fmt.Sprintf("http://127.0.0.1:%d/", port), timeout)
}

func (m *RuntimeManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := validateRequestedRuntimeOwner(cfg); err != nil {
		return err
	}
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	state, err := readOrNewState(paths, cfg)
	if err != nil {
		return err
	}
	if err := activeRuntimeOwnershipError(state, cfg); err != nil {
		active := claimsRuntimeRunning(state) ||
			(state.ProcessCompose.APIPort > 0 && m.probeAPI(state.ProcessCompose.APIPort, 500*time.Millisecond)) ||
			processComposeSupervisorAlive(paths)
		if active {
			return err
		}
		m.progressf("runtime state belongs to another profile or instance; skipping shutdown")
		return nil
	}
	cfg = applyStateConfig(cfg, state)
	if state.ProcessCompose.APIPort > 0 {
		cfg.ProcessComposePort = state.ProcessCompose.APIPort
	}
	var downErr error
	apiAlive := m.probeAPI(cfg.ProcessComposePort, 500*time.Millisecond)
	fallbackCleanup := !apiAlive
	if apiAlive {
		processComposeTimeout := m.effectiveProcessComposeDownTimeout()
		m.progressf("stopping process-compose on 127.0.0.1:%d (timeout %s)", cfg.ProcessComposePort, processComposeTimeout)
		downCtx, cancel := context.WithTimeout(ctx, processComposeTimeout)
		defer cancel()
		downErr = m.processComposeDownWithProgress(downCtx, cfg, paths)
		fallbackCleanup = downErr != nil
	} else {
		m.progressf("process-compose API not reachable on 127.0.0.1:%d; skipping process-compose down", cfg.ProcessComposePort)
	}
	if !fallbackCleanup {
		if err := m.killStaleRuntimeProcesses(context.Background(), cfg, paths); err != nil && downErr == nil {
			downErr = err
		}
		if err := m.waitForRuntimeStopped(ctx, cfg, paths); err != nil {
			m.progressf("process-compose supervisor still reachable; stopping recorded supervisor process")
			if stopErr := m.stopProcessComposeSupervisor(context.Background(), paths); stopErr != nil && downErr == nil {
				downErr = stopErr
			}
			if waitErr := m.waitForRuntimeStopped(ctx, cfg, paths); waitErr != nil && downErr == nil {
				downErr = waitErr
			}
		}
	} else {
		if downErr != nil {
			m.progressf("process-compose down failed; running fallback local runtime cleanup")
		} else {
			m.progressf("running fallback local runtime cleanup")
		}
		fallbackErr := error(nil)
		if apiAlive {
			if err := m.stopProcessComposeSupervisor(context.Background(), paths); err != nil && fallbackErr == nil {
				fallbackErr = err
			}
		}
		_ = m.killStaleRuntimeProcesses(context.Background(), cfg, paths)
		m.progressf("stopping frontend Caddy on 127.0.0.1:%d", cfg.FrontendPort)
		if err := m.frontend.Down(ctx, cfg, paths); err != nil && fallbackErr == nil {
			fallbackErr = err
		}
		m.progressf("stopping Local Gateway proxy on 127.0.0.1:%d", cfg.LocalProxy.Port)
		if err := m.localProxy.Down(ctx, cfg, paths); err != nil && fallbackErr == nil {
			fallbackErr = err
		}
		for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
			m.progressf("stopping algorithm process %s", spec.Name)
			if err := m.algorithm.Down(ctx, paths, spec.Name); err != nil && fallbackErr == nil {
				fallbackErr = err
			}
		}
		m.progressf("stopping Milvus Lite process")
		if err := m.milvusLite.Down(ctx, paths); err != nil && fallbackErr == nil {
			fallbackErr = err
		}
		m.progressf("stopping core service on 127.0.0.1:%d", cfg.LocalProxy.CoreHostPort)
		if err := m.coreService.Down(ctx, cfg, paths); err != nil && fallbackErr == nil {
			fallbackErr = err
		}
		m.progressf("stopping scan-control-plane on 127.0.0.1:%d", cfg.LocalProxy.ScanHostPort)
		if err := m.scanControl.Down(ctx, paths); err != nil && fallbackErr == nil {
			fallbackErr = err
		}
		m.progressf("stopping file-watcher on 127.0.0.1:%d", cfg.FileWatcher.Port)
		if err := m.fileWatcher.Down(ctx, paths); err != nil && fallbackErr == nil {
			fallbackErr = err
		}
		m.progressf("stopping auth-service on 127.0.0.1:%d", cfg.AuthService.Port)
		if err := m.authService.Down(ctx, cfg, paths); err != nil && fallbackErr == nil {
			fallbackErr = err
		}
		if fallbackErr == nil {
			if err := m.killStaleRuntimeProcesses(context.Background(), cfg, paths); err != nil {
				fallbackErr = err
			}
		}
		if fallbackErr == nil {
			fallbackErr = m.waitForRuntimeStopped(ctx, cfg, paths)
		}
		downErr = fallbackErr
		if downErr == nil {
			m.progressf("fallback local runtime cleanup completed")
		}
	}
	if downErr != nil {
		state = newStateWithServiceStatus(state, "failed")
		state.OverallStatus = "failed"
		_ = writeRuntimeState(paths.StateFile, state)
		return downErr
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

func (m *RuntimeManager) processComposeDownWithProgress(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- m.processCompose.Down(ctx, cfg, paths, m.out, m.errOut)
	}()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	nextReport := m.now().Add(5 * time.Second)
	for {
		select {
		case err := <-errCh:
			return err
		case <-ticker.C:
			apiAlive := cfg.ProcessComposePort > 0 && m.probeAPI(cfg.ProcessComposePort, 500*time.Millisecond)
			supervisorAlive := processComposeSupervisorAlive(paths)
			if !apiAlive && !supervisorAlive {
				m.progressf("process-compose supervisor stopped on 127.0.0.1:%d", cfg.ProcessComposePort)
				return nil
			}
			if !m.now().Before(nextReport) {
				m.progressf(
					"still waiting for process-compose down on 127.0.0.1:%d: api=%s supervisor=%s; service logs: %s",
					cfg.ProcessComposePort,
					aliveLabel(apiAlive),
					aliveLabel(supervisorAlive),
					displayPath(paths.RepoRoot, paths.LogsDir),
				)
				nextReport = m.now().Add(5 * time.Second)
			}
		case <-ctx.Done():
			m.progressf("process-compose down timed out on 127.0.0.1:%d; switching to fallback cleanup", cfg.ProcessComposePort)
			select {
			case err := <-errCh:
				return err
			case <-time.After(1 * time.Second):
				return ctx.Err()
			}
		}
	}
}

func processComposeSupervisorAlive(paths RuntimePaths) bool {
	pid, err := readPIDFile(paths.ProcessComposePIDFile)
	return err == nil && pid > 0 && processAlive(pid)
}

func aliveLabel(alive bool) string {
	if alive {
		return "alive"
	}
	return "stopped"
}

func (m *RuntimeManager) effectiveProcessComposeDownTimeout() time.Duration {
	timeout := m.processComposeDownTimeout
	if timeout <= 0 {
		timeout = time.Duration(defaultProcessComposeDownTimeout) * time.Second
	}
	if m.downTimeout > 0 && timeout > m.downTimeout {
		return m.downTimeout
	}
	return timeout
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
	err := m.processCompose.Down(downCtx, staleCfg, paths, m.out, m.errOut)
	if err != nil {
		_ = m.stopProcessComposeSupervisor(context.Background(), paths)
		_ = m.killStaleRuntimeProcesses(context.Background(), cfg, paths)
	}
	return nil
}

func (m *RuntimeManager) killStaleRuntimeProcesses(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	records := discoverLocalRuntimeProcesses(paths, cfg, m.processScanner)
	if len(records) == 0 {
		return nil
	}
	m.progressf("stopping %d orphan local runtime process(es) for this repo", len(records))
	err := stopLocalProcessRecords(ctx, records)
	cleanupLocalProcessRecords(paths, records)
	return err
}

func (m *RuntimeManager) stopProcessComposeSupervisor(ctx context.Context, paths RuntimePaths) error {
	pid, err := readPIDFile(paths.ProcessComposePIDFile)
	if err != nil {
		return err
	}
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(paths.ProcessComposePIDFile)
		return nil
	}
	if err := stopSupervisorProcess(pid); err != nil {
		_ = proc.Signal(os.Interrupt)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = forceKillProcessTree(pid)
			return ctx.Err()
		case <-deadline.C:
			_ = forceKillProcessTree(pid)
			_ = os.Remove(paths.ProcessComposePIDFile)
			return nil
		case <-ticker.C:
			if !processAlive(pid) {
				_ = os.Remove(paths.ProcessComposePIDFile)
				return nil
			}
		}
	}
}

func (m *RuntimeManager) checkRuntimeReady(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) bool {
	if m.runtimeReady != nil {
		return m.runtimeReady(ctx, cfg, paths)
	}
	if !m.probeLocalProxy(cfg.LocalProxy.Port, 500*time.Millisecond) {
		return false
	}
	if !m.probeFrontend(cfg.FrontendPort, 500*time.Millisecond) {
		return false
	}
	if !m.probeAuth(cfg.AuthService.Port, 500*time.Millisecond) {
		return false
	}
	if !m.probeCore(cfg.LocalProxy.CoreHostPort, 500*time.Millisecond) {
		return false
	}
	if !m.probeScan(cfg.LocalProxy.ScanHostPort, 500*time.Millisecond) {
		return false
	}
	if !m.probeFileWatch(cfg.FileWatcher.Port, 500*time.Millisecond) {
		return false
	}
	for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
		if !httpOK(ctx, fmt.Sprintf("http://127.0.0.1:%d%s", spec.Port, spec.HealthPath), 500*time.Millisecond) {
			return false
		}
	}
	if cfg.ModeProfile.VectorStore.ManagedProcess && !tcpOK(ctx, "127.0.0.1", cfg.ModeProfile.VectorStore.Port, 500*time.Millisecond) {
		return false
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

func (m *RuntimeManager) waitForRuntimeStopped(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	timeout := m.downTimeout
	if timeout <= 0 {
		timeout = time.Duration(defaultLocalDownTimeout) * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	nextReport := m.now()
	m.progressf("waiting up to %s for local runtime processes to stop", timeout)

	for {
		apiAlive := cfg.ProcessComposePort > 0 && m.probeAPI(cfg.ProcessComposePort, 500*time.Millisecond)
		authAlive := false
		if _, statErr := os.Stat(paths.AuthServicePIDFile); statErr == nil && cfg.AuthService.Port > 0 {
			authAlive = m.probeAuth(cfg.AuthService.Port, 500*time.Millisecond)
		}
		milvusAlive := false
		if _, statErr := os.Stat(paths.MilvusLitePIDFile); statErr == nil && cfg.ModeProfile.VectorStore.ManagedProcess && cfg.ModeProfile.VectorStore.Port > 0 {
			milvusAlive = tcpOK(ctx, "127.0.0.1", cfg.ModeProfile.VectorStore.Port, 500*time.Millisecond)
		}
		if !apiAlive && !authAlive && !milvusAlive {
			return nil
		}
		if !m.now().Before(nextReport) {
			blockers := make([]string, 0, 5)
			if apiAlive {
				blockers = append(blockers, "process-compose API")
			}
			if authAlive {
				blockers = append(blockers, "auth-service")
			}
			if milvusAlive {
				blockers = append(blockers, "Milvus Lite")
			}
			if len(blockers) == 0 {
				blockers = append(blockers, "runtime probes")
			}
			m.progressf("still waiting for local runtime to stop: %s", strings.Join(blockers, ", "))
			nextReport = m.now().Add(5 * time.Second)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out after %s waiting for local runtime to stop", timeout)
		case <-ticker.C:
		}
	}
}

func (m *RuntimeManager) printReadySummary(cfg RuntimeConfig) {
	_, _ = fmt.Fprintf(m.out, "local runtime ready\n")
	_, _ = fmt.Fprintf(m.out, "process-compose: http://127.0.0.1:%d\n", cfg.ProcessComposePort)
	_, _ = fmt.Fprintf(m.out, "frontend: http://localhost:%d\n", cfg.FrontendPort)
	if cfg.NetworkProfile == "lan" {
		if ip := firstLANIPv4(); ip != "" {
			_, _ = fmt.Fprintf(m.out, "frontend LAN: http://%s:%d\n", ip, cfg.FrontendPort)
		}
	}
	_, _ = fmt.Fprintf(m.out, "status: local-runtime-manager status --json\n")
}

func (m *RuntimeManager) printPortResolutionSummary(cfg RuntimeConfig) {
	for _, resolution := range cfg.PortResolutions {
		name := resolution.Name
		if name == "" {
			name = "local service"
		}
		envName := resolution.EnvName
		if envName == "" {
			envName = "default"
		}
		_, _ = fmt.Fprintf(
			m.errOut,
			"local port moved: %s %s preferred %d, using %d (%s)\n",
			name,
			envName,
			resolution.RequestedPort,
			resolution.ResolvedPort,
			resolution.Reason,
		)
	}
}

func validatePinnedLocalPorts(cfg RuntimeConfig) error {
	if !envBool(localPortsPinnedEnvVar, false) {
		return nil
	}
	seen := map[int]string{}
	for _, item := range resolvedLocalPorts(cfg) {
		if previous, ok := seen[item.port]; ok {
			return fmt.Errorf("local ports are pinned but %s and %s both resolve to port %d", previous, item.name, item.port)
		}
		seen[item.port] = item.name
		if !localPortAvailableOn(item.address, item.port) {
			return fmt.Errorf("local ports are pinned and %s port %d is already in use; unset %s or choose a free port", item.name, item.port, localPortsPinnedEnvVar)
		}
	}
	return nil
}

type localPortItem struct {
	name    string
	port    int
	address string
}

func resolvedLocalPorts(cfg RuntimeConfig) []localPortItem {
	frontendAddress := "127.0.0.1"
	if cfg.NetworkProfile == "lan" {
		frontendAddress = "0.0.0.0"
	}
	items := []localPortItem{
		{name: "process-compose", port: cfg.ProcessComposePort, address: "127.0.0.1"},
		{name: "frontend", port: cfg.FrontendPort, address: frontendAddress},
		{name: "local-proxy", port: cfg.LocalProxy.Port, address: "127.0.0.1"},
		{name: "auth-service", port: cfg.AuthService.Port, address: "127.0.0.1"},
		{name: "core", port: cfg.LocalProxy.CoreHostPort, address: "127.0.0.1"},
		{name: "scan-control-plane", port: cfg.LocalProxy.ScanHostPort, address: "127.0.0.1"},
		{name: "file-watcher", port: cfg.FileWatcher.Port, address: "127.0.0.1"},
		{name: "postgres", port: cfg.Algorithm.PostgresPort, address: "127.0.0.1"},
		{name: "document-service", port: cfg.Algorithm.DocPort, address: "127.0.0.1"},
		{name: "processor-server", port: cfg.Algorithm.ProcessorPort, address: "127.0.0.1"},
		{name: "lazyllm-algo", port: cfg.Algorithm.AlgoPort, address: "127.0.0.1"},
		{name: "processor-worker", port: cfg.Algorithm.WorkerPort, address: "127.0.0.1"},
		{name: "chat", port: cfg.Algorithm.ChatPort, address: "127.0.0.1"},
		{name: "milvus-lite", port: cfg.ModeProfile.VectorStore.Port, address: "127.0.0.1"},
		{name: "opensearch", port: cfg.Algorithm.OpenSearchPort, address: "127.0.0.1"},
	}
	if cfg.Algorithm.EnableEvo {
		items = append(items, localPortItem{name: "evo-api", port: cfg.Algorithm.EvoPort, address: "127.0.0.1"})
	}
	if cfg.Algorithm.RouterPortPoolStart > 0 {
		end := cfg.Algorithm.RouterPortPoolEnd
		if end < cfg.Algorithm.RouterPortPoolStart {
			end = cfg.Algorithm.RouterPortPoolStart
		}
		for port := cfg.Algorithm.RouterPortPoolStart; port <= end; port++ {
			items = append(items, localPortItem{name: "router-port-pool", port: port, address: "127.0.0.1"})
		}
	}
	return items
}

func firstLANIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		ip = ip.To4()
		if ip == nil || ip.IsLoopback() {
			continue
		}
		return ip.String()
	}
	return ""
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
	return processAlive(pid), nil
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
	if state.ResourcesRoot == "" {
		state.ResourcesRoot = cfg.ResourcesRoot
	}
	if state.RuntimeRoot == "" {
		state.RuntimeRoot = cfg.RuntimeRoot
	}

	resp := StatusResponse{
		Runtime:        state.Profile,
		Profile:        state.Profile,
		OwnerMatched:   cfg.Profile == "desktop" && cfg.OwnerToken != "" && cfg.OwnerToken == state.OwnerToken,
		OverallStatus:  state.OverallStatus,
		RepoRoot:       state.RepoRoot,
		ResourcesRoot:  state.ResourcesRoot,
		BuildRoot:      cfg.BuildRoot,
		RuntimeRoot:    state.RuntimeRoot,
		DataDir:        paths.DataDir,
		LogsDir:        paths.LogsDir,
		ProcessCompose: state.ProcessCompose,
		Config:         snapshotRuntimeConfig(cfg),
		Services:       state.Services,
	}
	if resp.Services == nil {
		resp.Services = map[string]RuntimeServiceState{}
	}
	if _, ok := resp.Services[processComposeServiceName]; !ok {
		resp.Services[processComposeServiceName] = RuntimeServiceState{
			Kind:   "host-supervisor",
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
	if cfg.ModeProfile.VectorStore.ManagedProcess {
		if _, ok := resp.Services[milvusLiteProcessName]; !ok {
			resp.Services[milvusLiteProcessName] = RuntimeServiceState{
				Kind:   "host-process",
				Status: "unknown",
			}
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
		if m.probeLocalProxy(cfg.LocalProxy.Port, 500*time.Millisecond) {
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
		if m.probeFrontend(cfg.FrontendPort, 500*time.Millisecond) {
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
		if cfg.ModeProfile.VectorStore.ManagedProcess {
			milvus := resp.Services[milvusLiteProcessName]
			milvus.Kind = "host-process"
			if tcpOK(ctx, "127.0.0.1", cfg.ModeProfile.VectorStore.Port, 500*time.Millisecond) {
				milvus.Status = "running"
			} else {
				hostHealthy = false
				if milvus.Status == "running" || milvus.Status == "starting" {
					milvus.Status = "stale"
				} else if milvus.Status == "" || milvus.Status == "unknown" {
					milvus.Status = "stopped"
				}
			}
			resp.Services[milvusLiteProcessName] = milvus
		}
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
		if m.checkRuntimeReady(ctx, cfg, paths) {
			resp.OverallStatus = "ready"
			s := resp.Services[processComposeServiceName]
			if s.Status == "running" || s.Status == "starting" || s.Status == "stale" {
				s.Status = "stopped"
			}
			resp.Services[processComposeServiceName] = s
		} else if resp.OverallStatus == "ready" || resp.OverallStatus == "running" || resp.OverallStatus == "starting" {
			resp.OverallStatus = "stale"
		} else if resp.OverallStatus == "" {
			resp.OverallStatus = "stopped"
		}
		if resp.OverallStatus != "ready" {
			s := resp.Services[processComposeServiceName]
			if s.Status == "running" || s.Status == "starting" {
				s.Status = "stale"
			} else if s.Status == "" || s.Status == "unknown" {
				s.Status = "stopped"
			}
			resp.Services[processComposeServiceName] = s
		}
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
		fmt.Sprintf("resourcesRoot: %s", resp.ResourcesRoot),
		fmt.Sprintf("buildRoot: %s", resp.BuildRoot),
		fmt.Sprintf("runtimeRoot: %s", resp.RuntimeRoot),
		fmt.Sprintf("dataDir: %s", resp.DataDir),
		fmt.Sprintf("logsDir: %s", resp.LogsDir),
	}
	for name, svc := range resp.Services {
		lines = append(lines, fmt.Sprintf("%s.kind=%s status=%s", name, svc.Kind, svc.Status))
	}
	return strings.Join(lines, "\n") + "\n"
}
