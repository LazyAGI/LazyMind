package workflowconnector

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"lazymind/agentconnector/internal/localfile"
)

// Worker lets the already-loaded JavaScript clean up its panels before
// termination, including when a local edit prevents the new entry from loading.
type Worker struct {
	Done     <-chan error
	cancel   context.CancelFunc
	stopFile string
}

func (i Installation) StartWorker(ctx context.Context, home, reload string, command func(context.Context, string) *exec.Cmd) (*Worker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	stopFile := filepath.Join(filepath.Dir(RecordPath(home, i.Host)), "connector-stop-"+hex.EncodeToString(nonce[:]))
	file, err := os.OpenFile(filepath.Join(logDir, i.Host+"-workflow.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cmd := command(child, stopFile)
	cmd.Stdout, cmd.Stderr = file, file
	cmd.WaitDelay = 3 * time.Second
	if err := i.ConfigureCommand(cmd, reload); err != nil {
		cancel()
		file.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		file.Close()
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		cancel()
		file.Close()
		done <- err
	}()
	return &Worker{Done: done, cancel: cancel, stopFile: stopFile}, nil
}

func (w *Worker) Stop() bool {
	defer os.Remove(w.stopFile)
	if err := localfile.Write(w.stopFile, nil, 0600); err == nil {
		timer := time.NewTimer(4 * time.Second)
		defer timer.Stop()
		select {
		case err := <-w.Done:
			return err == nil
		case <-timer.C:
		}
	}
	w.cancel()
	<-w.Done
	return false
}
