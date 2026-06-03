package source

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	internal "github.com/lazymind/file_watcher/internal"
	"github.com/lazymind/file_watcher/internal/config"
	"github.com/lazymind/file_watcher/internal/fs"
)

type startCall struct {
	sourceID string
	tenantID string
	root     string
}

type watcherStub struct {
	mu      sync.Mutex
	started map[string]startCall
	startCh chan startCall
}

func newWatcherStub() *watcherStub {
	return &watcherStub{
		started: make(map[string]startCall),
		startCh: make(chan startCall, 4),
	}
}

func (w *watcherStub) Start(_ context.Context, sourceID, tenantID, root string) error {
	call := startCall{sourceID: sourceID, tenantID: tenantID, root: root}
	w.mu.Lock()
	w.started[sourceID] = call
	w.mu.Unlock()
	w.startCh <- call
	return nil
}

func (w *watcherStub) Stop(sourceID string) error {
	w.mu.Lock()
	delete(w.started, sourceID)
	w.mu.Unlock()
	return nil
}

func (w *watcherStub) Health(sourceID string) fs.WatcherHealth {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.started[sourceID]
	return fs.WatcherHealth{Enabled: ok, Healthy: ok}
}

type validatorStub struct{}

func (validatorStub) EnsureAllowed(string) error {
	return nil
}

func TestStopSourceIsIdempotent(t *testing.T) {
	t.Parallel()

	mgr := NewManager(
		&config.Config{AgentID: "agent-1", TenantID: "tenant-default"},
		newWatcherStub(),
		validatorStub{},
		fs.NewPathMapper("", nil),
		zap.NewNop(),
	)

	if err := mgr.StopSource(context.Background(), "src-missing"); err != nil {
		t.Fatalf("expected stopping a missing source to be a no-op, got %v", err)
	}
}

func TestHandleCommandUsesCommandTenantID(t *testing.T) {
	t.Parallel()

	watcher := newWatcherStub()
	mgr := NewManager(
		&config.Config{AgentID: "agent-1", TenantID: "tenant-default"},
		watcher,
		validatorStub{},
		fs.NewPathMapper("", nil),
		zap.NewNop(),
	)

	rootPath := t.TempDir()
	_, err := mgr.HandleCommand(context.Background(), internal.Command{
		Type:     internal.CommandStartSource,
		SourceID: "src-1",
		TenantID: "tenant-cmd",
		RootPath: rootPath,
	})
	if err != nil {
		t.Fatalf("handle start command: %v", err)
	}

	select {
	case call := <-watcher.startCh:
		if call.tenantID != "tenant-cmd" {
			t.Fatalf("expected tenant tenant-cmd, got %q", call.tenantID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher start")
	}

	runtimes := mgr.ListRuntimes()
	if len(runtimes) != 1 {
		t.Fatalf("expected 1 runtime, got %d", len(runtimes))
	}
	if runtimes[0].TenantID != "tenant-cmd" {
		t.Fatalf("expected runtime tenant tenant-cmd, got %q", runtimes[0].TenantID)
	}

	if err := mgr.StopSource(context.Background(), "src-1"); err != nil {
		t.Fatalf("stop source: %v", err)
	}
}

func TestHandleCommandFallsBackToAgentTenantID(t *testing.T) {
	t.Parallel()

	watcher := newWatcherStub()
	mgr := NewManager(
		&config.Config{AgentID: "agent-1", TenantID: "tenant-default"},
		watcher,
		validatorStub{},
		fs.NewPathMapper("", nil),
		zap.NewNop(),
	)

	rootPath := t.TempDir()
	_, err := mgr.HandleCommand(context.Background(), internal.Command{
		Type:     internal.CommandStartSource,
		SourceID: "src-2",
		RootPath: rootPath,
	})
	if err != nil {
		t.Fatalf("handle start command: %v", err)
	}

	select {
	case call := <-watcher.startCh:
		if call.tenantID != "tenant-default" {
			t.Fatalf("expected tenant tenant-default, got %q", call.tenantID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher start")
	}

	if err := mgr.StopSource(context.Background(), "src-2"); err != nil {
		t.Fatalf("stop source: %v", err)
	}
}

func TestStartSourceMapsPublicRootToRuntimeRoot(t *testing.T) {
	t.Parallel()

	watcher := newWatcherStub()
	runtimeRoot := t.TempDir()
	mapper := fs.NewPathMapper("posix", []config.PathMapping{
		{PublicRoot: "/host/docs", RuntimeRoot: runtimeRoot},
	})
	mgr := NewManager(
		&config.Config{AgentID: "agent-1", TenantID: "tenant-default"},
		watcher,
		validatorStub{},
		mapper,
		zap.NewNop(),
	)

	if err := mgr.StartSource(context.Background(), internal.StartSourceRequest{
		SourceID:        "src-map",
		RootPath:        "/host/docs",
		SkipInitialScan: true,
	}); err != nil {
		t.Fatalf("start source: %v", err)
	}

	select {
	case call := <-watcher.startCh:
		if call.root != runtimeRoot {
			t.Fatalf("expected watcher root %q, got %q", runtimeRoot, call.root)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher start")
	}

	runtimes := mgr.ListRuntimes()
	if len(runtimes) != 1 {
		t.Fatalf("expected 1 runtime, got %d", len(runtimes))
	}
	if runtimes[0].RootPath != "/host/docs" {
		t.Fatalf("expected public runtime root, got %q", runtimes[0].RootPath)
	}
	if err := mgr.StopSource(context.Background(), "src-map"); err != nil {
		t.Fatalf("stop source: %v", err)
	}
}

func TestLegacyDocumentCommandsAreV2Disabled(t *testing.T) {
	t.Parallel()

	mgr := NewManager(
		&config.Config{AgentID: "agent-1", TenantID: "tenant-default"},
		newWatcherStub(),
		validatorStub{},
		fs.NewPathMapper("", nil),
		zap.NewNop(),
	)

	for _, commandType := range []internal.CommandType{
		internal.CommandScanSource,
		internal.CommandSnapshotSource,
		internal.CommandStageFile,
	} {
		result, err := mgr.HandleCommand(context.Background(), internal.Command{Type: commandType, SourceID: "src-1"})
		if err != nil {
			t.Fatalf("expected %s to be compatibility-acked, got error %v", commandType, err)
		}
		payload, ok := result.(map[string]any)
		if !ok || payload["code"] != "V2_DISABLED" || payload["accepted"] != false {
			t.Fatalf("expected v2-disabled result for %s, got %#v", commandType, result)
		}
	}
}
