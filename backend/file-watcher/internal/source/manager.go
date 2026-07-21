package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"

	internal "github.com/lazymind/file_watcher/internal"
	"github.com/lazymind/file_watcher/internal/config"
	"github.com/lazymind/file_watcher/internal/fs"
)

// Manager defines the Source lifecycle management interface.
type Manager interface {
	StartSource(ctx context.Context, req internal.StartSourceRequest) error
	StopSource(ctx context.Context, sourceID string) error
	ListRuntimes() []internal.SourceRuntime
	HandleCommand(ctx context.Context, cmd internal.Command) (any, error)
	Stats() (sourceCount, watchCount, taskCount int)
}

type manager struct {
	cfg       *config.Config
	watcher   fs.RecursiveWatcher
	validator fs.PathValidator
	mapper    fs.PathMapper
	log       *zap.Logger

	mu       sync.RWMutex
	runtimes map[string]*runtimeEntry
}

type runtimeEntry struct {
	runtime internal.SourceRuntime
	cancel  context.CancelFunc
}

func NewManager(
	cfg *config.Config,
	watcher fs.RecursiveWatcher,
	validator fs.PathValidator,
	mapper fs.PathMapper,
	log *zap.Logger,
) Manager {
	if mapper == nil {
		mapper = fs.NewPathMapper("", nil)
	}
	return &manager{
		cfg:       cfg,
		watcher:   watcher,
		validator: validator,
		mapper:    mapper,
		log:       log,
		runtimes:  make(map[string]*runtimeEntry),
	}
}

func (m *manager) StartSource(ctx context.Context, req internal.StartSourceRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := runtimeKey(req.SourceID, req.BindingID)

	if _, exists := m.runtimes[key]; exists {
		m.log.Info("source binding already running, skip start", zap.String("source_id", req.SourceID), zap.String("binding_id", req.BindingID))
		return nil
	}

	publicRootPath := m.mapper.CleanPublic(req.RootPath)
	runtimeRootPath := m.mapper.ToRuntime(req.RootPath)
	// Validate the path.
	if err := m.validator.EnsureAllowed(runtimeRootPath); err != nil {
		return err
	}
	if err := m.ensureSourceDirs(req.SourceID); err != nil {
		return err
	}

	// Source is long-lived and should not be bound to a single HTTP or command request context.
	sourceCtx, cancel := context.WithCancel(context.Background())
	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = m.cfg.TenantID
	}
	rt := internal.SourceRuntime{
		SourceID:  req.SourceID,
		BindingID: req.BindingID,
		TenantID:  tenantID,
		RootPath:  publicRootPath,
		Status:    internal.SourceRuntimeStatusStarting,
	}

	entry := &runtimeEntry{runtime: rt, cancel: cancel}
	m.runtimes[key] = entry

	go func() {
		if !req.SkipInitialScan {
			m.log.Info("source start uses watcher only; initial scan disabled",
				zap.String("source_id", req.SourceID),
				zap.String("root_path", publicRootPath),
			)
		}
		if err := m.watcher.Start(sourceCtx, req.SourceID, req.BindingID, tenantID, runtimeRootPath); err != nil {
			m.log.Error("watcher start failed",
				zap.String("source_id", req.SourceID),
				zap.Error(err),
			)
			m.setStatus(key, internal.SourceRuntimeStatusError)
			return
		}
		m.log.Info("source lifecycle watcher start done",
			zap.String("source_id", req.SourceID),
		)
		m.setWatcherEnabled(key, true)
		m.setStatus(key, internal.SourceRuntimeStatusWatching)
		m.setStatus(key, internal.SourceRuntimeStatusRunning)
		m.log.Info("source started", zap.String("source_id", req.SourceID))
	}()

	return nil
}

func (m *manager) StopSource(_ context.Context, sourceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found := false
	for key, entry := range m.runtimes {
		if entry.runtime.SourceID != sourceID {
			continue
		}
		found = true
		entry.cancel()
		_ = m.watcher.Stop(entry.runtime.SourceID, entry.runtime.BindingID)
		entry.runtime.WatcherEnabled = false
		entry.runtime.Status = internal.SourceRuntimeStatusStopped
		delete(m.runtimes, key)
	}
	if !found {
		m.log.Info("source already stopped", zap.String("source_id", sourceID))
		return nil
	}

	m.log.Info("source stopped", zap.String("source_id", sourceID))
	return nil
}

func (m *manager) stopBinding(sourceID, bindingID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := runtimeKey(sourceID, bindingID)
	entry, ok := m.runtimes[key]
	if !ok {
		return nil
	}
	entry.cancel()
	_ = m.watcher.Stop(sourceID, bindingID)
	entry.runtime.WatcherEnabled = false
	entry.runtime.Status = internal.SourceRuntimeStatusStopped
	delete(m.runtimes, key)
	return nil
}

func (m *manager) ListRuntimes() []internal.SourceRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]internal.SourceRuntime, 0, len(m.runtimes))
	for _, e := range m.runtimes {
		rt := e.runtime
		health := m.watcher.Health(rt.SourceID, rt.BindingID)
		rt.WatcherEnabled = health.Enabled
		rt.WatcherHealthy = health.Healthy
		rt.WatcherLastError = health.LastError
		if !health.LastEventAt.IsZero() {
			rt.LastEventAt = health.LastEventAt
		}
		result = append(result, rt)
	}
	return result
}

