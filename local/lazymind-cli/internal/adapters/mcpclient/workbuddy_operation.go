package mcpclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workbuddyhost"
)

func lockWorkBuddyOperation(ctx context.Context, home string) (func(), error) {
	dir := filepath.Join(home, "workbuddy-workflow")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return localfile.LockContext(bounded, filepath.Join(dir, "operation.lock"))
}

func workBuddyLockError(err error) error {
	code := "configuration_failed"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "operation_busy"
	}
	if errors.Is(err, context.Canceled) {
		code = "operation_cancelled"
	}
	return &workbuddyhost.SetupError{Code: code, Err: err}
}

type workBuddyOperation struct {
	home, id, stage string
	started         time.Time
}

func newWorkBuddyOperation(home string) *workBuddyOperation {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return &workBuddyOperation{home: home, id: hex.EncodeToString(id[:]), started: time.Now()}
}

func (o *workBuddyOperation) enter(stage string) { o.stage = stage; o.record("running", "") }

func (o *workBuddyOperation) finish(status *agentintegration.Status) {
	code := ""
	if status.Issue != nil {
		status.Issue.Stage, status.Issue.OperationID = o.stage, o.id
		code = status.Issue.Code
	}
	o.record(string(status.State), code)
}

func (o *workBuddyOperation) record(state, code string) {
	dir := filepath.Join(o.home, "logs")
	if os.MkdirAll(dir, 0700) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "agent-setup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	// Strict allowlist: never include CLI output, tokens or config documents.
	_ = json.NewEncoder(f).Encode(map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano), "agent": "workbuddy", "operation_id": o.id,
		"stage": o.stage, "state": state, "code": code, "elapsed_ms": time.Since(o.started).Milliseconds()})
}
