package graphengine

import (
	"fmt"
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
	Version     string         `json:"version,omitempty" yaml:"-"`
	Skills      []string       `json:"skills,omitempty" yaml:"skills,omitempty"`
	Constraints []string       `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Tools       []ExternalTool `json:"tools,omitempty" yaml:"tools,omitempty"`
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

const externalAdapterInstructions = `Execute the shared step prompt, Skills, business constraints, output schemas and acceptance criteria with your host's capabilities. Tool names, wrapper calls, local paths, tools-only/terminal rules and automatic publication described in the shared task are LazyMind implementation bindings, not required operations on an external host. Preserve their business intent using your own capabilities or explicitly advertised LazyMind tools; do not invoke unavailable native wrappers or a hidden LazyMind text/vision model. A restriction on a native wrapper, such as UI-only export or automatic saving, does not prohibit the host from delivering the user's requested file through the declared artifact slots. Do not change page count, requested format, evidence rules, review boundaries or output meanings. The host owns generation and semantic/visual validation; LazyMind owns persistence and workflow transitions. If a required capability is unavailable on both sides, report it and ask the user to configure that capability in LazyMind before retrying.`

func compileExecution(graph *CompiledStateGraph) []Diagnostic {
	policy := graph.Runtime.Execution
	var diagnostics []Diagnostic
	invalid := func(field, message string) {
		diagnostics = append(diagnostics, diag("E_EXECUTION_INVALID", "error", "workflow.yaml.runtime.execution"+field, message))
	}
	if graph.Runtime.ExternalExecution != nil {
		invalid("", "execution and legacy external_execution cannot be combined")
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
			if !ValidExternalSkillPath(skill) {
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
			if !externalToolName.MatchString(tool.Name) || names[tool.Name] || (tool.Source != "package" && tool.Source != "global") {
				invalid(fmt.Sprintf("%s.tools[%d]", field, i), "tools require a unique name and package or global source")
			}
			names[tool.Name] = true
		}
		node.Execution = &step
		// Compatibility projection for existing MCP clients. This is derived from
		// the shared node, never independently authored business instructions.
		node.ExternalExecution = &ExternalStepExecution{
			Version: ExternalExecutionVersion, ValidationOwner: "external-agent", Prompt: node.Prompt,
			AdapterInstructions: externalAdapterInstructions,
			Skills:              step.Skills, Constraints: step.Constraints, Tools: step.Tools,
		}
		graph.Nodes[stepID] = node
	}
	return diagnostics
}
