package chatagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImageAttachmentsSinceDoesNotReplayOlderImages(t *testing.T) {
	directory := t.TempDir()
	oldPath := filepath.Join(directory, "old.png")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now()
	newPath := filepath.Join(directory, "new.webp")
	if err := os.WriteFile(newPath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachments, err := ImageAttachmentsSince(directory, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].Filename != "new.webp" || attachments[0].Path != newPath {
		t.Fatalf("attachments=%#v", attachments)
	}
}
