package mcpclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workbuddyhost"
)

func lockWorkBuddyOperation(ctx context.Context) (func(), error) {
	path := configPath(WorkBuddy) + ".lazymind-operation-lock"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return localfile.LockContext(bounded, path)
}

func (a *Adapter) verifyWorkBuddyRegistration() (bool, error) {
	_, servers, err := readJSONConfig(configPath(WorkBuddy))
	if err != nil {
		return false, err
	}
	if err := checkWorkBuddyRegistration(servers, a.home, a.hostID); err != nil {
		return false, err
	}
	_, registered := servers[serverName]
	return registered, nil
}

func markWorkBuddyRegistrationConflict(status *agentintegration.Status) {
	enabled := false
	status.Enabled = &enabled
	status.State = agentintegration.Conflict
	status.Issue = &agentintegration.Issue{Code: "environment_mismatch"}
	status.Message = "WorkBuddy is linked to another LazyMind deployment. Disconnect it from that deployment before connecting here."
	status.CanConnect = false
	status.CanDisconnect = false
}

func workBuddyRegistrationFailure(err error) agentintegration.Status {
	status := agentintegration.Status{Agent: "workbuddy", DisplayName: "WorkBuddy"}
	if errors.Is(err, errWorkBuddyRegistrationOccupied) {
		markWorkBuddyRegistrationConflict(&status)
		return status
	}
	return workBuddyFailure(status, err)
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
