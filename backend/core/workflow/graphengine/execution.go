package graphengine

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExecutionPolicy declares one business contract for every executor. Step prompts,
// outputs and acceptance criteria live only in state.yml. The optional tool list
// advertises audited capabilities to hosts; native tool bindings stay on the node.
type ExecutionPolicy struct {
	Version     string                   `json:"version" yaml:"version"`
	Skills      []string                 `json:"skills,omitempty" yaml:"skills,omitempty"`
	Constraints []string                 `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Steps       map[string]StepExecution `json:"steps" yaml:"steps"`
}

type StepExecution struct {
	Version     string          `json:"version,omitempty" yaml:"-"`
	Skills      []string        `json:"skills,omitempty" yaml:"skills,omitempty"`
	Constraints []string        `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Tools       []ExecutionTool `json:"tools,omitempty" yaml:"tools,omitempty"`
}

type ExecutionTool struct {
	Name   string `json:"name" yaml:"name"`
	Source string `json:"source" yaml:"source"`
}

var executionToolName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]*$`)

// ValidExecutionSkillPath accepts only package-relative files, never host paths.
func ValidExecutionSkillPath(value string) bool {
	return value != "" && value != "." && value != ".." && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\\:\x00\r\n") && !strings.HasPrefix(value, "/") &&
		!strings.HasPrefix(value, "../") && path.Clean(value) == value
}

func (e *StepExecution) Supported() bool {
	return e != nil && e.Version == "1"
}

// ForExecutor keeps the shared business contract and selects native bindings
// only for the native executor.
func (node CompiledNode) ForExecutor(host string) CompiledNode {
	if host == "external-agent" && node.Execution.Supported() {
		node.LegacyTools, node.TerminalTools = nil, nil
		node.ToolsOnly, node.TerminalToolsOnly = false, false
	}
	return node
}

func (p *RuntimePolicy) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.MappingNode {
		for i := 0; i < len(value.Content); i += 2 {
			if value.Content[i].Value == "external_execution" {
				return fmt.Errorf("runtime.external_execution is unsupported; declare runtime.execution")
			}
		}
	}
	type plain RuntimePolicy
	return value.Decode((*plain)(p))
}

// Reject business overrides instead of silently accepting a second specification.
func (s *StepExecution) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("execution step must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		switch value.Content[i].Value {
		case "skills", "constraints", "tools":
		default:
			return fmt.Errorf("execution step field %q is unsupported; prompts, outputs and acceptance belong in scenario/state.yml", value.Content[i].Value)
		}
	}
	type plain StepExecution
	return value.Decode((*plain)(s))
}

func compileExecution(graph *CompiledStateGraph) []Diagnostic {
	policy := graph.Runtime.Execution
	if policy == nil {
		return nil
	}
	var diagnostics []Diagnostic
	invalid := func(field, message string) {
		diagnostics = append(diagnostics, diag("E_EXECUTION_INVALID", "error", "workflow.yaml.runtime.execution"+field, message))
	}
	if policy.Version != "1" {
		invalid(".version", "execution version must be 1")
	}
	for stepID := range policy.Steps {
		if _, exists := graph.Nodes[stepID]; !exists {
			invalid(".steps."+stepID, "execution references an unknown step")
		}
	}
	for stepID, node := range graph.Nodes {
		step, exists := policy.Steps[stepID]
		field := ".steps." + stepID
		if !exists || strings.TrimSpace(node.Prompt) == "" {
			invalid(field, "every execution step requires a state.yml prompt and an explicit capability declaration")
			continue
		}
		step.Version = policy.Version
		step.Skills = append(append([]string{}, policy.Skills...), step.Skills...)
		step.Constraints = append(append([]string{}, policy.Constraints...), step.Constraints...)
		skills := map[string]bool{}
		uniqueSkills := make([]string, 0, len(step.Skills))
		for i, skill := range step.Skills {
			if !ValidExecutionSkillPath(skill) {
				invalid(fmt.Sprintf("%s.skills[%d]", field, i), "skill must be a safe package-relative file path")
			}
			if !skills[skill] {
				uniqueSkills = append(uniqueSkills, skill)
				skills[skill] = true
			}
		}
		step.Skills = uniqueSkills
		for i, constraint := range step.Constraints {
			if strings.TrimSpace(constraint) == "" {
				invalid(fmt.Sprintf("%s.constraints[%d]", field, i), "constraint must not be empty")
			}
		}
		names := map[string]bool{}
		for i, tool := range step.Tools {
			if !executionToolName.MatchString(tool.Name) || names[tool.Name] || (tool.Source != "package" && tool.Source != "global") {
				invalid(fmt.Sprintf("%s.tools[%d]", field, i), "tools require a unique name and package or global source")
			}
			names[tool.Name] = true
		}
		node.Execution = &step
		graph.Nodes[stepID] = node
	}
	return diagnostics
}
