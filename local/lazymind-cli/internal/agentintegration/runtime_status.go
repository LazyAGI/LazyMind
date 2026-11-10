package agentintegration

func SetRuntimeIssue(status *Status, code string) {
	if status.Connection == nil {
		status.Connection = &ConnectionStatus{PresentationStatus: "host-offline"}
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
