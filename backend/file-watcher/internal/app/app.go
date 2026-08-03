package app

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	internal "github.com/lazymind/file_watcher/internal"
	"github.com/lazymind/file_watcher/internal/api"
	"github.com/lazymind/file_watcher/internal/config"
	"github.com/lazymind/file_watcher/internal/control"
	"github.com/lazymind/file_watcher/internal/fs"
	"github.com/lazymind/file_watcher/internal/source"
)

// App coordinates the process-level lifecycle.
type App struct {
	cfg         *config.Config
	log         *zap.Logger
	server      *http.Server
	heartbeat   *control.HeartbeatReporter
	manager     source.Manager
	cpClient    control.ControlPlaneClient
	agentStatus internal.AgentStatus
	statusMu    sync.Mutex
}

// New wires all dependencies and returns a runnable App.
func New(cfg *config.Config, log *zap.Logger) *App {
	// Control-plane client.
	cpClient := control.NewHTTPClient(cfg.ControlPlaneBaseURL, cfg.AgentToken, log)

	// fs layer.
	pathMapper := fs.NewPathMapper(cfg.HostPathStyle, cfg.PathMappings)
	validator := fs.NewPathValidator(cfg.Security.AllowedRoots)
	watcher := fs.NewRecursiveWatcher(cfg.AgentID, cfg.Watch, cpClient, pathMapper, log)
	stagingSvc := fs.NewStagingService(cfg.Staging, log)

	// Source manager, which also implements Manager and CommandDispatcher.
	mgr := source.NewManager(cfg, watcher, validator, pathMapper, log)

	a := &App{
		cfg:         cfg,
		log:         log,
		manager:     mgr,
		cpClient:    cpClient,
		agentStatus: internal.AgentStatusRegistering,
	}

	// Heartbeat and command puller. statusFn reads the dynamic App status through a closure.
	heartbeat := control.NewHeartbeatReporter(
		cfg,
		cpClient,
		mgr,
		a.getStatus,
		mgr.Stats,
		log,
	)
	a.heartbeat = heartbeat

	// HTTP server
	handler := api.NewHandler(mgr, validator, stagingSvc, pathMapper, log)
	a.server = api.NewServer(cfg, handler, log)

	return a
}

// Run starts subsystems in a fixed order and blocks until an exit signal arrives.
func (a *App) Run(ctx context.Context) error {
	hostname, _ := os.Hostname()
	advertiseAddr := a.cfg.AgentListenURL()
	if err := a.ensureBaseDirs(); err != nil {
		return err
	}
	a.log.Info("file_watcher starting",
		zap.String("agent_id", a.cfg.AgentID),
		zap.String("hostname", hostname),
		zap.String("listen", a.cfg.ListenAddr),
		zap.String("advertise", advertiseAddr),
		zap.String("base_root", a.cfg.BaseRoot),
		zap.String("staging_root", a.cfg.Staging.HostRoot),
		zap.String("log_dir", a.cfg.LogDir),
	)

	// Start the HTTP server before registration so recovery reconciliation can
	// reach the local filesystem as soon as control-plane accepts the Agent.
	serverErr := make(chan error, 1)
	go func() {
		a.log.Info("http server listening", zap.String("addr", a.cfg.ListenAddr))
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	registerRequest := internal.RegisterAgentRequest{
		AgentID:    a.cfg.AgentID,
		TenantID:   a.cfg.TenantID,
		Hostname:   hostname,
		Version:    "0.1.0",
		ListenAddr: advertiseAddr,
	}
	if err := a.cpClient.RegisterAgent(ctx, registerRequest); err != nil {
		a.log.Warn("register agent failed, will retry registration", zap.Error(err))
		go a.retryRegistration(ctx, registerRequest)
	} else {
		a.setStatus(internal.AgentStatusOnline)
	}

	// Start the heartbeat goroutine.
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()
	go a.heartbeat.Run(heartbeatCtx)

	// Start the health check loop.
	go a.healthLoop(ctx)

	// Wait for an exit signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		a.log.Info("received signal, shutting down", zap.String("signal", sig.String()))
	case err := <-serverErr:
		a.log.Error("http server error", zap.Error(err))
	case <-ctx.Done():
	}

	return a.shutdown()
}

