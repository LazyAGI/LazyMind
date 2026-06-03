package fs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	internal "github.com/lazymind/file_watcher/internal"
	"github.com/lazymind/file_watcher/internal/config"
)

// RecursiveWatcher defines the recursive file watching interface.
type RecursiveWatcher interface {
	Start(ctx context.Context, sourceID, tenantID, root string) error
	Stop(sourceID string) error
	Health(sourceID string) WatcherHealth
}

// EventReporter reports file events.
type EventReporter interface {
	ReportEvents(ctx context.Context, req internal.ReportEventsRequest) error
}

type WatcherHealth struct {
	Enabled     bool
	Healthy     bool
	LastEventAt time.Time
	LastError   string
}

type watcherEntry struct {
	cancel      context.CancelFunc
	watcher     *fsnotify.Watcher
	tenantID    string
	running     bool
	lastEventAt time.Time
	lastError   string
}

type recursiveWatcher struct {
	cfg      config.WatchConfig
	agentID  string
	reporter EventReporter
	mapper   PathMapper
	log      *zap.Logger

	mu      sync.Mutex
	entries map[string]*watcherEntry // sourceID -> entry
}

func NewRecursiveWatcher(agentID string, cfg config.WatchConfig, reporter EventReporter, mapper PathMapper, log *zap.Logger) RecursiveWatcher {
	if mapper == nil {
		mapper = NewPathMapper("", nil)
	}
	return &recursiveWatcher{
		cfg:      cfg,
		agentID:  agentID,
		reporter: reporter,
		mapper:   mapper,
		log:      log,
		entries:  make(map[string]*watcherEntry),
	}
}

func (rw *recursiveWatcher) Start(ctx context.Context, sourceID, tenantID, root string) error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if _, exists := rw.entries[sourceID]; exists {
		return nil // Already watching.
	}

	// Create a watcher first to verify root is accessible before starting the goroutine.
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := addRecursive(fw, root); err != nil {
		_ = fw.Close()
		return err
	}
	_ = fw.Close() // Re-created inside runWithRestart for unified lifecycle management.

	watchCtx, cancel := context.WithCancel(ctx)
	entry := &watcherEntry{cancel: cancel, watcher: nil, tenantID: tenantID}
	rw.entries[sourceID] = entry

	go rw.runWithRestart(watchCtx, sourceID, tenantID, root)
	rw.log.Info("watcher started", zap.String("source_id", sourceID), zap.String("root", root))
	return nil
}

