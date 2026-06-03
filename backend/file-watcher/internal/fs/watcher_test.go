package fs

import (
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	internal "github.com/lazymind/file_watcher/internal"
)

func TestWatcherIgnoresTransientEditorFileEvents(t *testing.T) {
	t.Parallel()

	rw := &recursiveWatcher{log: zap.NewNop()}
	scheduled := 0
	rw.handleFsEvent(fsnotify.Event{
		Name: filepath.Join(t.TempDir(), ".test2.txt.swp"),
		Op:   fsnotify.Create,
	}, nil, func(string, internal.FileEventType, bool) {
		scheduled++
	})

	if scheduled != 0 {
		t.Fatalf("expected transient file event to be ignored, scheduled=%d", scheduled)
	}
}

func TestWatcherEventObjectKeyMatchesLocalFSConnectorPathKey(t *testing.T) {
	t.Parallel()

	got := pathObjectKey("agent-1", filepath.Join("/workspace", "docs", "a.md"))
	want := "local_fs:agent-1:path:/workspace/docs/a.md"
	if got != want {
		t.Fatalf("object key = %q, want %q", got, want)
	}
}