func (a *App) retryRegistration(ctx context.Context, req internal.RegisterAgentRequest) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if err := a.cpClient.RegisterAgent(ctx, req); err != nil {
			a.log.Warn("register agent retry failed", zap.Duration("next_retry", backoff), zap.Error(err))
			backoff = minDuration(backoff*2, time.Minute)
			continue
		}
		a.setStatus(internal.AgentStatusOnline)
		a.log.Info("agent registration recovered")
		return
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (a *App) shutdown() error {
	a.log.Info("shutting down http server")
	if err := api.GracefulShutdown(a.server, 10*time.Second); err != nil {
		a.log.Warn("http server shutdown error", zap.Error(err))
	}
	a.log.Info("file_watcher stopped")
	return nil
}

// healthLoop runs health checks every 30s and updates AgentStatus.
func (a *App) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runHealthCheck(ctx)
		}
	}
}

func (a *App) runHealthCheck(ctx context.Context) {
	var failures []string

	// 1. Check whether local runtime directories are writable.
	if a.cfg.Staging.Enabled {
		if err := checkDirWritable(a.cfg.Staging.HostRoot); err != nil {
			failures = append(failures, "staging_not_writable: "+err.Error())
		}
	}
	if strings.TrimSpace(a.cfg.LogDir) != "" {
		if err := checkDirWritable(a.cfg.LogDir); err != nil {
			failures = append(failures, "log_dir_not_writable: "+err.Error())
		}
	}

	// 2. Check control-plane reachability without mutating Agent state.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.cpClient.CheckHealth(probeCtx); err != nil {
		failures = append(failures, "control_plane_unreachable: "+err.Error())
	}

	// 3. Check whether each Source watcher is alive.
	runtimes := a.manager.ListRuntimes()
	for _, rt := range runtimes {
		if rt.Status == internal.SourceRuntimeStatusRunning && !rt.WatcherHealthy {
			failure := "watcher_dead: source_id=" + rt.SourceID
			if rt.WatcherLastError != "" {
				failure += ", error=" + rt.WatcherLastError
			}
			failures = append(failures, failure)
		}
	}

	// 4. Check whether any Source is in ERROR status.
	for _, rt := range runtimes {
		if rt.Status == internal.SourceRuntimeStatusError {
			failures = append(failures, "source_error: source_id="+rt.SourceID)
		}
	}

	if len(failures) > 0 {
		a.setStatus(internal.AgentStatusDegraded)
		a.log.Warn("health check failed",
			zap.Int("active_sources", len(runtimes)),
			zap.Strings("failures", failures),
		)
	} else {
		a.setStatus(internal.AgentStatusOnline)
		a.log.Info("health check ok", zap.Int("active_sources", len(runtimes)))
	}
}

func (a *App) getStatus() internal.AgentStatus {
	a.statusMu.Lock()
	defer a.statusMu.Unlock()
	return a.agentStatus
}

func (a *App) setStatus(s internal.AgentStatus) {
	a.statusMu.Lock()
	defer a.statusMu.Unlock()
	a.agentStatus = s
}

// checkDirWritable checks whether a directory exists and is writable.
func checkDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe := dir + "/.health_probe"
	f, err := os.Create(probe)
	if err != nil {
		return err
	}
	_ = f.Close()
	return os.Remove(probe)
}

func (a *App) ensureBaseDirs() error {
	if a.cfg.Staging.Enabled && strings.TrimSpace(a.cfg.Staging.HostRoot) != "" {
		if err := os.MkdirAll(a.cfg.Staging.HostRoot, 0o755); err != nil {
			return err
		}
	}
	if strings.TrimSpace(a.cfg.LogDir) != "" {
		if err := os.MkdirAll(a.cfg.LogDir, 0o755); err != nil {
			return err
		}
	}
	return nil
}
