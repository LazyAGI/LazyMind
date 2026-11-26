package agentintegration

import "lazymind/agentconnector/internal/workflowconnector"

func SetRuntimeIssue(status *Status, code string) {
	if status.Connection == nil {
		status.Connection = &ConnectionStatus{PresentationStatus: "host-offline"}
	}
	// Native trust and enablement remain prerequisites across host restarts.
	if status.Issue != nil && (status.Issue.Code == "mcp_trust_required" || status.Issue.Code == "mcp_enable_required") {
		issue, message := status.Issue, status.Message
		defer func() { status.Issue, status.Message = issue, message }()
	}
	switch code {
	case "connection_required":
		status.Connection.RuntimeState = "connection_required"
		status.Connection.ControlReady = false
		status.Issue = nil
		status.Message = "Open the Codex installation guide in LazyMind to connect before executing a Workflow."
	case "waiting_for_app":
		status.Connection.RuntimeState = "waiting"
		status.Connection.ControlReady = false
		status.Issue = nil
		status.Message = "Waiting for the application to start."
	case "":
		if status.Connection.ControlReady {
			status.Connection.RuntimeState = "connected"
			return
		}
		status.Connection.RuntimeState = "unavailable"
		if status.Issue == nil {
			status.Issue = &Issue{Code: "host_unavailable"}
			status.Message = "The connector is unavailable. Turn the switch off and on again."
		}
	default:
		status.Connection.RuntimeState = "unavailable"
		status.Connection.ControlReady = false
		status.Issue = &Issue{Code: code}
		status.Message = "Startup or control connection is unavailable. Turn the switch off and on again."
	}
}

// SetLazyMindIssue reports Core connectivity alongside the existing native and
// MCP checks. An outage cannot make a healthy host control probe fail.
func SetLazyMindIssue(status *Status, code string) {
	switch code {
	case "":
		status.Connection.LazyMindState = "connected"
	case workflowconnector.ConnectionPending:
		status.Connection.LazyMindState = "pending"
	case "lazymind_unavailable":
		status.Connection.LazyMindState = "unavailable"
	default:
		status.Connection.LazyMindState = "authorization_required"
	}
	if code != "" && status.Issue == nil {
		status.Issue = &Issue{Code: code}
	}
}
