package mcpclient

import (
	"errors"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/workbuddyhost"
	"log"
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
	default:
		return "WorkBuddy connection failed. Retry, and report the issue if it persists."
	}
}

func workBuddyFailure(status agentintegration.Status, err error) agentintegration.Status {
	var compatibility *workbuddyhost.CompatibilityError
	if errors.As(err, &compatibility) {
		status.Issue = &agentintegration.Issue{Code: compatibility.Code}
	} else {
		status.Issue = nil
	}
	return agentintegration.Fail(status, err.Error())
}

func sanitizeWorkBuddyFailure(status *agentintegration.Status) {
	log.Printf("WorkBuddy integration: %s", status.Message)
	if status.Issue == nil {
		status.Issue = &agentintegration.Issue{Code: "operation_failed"}
	}
	status.Message = workBuddyIssueMessage(status.Issue.Code)
}
