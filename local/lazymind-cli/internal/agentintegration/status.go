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

type Status struct {
	Agent        string        `json:"agent"`
	DisplayName  string        `json:"display_name"`
	State        State         `json:"state"`
	Requirements []Requirement `json:"requirements,omitempty"`
	Action       *Action       `json:"action,omitempty"`
	Message      string        `json:"message,omitempty"`
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
