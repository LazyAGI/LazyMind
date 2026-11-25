package codexhost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lazymind/agentconnector/internal/workflowconnector"
)

// ControlIssue reports whether the host exposes session control.
func ControlIssue(status Status) string {
	switch status.Issue {
	case "cdp_unavailable", "capability_unavailable", "control_window_unavailable", "host_unavailable":
		return status.Issue
	}
	if !status.Ready || status.Issue != "" {
		return "host_unavailable"
	}
	return ""
}

// WaitReady requires evidence produced after this connection's desktop startup.
// Like WorkBuddy, transient initialization failures are retried, while revoked
// pairing fails immediately. A fresh heartbeat is also checked against the live
// desktop so an in-flight probe from the previous process cannot admit setup.
func WaitReady(ctx context.Context, home, account string, since time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastIssue := "host_unavailable"
	for {
		if err := ctx.Err(); err != nil {
			return setupError(lastIssue, err)
		}
		config, pair, err := Load(home, account)
		if err != nil {
			return err
		}
		status, err := readStatus(config, pair)
		lastIssue = "host_unavailable"
		if err == nil && status.CheckedAt >= since.UnixMilli() {
			lastIssue = ControlIssue(status)
			if lastIssue == "" {
				lastIssue = status.ConnectionEvidence.Issue(pair.RuntimeRevision, status.CheckedAt, since)
				if lastIssue != "" && !workflowconnector.ConnectionRetryable(lastIssue) {
					return setupError(lastIssue, errors.New("Codex Workflow connection authorization failed"))
				}
			}
			if lastIssue == "" {
				if err := CheckConnection(ctx, home); err == nil {
					// The awaited live probe must not outlive pairing authorization.
					config, pair, err := Load(home, account)
					if err != nil {
						return err
					}
					lastIssue = "host_unavailable"
					if current, err := readStatus(config, pair); err == nil && current.CheckedAt >= since.UnixMilli() {
						lastIssue = ControlIssue(current)
						if lastIssue == "" {
							lastIssue = current.ConnectionEvidence.Issue(pair.RuntimeRevision, current.CheckedAt, since)
							if lastIssue == "" {
								return nil
							}
						}
					}
				} else {
					lastIssue = SetupIssue(err)
					var issue *ConnectionIssue
					if lastIssue == "" && errors.As(err, &issue) {
						lastIssue = "cdp_unavailable"
						switch issue.Code {
						case "CODEX_CONFIGURATION_UNAVAILABLE":
							return setupError("startup_unavailable", err)
						case "CODEX_CONTROL_UNAVAILABLE":
							lastIssue = "capability_unavailable"
						}
					}
					if lastIssue == "" {
						lastIssue = "cdp_unavailable"
					}
					if lastIssue == "runtime_unavailable" || lastIssue == "startup_unavailable" || lastIssue == "configuration_failed" {
						return setupError(lastIssue, err)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return setupError(lastIssue, fmt.Errorf("Codex session control did not become ready: %w", ctx.Err()))
		case <-ticker.C:
		}
	}
}
