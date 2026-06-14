package fs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestFileEventOccurredAtUsesFileModTime(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	want := time.Date(2026, 6, 13, 10, 11, 12, 0, time.UTC)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	got := fileEventOccurredAt(path)

	if !got.Equal(want) {
		t.Fatalf("occurred_at = %v, want file mtime %v", got, want)
	}
}