// HandleCommand handles commands issued by control-plane.
func (m *manager) HandleCommand(ctx context.Context, cmd internal.Command) (any, error) {
	m.log.Info("received control-plane command",
		zap.Int64("command_id", cmd.ID),
		zap.String("type", string(cmd.Type)),
		zap.String("source_id", cmd.SourceID),
		zap.String("tenant_id", cmd.TenantID),
		zap.String("mode", string(cmd.Mode)),
		zap.String("document_id", cmd.DocumentID),
		zap.String("version_id", cmd.VersionID),
	)
	switch cmd.Type {
	case internal.CommandStartSource:
		if err := m.ensureSourceDirs(cmd.SourceID); err != nil {
			return nil, err
		}
		return nil, m.StartSource(ctx, internal.StartSourceRequest{
			SourceID:        cmd.SourceID,
			BindingID:       cmd.BindingID,
			TenantID:        m.resolveTenantID(cmd.SourceID, cmd.BindingID, cmd.TenantID),
			RootPath:        cmd.RootPath,
			SkipInitialScan: cmd.SkipInitialScan,
		})
	case internal.CommandStopSource:
		if cmd.BindingID != "" {
			return nil, m.stopBinding(cmd.SourceID, cmd.BindingID)
		}
		return nil, m.StopSource(ctx, cmd.SourceID)
	case internal.CommandScanSource:
		return legacyDisabledResult(cmd.Type), nil
	case internal.CommandReloadSource:
		if cmd.BindingID != "" {
			_ = m.stopBinding(cmd.SourceID, cmd.BindingID)
		} else {
			_ = m.StopSource(ctx, cmd.SourceID)
		}
		if err := m.ensureSourceDirs(cmd.SourceID); err != nil {
			return nil, err
		}
		return nil, m.StartSource(ctx, internal.StartSourceRequest{
			SourceID:        cmd.SourceID,
			BindingID:       cmd.BindingID,
			TenantID:        m.resolveTenantID(cmd.SourceID, cmd.BindingID, cmd.TenantID),
			RootPath:        cmd.RootPath,
			SkipInitialScan: cmd.SkipInitialScan,
		})
	case internal.CommandSnapshotSource:
		return legacyDisabledResult(cmd.Type), nil
	case internal.CommandStageFile:
		return legacyDisabledResult(cmd.Type), nil
	default:
		return nil, fmt.Errorf("unknown command type: %s", cmd.Type)
	}
}

func (m *manager) resolveTenantID(sourceID, bindingID, fallback string) string {
	if fallback != "" {
		return fallback
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if entry, ok := m.runtimes[runtimeKey(sourceID, bindingID)]; ok && entry.runtime.TenantID != "" {
		return entry.runtime.TenantID
	}
	for _, entry := range m.runtimes {
		if entry.runtime.SourceID == sourceID && entry.runtime.TenantID != "" {
			return entry.runtime.TenantID
		}
	}
	return m.cfg.TenantID
}

func (m *manager) setStatus(sourceID string, status internal.SourceRuntimeStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.runtimes[sourceID]; ok {
		e.runtime.Status = status
	}
}

func (m *manager) setWatcherEnabled(sourceID string, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.runtimes[sourceID]; ok {
		e.runtime.WatcherEnabled = enabled
	}
}

func (m *manager) ensureSourceDirs(sourceID string) error {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return fmt.Errorf("source_id is required")
	}
	dirs := []string{}
	if m.cfg.Staging.Enabled && strings.TrimSpace(m.cfg.Staging.HostRoot) != "" {
		dirs = append(dirs, filepath.Join(m.cfg.Staging.HostRoot, "sources", sourceID, "files"))
	}
	if strings.TrimSpace(m.cfg.LogDir) != "" {
		dirs = append(dirs, filepath.Join(m.cfg.LogDir, "sources", sourceID))
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create source scoped dir %s failed: %w", dir, err)
		}
	}
	return nil
}

// Stats returns runtime statistics for heartbeat reporting.
func (m *manager) Stats() (sourceCount, watchCount, taskCount int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sources := make(map[string]struct{}, len(m.runtimes))
	for _, entry := range m.runtimes {
		sources[entry.runtime.SourceID] = struct{}{}
		health := m.watcher.Health(entry.runtime.SourceID, entry.runtime.BindingID)
		if health.Enabled && health.Healthy {
			watchCount++
		}
	}
	sourceCount = len(sources)
	return
}

func runtimeKey(sourceID, bindingID string) string {
	if bindingID == "" {
		return sourceID
	}
	return sourceID + "\x00" + bindingID
}

func legacyDisabledResult(commandType internal.CommandType) map[string]any {
	return map[string]any{
		"accepted": false,
		"code":     "LEGACY_DISABLED",
		"message":  fmt.Sprintf("%s is disabled in file-watcher", commandType),
	}
}
