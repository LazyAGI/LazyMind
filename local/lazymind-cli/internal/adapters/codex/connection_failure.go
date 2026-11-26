package codex

import (
	"context"
	"errors"
	"os"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func codexConnectionFailure(status agentintegration.Status, err error) agentintegration.Status {
	code := codexhost.SetupIssue(err)
	if code == "quit_required" {
		return agentintegration.RequireQuit(status, "connect")
	}
	if issue := workflowhost.PairingIssue(err); issue != "" {
		code = issue
	}
	if credentials.IsAuthenticationRequired(err) {
		code = "lazymind_auth_required"
	}
	if errors.Is(err, context.Canceled) {
		code = "operation_cancelled"
	}
	if code == "" && errors.Is(err, context.DeadlineExceeded) {
		code = "startup_timeout"
	}
	if code == "" && (errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist)) {
		code = "configuration_failed"
	}
	if code == "" {
		code = "host_unavailable"
	}
	status.Issue = &agentintegration.Issue{Code: code}
	status = agentintegration.FailError(status, err)
	// Keep raw subprocess output out of the API; phase, code and diagnostic ID
	// identify the failure without exposing local command output or credentials.
	status.Message = "Codex connection failed (" + code + "). Retry and report the diagnostic ID if the problem persists."
	return status
}
