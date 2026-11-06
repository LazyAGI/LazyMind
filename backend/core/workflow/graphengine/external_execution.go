package graphengine

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

const ExternalExecutionVersion = "1"

// ExternalExecutionPolicy is a revision-owned opt-in. Native instructions and
// tools remain unchanged; external execution never falls back to a native model.
type ExternalExecutionPolicy struct {
	Version     string                           `json:"version" yaml:"version"`
	Skills      []string                         `json:"skills,omitempty" yaml:"skills,omitempty"`
	Constraints []string                         `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Steps       map[string]ExternalStepExecution `json:"steps" yaml:"steps"`
}

type ExternalTool struct {
	Name   string `json:"name" yaml:"name"`
	Source string `json:"source" yaml:"source"`
}

// ExternalStepExecution is resolved from the immutable package revision.
// Validation belongs to the external agent; Core checks access and consistency.
type ExternalStepExecution struct {
	Version         string         `json:"version,omitempty" yaml:"-"`
	ValidationOwner string         `json:"validation_owner,omitempty" yaml:"-"`
	Prompt          string         `json:"prompt" yaml:"prompt"`
	Skills          []string       `json:"skills,omitempty" yaml:"skills,omitempty"`
	Constraints     []string       `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Tools           []ExternalTool `json:"tools,omitempty" yaml:"tools,omitempty"`
	// Nil inherits the native data contract; an explicit empty list clears it.
	Acceptance      []string `json:"acceptance_criteria" yaml:"acceptance_criteria"`
	RequiredOutputs []string `json:"required_outputs" yaml:"required_outputs"`
}

func (e *ExternalStepExecution) Supported() bool {
	return e != nil && e.Version == ExternalExecutionVersion && e.ValidationOwner == "external-agent" && strings.TrimSpace(e.Prompt) != ""
}

var externalToolName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]*$`)

// ValidExternalSkillPath accepts only package-relative files, never host paths.
func ValidExternalSkillPath(value string) bool {
	return value != "" && value != "." && value != ".." && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\\:\x00\r\n") && !strings.HasPrefix(value, "/") &&
		!strings.HasPrefix(value, "../") && path.Clean(value) == value
}

func compileExternalExecution(graph *CompiledStateGraph) []Diagnostic {
	policy := graph.Runtime.ExternalExecution
	if policy == nil {
		return nil
	}
	var diagnostics []Diagnostic
	invalid := func(field, message string) {
		diagnostics = append(diagnostics, diag("E_EXTERNAL_EXECUTION_INVALID", "error", "workflow.yaml.runtime.external_execution"+field, message))
	}
	if policy.Version != ExternalExecutionVersion {
		invalid(".version", "external execution version must be 1")
	}
	for stepID := range policy.Steps {
		if _, exists := graph.Nodes[stepID]; !exists {
			invalid(".steps."+stepID, "external execution references an unknown step")
		}
	}
	for stepID, node := range graph.Nodes {
		step, exists := policy.Steps[stepID]
		field := ".steps." + stepID
		if !exists || strings.TrimSpace(step.Prompt) == "" {
			invalid(field+".prompt", "every step requires an explicit external execution prompt")
			continue
		}
		step.Version, step.ValidationOwner = policy.Version, "external-agent"
		step.Prompt = strings.TrimSpace(step.Prompt)
		step.Skills = append(append([]string{}, policy.Skills...), step.Skills...)
		step.Constraints = append(append([]string{}, policy.Constraints...), step.Constraints...)
		skills := map[string]bool{}
		uniqueSkills := make([]string, 0, len(step.Skills))
		for i, skill := range step.Skills {
			if !ValidExternalSkillPath(skill) {
				invalid(fmt.Sprintf("%s.skills[%d]", field, i), "skill must be a safe package-relative file path")
			}
			if !skills[skill] {
				uniqueSkills = append(uniqueSkills, skill)
				skills[skill] = true
			}
		}
		step.Skills = uniqueSkills
		for i, constraint := range append(append([]string{}, step.Constraints...), step.Acceptance...) {
			if strings.TrimSpace(constraint) == "" {
				invalid(fmt.Sprintf("%s.constraints[%d]", field, i), "constraint or acceptance criterion must not be empty")
			}
		}
		names := map[string]bool{}
		for i, tool := range step.Tools {
			if !externalToolName.MatchString(tool.Name) || names[tool.Name] || (tool.Source != "package" && tool.Source != "global") {
				invalid(fmt.Sprintf("%s.tools[%d]", field, i), "tools require a unique name and package or global source")
			}
			names[tool.Name] = true
		}
		outputs := map[string]bool{}
		for _, output := range node.Outputs {
			outputs[output] = true
		}
		required := map[string]bool{}
		for _, output := range step.RequiredOutputs {
			if !outputs[output] || required[output] {
				invalid(field+".required_outputs", "required outputs must name distinct outputs declared by this step")
			}
			required[output] = true
		}
		node.ExternalExecution = &step
		graph.Nodes[stepID] = node
	}
	return diagnostics
}
