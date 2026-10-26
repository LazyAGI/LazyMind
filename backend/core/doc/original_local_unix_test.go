//go:build !windows

package doc

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestKnowledgeParityRejectsFIFOWithoutBlocking(t *testing.T) {
	db := newDocumentTestDB(t)
	svc, err := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pipe.txt")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.AcquireLocalOriginal(context.Background(), DatasetCatalogCaller{UserID: "user"}, path, 1024)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("opening FIFO blocked")
	}
}
