package agentintegration

import "strings"

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
	Code string `json:"code"`
	Path string `json:"path,omitempty"`
}

type LoginStatus struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

type Status struct {
	Enabled       *bool             `json:"enabled,omitempty"`
	Login         *LoginStatus      `json:"login,omitempty"`
	Connection    *ConnectionStatus `json:"connection,omitempty"`
	RetryAction   string            `json:"retry_action,omitempty"`
	Issue         *Issue            `json:"issue,omitempty"`
	CanConnect    bool              `json:"can_connect,omitempty"`
	CanDisconnect bool              `json:"can_disconnect,omitempty"`
	Profiles      []ProfileStatus   `json:"profiles,omitempty"`
	Agent         string            `json:"agent"`
	DisplayName   string            `json:"display_name"`
	State         State             `json:"state"`
	Requirements  []Requirement     `json:"requirements,omitempty"`
	Action        *Action           `json:"action,omitempty"`
	Message       string            `json:"message,omitempty"`
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

// Connection reports runtime health separately from the user's enabled setting.
type ConnectionStatus struct {
	RuntimeState       string `json:"runtime_state,omitempty"`
	MCPTrusted         bool   `json:"mcp_trusted"`
	MCPEnabled         bool   `json:"mcp_enabled"`
	MCPConfigured      bool   `json:"mcp_configured"`
	ControlReady       bool   `json:"control_ready"`
	PresentationStatus string `json:"presentation_status"`
	PageStatus         string `json:"page_status,omitempty"`
}
