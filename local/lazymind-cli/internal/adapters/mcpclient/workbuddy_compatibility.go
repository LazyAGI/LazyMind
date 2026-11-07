package mcpclient

import (
	"context"
	"errors"
	"log"
	"os"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workbuddyhost"
	"lazymind/agentconnector/internal/workflowhost"
)

func workBuddyIssueMessage(code string) string {
	switch code {
	case "unsupported_version":
		return "Update WorkBuddy to " + workbuddyhost.MinimumVersion + " or later, then retry."
	case "version_unreadable":
		return "Unable to read the WorkBuddy desktop version. Check its installation and retry."
	case "capability_unavailable":
		return "This WorkBuddy installation does not expose the required Workflow interfaces."
	case "host_unavailable":
		return "Unable to reach WorkBuddy. Keep WorkBuddy and LazyMind running, then retry."
	case "quit_required":
		return "WorkBuddy has not fully exited. Save your work, quit it from its application or tray menu, then continue installation."
	case "process_inspection_failed":
		return "Unable to inspect WorkBuddy. Run both apps as the same user, quit WorkBuddy normally, then retry."
	case "control_port_conflict":
		return "The WorkBuddy control port is occupied by another service. Resolve the port conflict, then retry."
	case "control_window_unavailable":
		return "Unable to identify one WorkBuddy main window. Open the main window and retry."
	case "launch_failed", "startup_timeout":
		return "WorkBuddy did not start with session control. Open WorkBuddy to check its startup, then retry the connection."
	case "runtime_unavailable":
		return "WorkBuddy's required execution runtime is unavailable. Open WorkBuddy and let its runtime installation finish, then retry."
	case "lazymind_auth_required":
		return "Sign in to this LazyMind instance again, then retry the connection."
	case "workbuddy_login_required":
		return "Sign in to WorkBuddy to continue the connection."
	case "workbuddy_auth_unreadable":
		return "WorkBuddy sign-in status could not be read. Check its account status and authentication file permissions, then retry."
	case "lazymind_unavailable":
		return "LazyMind MCP is unavailable. Wait for LazyMind's local services to be ready, then retry."
	case "configuration_failed":
		return "Unable to read or write the connection configuration. Check file permissions and repair the configuration before retrying."
	case "operation_busy":
		return "Another WorkBuddy connection operation is running. Wait for it to finish, then check the connection."
	case "operation_cancelled":
		return "Connection was interrupted. Check its current state before retrying."
	case "startup_unavailable":
		return "WorkBuddy startup configuration is unavailable. Repair the connection and retry."
	case "pairing_required":
		return "WorkBuddy is not paired with this LazyMind account. Repair the connection to pair it for the current account."
	case "pairing_disabled":
		return "This WorkBuddy pairing is disabled. Connect again to enable it."
	case "pairing_account_mismatch":
		return "The WorkBuddy pairing belongs to another LazyMind account. Connect again for the current account."
	case "pairing_credential_mismatch":
		return "The WorkBuddy connector is using stale pairing credentials. Repair the connection to reload its current pairing."
	case "pairing_invalid", "pairing_unreadable":
		return "The WorkBuddy pairing cannot be read or is damaged. Check its configuration and permissions; export the connection diagnostics for repair."
	case "mcp_trust_required":
		return "Trust the lazymind connector in WorkBuddy, then check again."
	case "mcp_enable_required":
		return "Enable the lazymind connector in WorkBuddy, then check again."
	default:
		return "WorkBuddy connection failed. Retry, and report the issue if it persists."
	}
}

func workBuddyFailure(status agentintegration.Status, err error) agentintegration.Status {
	code := workbuddyhost.SetupIssue(err)
	if code == "" {
		code = workBuddyPairingIssue(err)
	}
	var api *coreapi.Error
	if code == "" && (credentials.IsAuthenticationRequired(err) ||
		errors.As(err, &api) && (api.StatusCode == 401 || api.StatusCode == 403)) {
		code = "lazymind_auth_required"
	}
	if code == "" && errors.Is(err, os.ErrPermission) {
		code = "configuration_failed"
	}
	if code == "" && errors.Is(err, context.Canceled) {
		code = "operation_cancelled"
	}
	status.Issue = nil
	if code != "" {
		status.Issue = &agentintegration.Issue{Code: code}
	}
	return agentintegration.FailError(status, err)
}

func workBuddyPairingIssue(err error) string {
	var pairing *workflowhost.PairingError
	if !errors.As(err, &pairing) {
		return ""
	}
	return map[string]string{
		"HOST_PAIRING_MISSING": "pairing_required", "HOST_PAIRING_DISABLED": "pairing_disabled",
		"HOST_ACCOUNT_MISMATCH": "pairing_account_mismatch", "HOST_CREDENTIAL_MISMATCH": "pairing_credential_mismatch",
		"HOST_PAIRING_INVALID": "pairing_invalid", "HOST_PAIRING_UNREADABLE": "pairing_unreadable",
	}[pairing.Code]
}

func sanitizeWorkBuddyFailure(status *agentintegration.Status) {
	if status.Issue == nil {
		status.Issue = &agentintegration.Issue{Code: "operation_failed"}
	}
	// Raw subprocess errors can contain credentials. Persist only structured
	// categories; the per-operation log supplies stage and correlation identity.
	log.Printf("WorkBuddy integration: code=%s stage=%s operation=%s", status.Issue.Code, status.Issue.Stage, status.Issue.OperationID)
	status.Message = workBuddyIssueMessage(status.Issue.Code)
}