// runWithRestart rebuilds the watcher after unexpected loop exits using exponential backoff.
func (rw *recursiveWatcher) runWithRestart(ctx context.Context, sourceID, tenantID, root string) {
	const maxBackoff = 60 * time.Second
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		fw, err := fsnotify.NewWatcher()
		if err != nil {
			rw.markUnhealthy(sourceID, err.Error())
			rw.log.Error("watcher rebuild failed", zap.String("source_id", sourceID), zap.Error(err))
		} else {
			if err := addRecursive(fw, root); err != nil {
				rw.markUnhealthy(sourceID, err.Error())
				rw.log.Error("watcher addRecursive failed", zap.String("source_id", sourceID), zap.Error(err))
				_ = fw.Close()
			} else {
				// Update the watcher reference in the entry.
				rw.mu.Lock()
				if e, ok := rw.entries[sourceID]; ok {
					if e.watcher != nil && e.watcher != fw {
						_ = e.watcher.Close()
					}
					e.watcher = fw
					e.running = true
					e.lastError = ""
				}
				rw.mu.Unlock()

				rw.log.Info("watcher loop running", zap.String("source_id", sourceID))
				rw.loop(ctx, sourceID, tenantID, fw)

				// Do not rebuild when the loop exits normally due to ctx cancellation.
				if ctx.Err() != nil {
					_ = fw.Close()
					return
				}

				rw.log.Warn("watcher loop exited unexpectedly, rebuilding",
					zap.String("source_id", sourceID),
					zap.Duration("backoff", backoff),
				)
				rw.markUnhealthy(sourceID, "watcher loop exited unexpectedly")
				_ = fw.Close()
				backoff = min(backoff*2, maxBackoff)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (rw *recursiveWatcher) Stop(sourceID string) error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	entry, ok := rw.entries[sourceID]
	if !ok {
		return nil
	}
	entry.cancel()
	entry.running = false
	entry.lastError = ""
	if entry.watcher != nil {
		_ = entry.watcher.Close()
	}
	delete(rw.entries, sourceID)
	rw.log.Info("watcher stopped", zap.String("source_id", sourceID))
	return nil
}

func (rw *recursiveWatcher) Health(sourceID string) WatcherHealth {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	entry, ok := rw.entries[sourceID]
	if !ok {
		return WatcherHealth{}
	}
	return WatcherHealth{
		Enabled:     true,
		Healthy:     entry.running && entry.watcher != nil,
		LastEventAt: entry.lastEventAt,
		LastError:   entry.lastError,
	}
}

// loop is the per-Source watcher main loop with built-in debounce.
func (rw *recursiveWatcher) loop(ctx context.Context, sourceID, tenantID string, fw *fsnotify.Watcher) {
	defer func() {
		rw.mu.Lock()
		defer rw.mu.Unlock()
		if e, ok := rw.entries[sourceID]; ok {
			e.running = false
			if ctx.Err() != nil {
				e.lastError = ""
			}
		}
	}()

	// debounce: path -> (eventType, timer)
	type pending struct {
		eventType internal.FileEventType
		isDir     bool
		timer     *time.Timer
	}
	debounced := make(map[string]*pending)
	var mu sync.Mutex

	flush := func(path string, et internal.FileEventType, isDir bool) {
		publicPath := rw.mapper.ToPublic(path)
		ev := internal.FileEvent{
			SourceID:   sourceID,
			TenantID:   tenantID,
			EventType:  et,
			Path:       publicPath,
			ObjectKey:  pathObjectKey(rw.agentID, publicPath),
			IsDir:      isDir,
			OccurredAt: time.Now(),
		}
		if err := rw.reporter.ReportEvents(ctx, internal.ReportEventsRequest{
			AgentID: rw.agentID,
			Events:  []internal.FileEvent{ev},
		}); err != nil {
			rw.log.Warn("report events failed", zap.String("source_id", sourceID), zap.Error(err))
		} else {
			rw.log.Debug("reported debounced event",
				zap.String("source_id", sourceID),
				zap.String("path", publicPath),
				zap.String("type", string(et)),
				zap.Bool("is_dir", isDir),
			)
		}
	}

	schedule := func(path string, et internal.FileEventType, isDir bool) {
		mu.Lock()
		defer mu.Unlock()
		if p, ok := debounced[path]; ok {
			p.timer.Reset(rw.cfg.DebounceWindow)
			p.eventType = et
			rw.log.Debug("debounce event merged",
				zap.String("source_id", sourceID),
				zap.String("path", path),
				zap.String("type", string(et)),
			)
			return
		}
		p := &pending{eventType: et, isDir: isDir}
		p.timer = time.AfterFunc(rw.cfg.DebounceWindow, func() {
			mu.Lock()
			delete(debounced, path)
			mu.Unlock()
			flush(path, p.eventType, p.isDir)
		})
		debounced[path] = p
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-fw.Events:
			if !ok {
				rw.markUnhealthy(sourceID, "watcher events channel closed")
				return
			}
			rw.markEvent(sourceID)
			rw.handleFsEvent(ev, fw, schedule)
		case err, ok := <-fw.Errors:
			if !ok {
				rw.markUnhealthy(sourceID, "watcher error channel closed")
				return
			}
			rw.markUnhealthy(sourceID, err.Error())
			rw.log.Error("watcher error", zap.String("source_id", sourceID), zap.Error(err))
		}
	}
}

func (rw *recursiveWatcher) markEvent(sourceID string) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if e, ok := rw.entries[sourceID]; ok {
		e.running = true
		e.lastEventAt = time.Now()
		e.lastError = ""
	}
}

func (rw *recursiveWatcher) markUnhealthy(sourceID, message string) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if e, ok := rw.entries[sourceID]; ok {
		e.running = false
		if message != "" {
			e.lastError = message
		}
	}
}

func (rw *recursiveWatcher) handleFsEvent(
	ev fsnotify.Event,
	fw *fsnotify.Watcher,
	schedule func(string, internal.FileEventType, bool),
) {
	isDir := isDirectory(ev.Name)
	if isTransientFile(ev.Name, isDir) {
		rw.log.Debug("ignored transient file event",
			zap.String("path", ev.Name),
			zap.String("op", ev.Op.String()),
		)
		return
	}

	switch {
	case ev.Op&fsnotify.Create != 0:
		if isDir {
			_ = addRecursive(fw, ev.Name)
		}
		schedule(ev.Name, internal.FileCreated, isDir)

	case ev.Op&fsnotify.Write != 0:
		schedule(ev.Name, internal.FileModified, isDir)

	case ev.Op&fsnotify.Remove != 0:
		_ = fw.Remove(ev.Name)
		schedule(ev.Name, internal.FileDeleted, isDir)

	case ev.Op&fsnotify.Rename != 0:
		// P0: Treat rename as delete; the new path will trigger a Create event.
		_ = fw.Remove(ev.Name)
		schedule(ev.Name, internal.FileDeleted, isDir)

		// Ignore Chmod.
	}
}

// addRecursive registers the directory tree recursively with fsnotify.Watcher.
func addRecursive(fw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return fw.Add(path)
		}
		return nil
	})
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
