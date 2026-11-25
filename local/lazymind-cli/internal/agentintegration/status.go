package agentintegration

import (
	"fmt"
	"strings"

	"lazymind/agentconnector/internal/workflowconnector"
)

type State string

const (
	RequirementsMissing State = "requirements_missing"
	Ready               State = "ready"
	ActionRequired      State = "action_required"
	Enabled             State = "enabled"
	Conflict            State = "conflict"
	Failed              State = "error"
)

type Requirement struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Satisfied   bool   `json:"satisfied"`
}

type Action struct {
	Kind string `json:"kind"`
	URL  string `json:"url,omitempty"`
}

type Issue struct {
	Code        string `json:"code"`
	Path        string `json:"path,omitempty"`
	Stage       string `json:"stage,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

type LoginStatus struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

type Status struct {
	ConnectorRuntime  workflowconnector.Runtime `json:"-"`
	Version           string                    `json:"version,omitempty"`
	DiagnosticReason  string                    `json:"-"`
	DiagnosticID      string                    `json:"diagnostic_id,omitempty"`
	DiagnosticLog     string                    `json:"diagnostic_log,omitempty"`
	DiagnosticContext *DiagnosticContext        `json:"diagnostic_context,omitempty"`
	Enabled           *bool                     `json:"enabled,omitempty"`
	Login             *LoginStatus              `json:"login,omitempty"`
	Connection        *ConnectionStatus         `json:"connection,omitempty"`
	RetryAction       string                    `json:"retry_action,omitempty"`
	Issue             *Issue                    `json:"issue,omitempty"`
	CanConnect        bool                      `json:"can_connect,omitempty"`
	CanDisconnect     bool                      `json:"can_disconnect,omitempty"`
	Profiles          []ProfileStatus           `json:"profiles,omitempty"`
	Agent             string                    `json:"agent"`
	DisplayName       string                    `json:"display_name"`
	State             State                     `json:"state"`
	Requirements      []Requirement             `json:"requirements,omitempty"`
	Action            *Action                   `json:"action,omitempty"`
	Message           string                    `json:"message,omitempty"`
}

// DiagnosticContext describes this status snapshot, not an inferred last repair.
// It reuses allowlisted inspection data; no log contents or credentials are read.
type DiagnosticContext struct {
	Connector     *ConnectorDiagnostic `json:"connector,omitempty"`
	Action        string               `json:"action"`
	Stage         string               `json:"stage,omitempty"`
	CurrentCLI    string               `json:"current_cli,omitempty"`
	AppVersion    string               `json:"app_version,omitempty"`
	Environment   map[string]string    `json:"environment,omitempty"`
	Configuration []Inspection         `json:"configuration,omitempty"`
}

type ConnectorDiagnostic struct {
	State                  string `json:"state"`
	Version                string `json:"version,omitempty"`
	RecordPath             string `json:"record_path"`
	FailurePath            string `json:"failure_path,omitempty"`
	EntryPath              string `json:"entry_path,omitempty"`
	PresentationModulePath string `json:"presentation_module_path,omitempty"`
	ControlModulePath      string `json:"control_module_path,omitempty"`
	Loaded                 bool   `json:"loaded"`
	LoadedRevision         string `json:"loaded_revision,omitempty"`
}

type ProfileStatus struct {
	Name    string `json:"name"`
	State   State  `json:"state"`
	Message string `json:"message,omitempty"`
}

func MissingRequirement(requirements []Requirement) bool {
	for _, requirement := range requirements {
		if !requirement.Satisfied {
			return true
		}
	}
	return false
}

func RequirementSatisfied(requirements []Requirement, id string) bool {
	for _, requirement := range requirements {
		if requirement.ID == id {
			return requirement.Satisfied
		}
	}
	return false
}

func Fail(status Status, message string) Status {
	status.State = Failed
	status.Action = nil
	status.Message = strings.TrimSpace(message)
	return status
}

// RequireQuit preserves the actual configuration and connection while setup
// waits for an explicit user retry. It must not advertise installation success.
func RequireQuit(status Status, retryAction string) Status {
	if retryAction == "" {
		retryAction = "connect"
	}
	status.State = ActionRequired
	status.Action = &Action{Kind: "quit_agent"}
	status.Issue = &Issue{Code: "quit_required"}
	status.RetryAction = retryAction
	status.DiagnosticReason = ""
	status.Message = fmt.Sprintf("Save your work and fully quit %s from its application or system tray menu, then retry.", status.DisplayName)
	return status
}

// Connection reports runtime health separately from installed configuration.
type ConnectionStatus struct {
	RuntimeState       string `json:"runtime_state,omitempty"`
	LazyMindState      string `json:"lazymind_state,omitempty"`
	MCPTrusted         bool   `json:"mcp_trusted"`
	MCPEnabled         bool   `json:"mcp_enabled"`
	MCPConfigured      bool   `json:"mcp_configured"`
	ControlReady       bool   `json:"control_ready"`
	PresentationStatus string `json:"presentation_status"`
	PageStatus         string `json:"page_status,omitempty"`
}
