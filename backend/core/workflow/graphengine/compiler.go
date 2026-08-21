package graphengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type rawWorkflow struct {
	ID      string           `yaml:"id"`
	Runtime RuntimePolicy    `yaml:"runtime"`
	Slots   []map[string]any `yaml:"slots"`
	Steps   []map[string]any `yaml:"steps"`
	UI      map[string]any   `yaml:"ui"`
}

type rawState struct {
	Initial     string                     `yaml:"initial"`
	StartRoute  string                     `yaml:"start_route"`
	Steps       any                        `yaml:"steps"`
	Transitions map[string][]rawTransition `yaml:"transitions"`
}

type rawTransition struct {
	To        string `yaml:"to"`
	When      string `yaml:"when"`
	Condition any    `yaml:"condition"`
}

type uiMaterialSpec struct {
	Type        string
	Cardinality string
	Ordered     bool
}

type rawStep struct {
	ID              string
	Label           string
	Route           string
	Inputs          any
	InputExpression any
	OptionalInputs  any
	Outputs         any
	SkipIf          any
	Prompt          string
	Acceptance      any
	Capabilities    any
	Tools           any
	TerminalTools   any
	ToolsOnly       bool
	StreamHeartbeat bool
	Mode            string
}

func Compile(workflowYAML, stateYAML, scenario string, profile Profile) CompileResult {
	result := CompileResult{Profile: profile, SchemaVersion: SchemaVersion, Diagnostics: []Diagnostic{}}
	var plugin rawWorkflow
	if err := yaml.Unmarshal([]byte(workflowYAML), &plugin); err != nil {
		result.Diagnostics = append(result.Diagnostics, diag("E_WORKFLOW_YAML_INVALID", "error", "workflow.yaml", err.Error()))
		return result
	}
	var state rawState
	if err := yaml.Unmarshal([]byte(stateYAML), &state); err != nil {
		result.Diagnostics = append(result.Diagnostics, diag("E_STATE_YAML_INVALID", "error", "scenario/state.yml", err.Error()))
		return result
	}
	graph := &CompiledStateGraph{
		SchemaVersion: SchemaVersion, StartRoute: state.StartRoute, Nodes: map[string]CompiledNode{}, MaterialProducers: map[string]ProducerRef{},
		InputExpressions: map[string]Expression{}, OptionalInputs: map[string][]MaterialRef{},
		Runtime: plugin.Runtime,
	}
	if graph.StartRoute == "" {
		graph.StartRoute = "all"
	}
	if graph.StartRoute != "all" && graph.StartRoute != "choice" {
		result.Diagnostics = append(result.Diagnostics, diag("E_ROUTE_INVALID", "error", "scenario/state.yml.start_route", "start_route must be all or choice"))
		graph.StartRoute = "all"
	}
	knownMaterials := map[string]bool{}
	materialSpecs := map[string]uiMaterialSpec{}
	external := map[string]bool{}
	optionalExternal := map[string]bool{}
	exposed := map[string]bool{}
	for i, slot := range plugin.Slots {
		id := scalar(slot["id"])
		if id == "" {
			result.Diagnostics = append(result.Diagnostics, diag("E_MATERIAL_ID_REQUIRED", "error", fmt.Sprintf("workflow.yaml.slots[%d].id", i), "material id is required"))
			continue
		}
		if knownMaterials[id] {
			result.Diagnostics = append(result.Diagnostics, materialDiag("E_MATERIAL_DUPLICATE", "error", fmt.Sprintf("workflow.yaml.slots[%d].id", i), id, "material id is duplicated: "+id))
		}
		knownMaterials[id] = true
		materialSpecs[id] = uiMaterialSpec{
			Type:        strings.ToLower(scalar(slot["type"])),
			Cardinality: strings.ToLower(scalar(slot["cardinality"])),
			Ordered:     boolValue(slot["ordered"]),
		}
		external[id] = boolValue(slot["external"]) || scalar(slot["producer"]) == "external"
		if required, declared := slot["required"]; declared {
			optionalExternal[id] = !boolValue(required)
		}
		exposed[id] = boolValue(slot["exposed"])
	}

	workflowSteps := map[string]map[string]any{}
	for i, step := range plugin.Steps {
		id := scalar(step["id"])
		if id == "" {
			result.Diagnostics = append(result.Diagnostics, diag("E_STEP_ID_REQUIRED", "error", fmt.Sprintf("workflow.yaml.steps[%d].id", i), "step id is required"))
			continue
		}
		if _, ok := workflowSteps[id]; ok {
			result.Diagnostics = append(result.Diagnostics, nodeDiag("E_STEP_DUPLICATE", "error", fmt.Sprintf("workflow.yaml.steps[%d].id", i), id, "step id is duplicated: "+id))
		}
		workflowSteps[id] = step
	}
	rawSteps, stepDiags := normalizeSteps(state.Steps)
	result.Diagnostics = append(result.Diagnostics, stepDiags...)
	inputPaths := map[string]string{}
	for id := range workflowSteps {
		if _, ok := rawSteps[id]; !ok {
			result.Diagnostics = append(result.Diagnostics, nodeDiag("E_STATE_STEP_MISSING", "error", "scenario/state.yml.steps."+id, id, "plugin step has no state configuration"))
		}
	}
	for id, step := range rawSteps {
		if id == "__start__" || id == "__end__" {
			result.Diagnostics = append(result.Diagnostics, nodeDiag("E_RESERVED_STEP_ID", "error", "scenario/state.yml.steps."+id, id, "reserved node id cannot be declared as a step"))
			continue
		}
		if _, ok := workflowSteps[id]; !ok {
			result.Diagnostics = append(result.Diagnostics, nodeDiag("E_WORKFLOW_STEP_MISSING", "error", "workflow.yaml.steps", id, "state step is not declared in workflow.yaml"))
		}
		node := CompiledNode{ID: id, Label: step.Label, Route: step.Route, Prompt: step.Prompt,
			Acceptance: stringList(step.Acceptance), Capabilities: stringList(step.Capabilities),
			LegacyTools: stringList(step.Tools), TerminalTools: stringList(step.TerminalTools),
			ToolsOnly: step.ToolsOnly, StreamHeartbeat: step.StreamHeartbeat, Mode: step.Mode}
		if definition := workflowSteps[id]; definition != nil {
			if len(node.Acceptance) == 0 {
				node.Acceptance = stringList(definition["acceptance_criteria"])
			}
			if len(node.Capabilities) == 0 {
				node.Capabilities = stringList(definition["capabilities"])
			}
		}
		if node.Route == "" {
			node.Route = "all"
		}
		if node.Route != "all" && node.Route != "choice" {
			result.Diagnostics = append(result.Diagnostics, nodeDiag("E_ROUTE_INVALID", "error", "scenario/state.yml.steps."+id+".route", id, "route must be all or choice"))
			node.Route = "all"
		}
		node.Outputs, node.RequiredOutputs = parseOutputs(step.Outputs)
		if step.Inputs != nil {
			inputPaths[id] = "scenario/state.yml.steps." + id + ".inputs"
			node.Input, node.OptionalInputs, stepDiags = parseUnifiedInputs(step.Inputs, id)
			result.Diagnostics = append(result.Diagnostics, stepDiags...)
			if step.InputExpression != nil || step.OptionalInputs != nil {
				result.Diagnostics = append(result.Diagnostics, nodeDiag("E_INPUT_FORMAT_CONFLICT", "error", inputPaths[id], id, "use inputs only; do not combine it with input_expression or optional_inputs"))
			}
		} else if step.InputExpression != nil {
			inputPaths[id] = "scenario/state.yml.steps." + id + ".input_expression"
			node.OptionalInputs = parseMaterialRefs(step.OptionalInputs)
			expr, err := parseExpression(step.InputExpression)
			if err != nil {
				result.Diagnostics = append(result.Diagnostics, nodeDiag("E_EXPRESSION_INVALID", "error", "scenario/state.yml.steps."+id+".input_expression", id, err.Error()))
			} else {
				node.Input = expr
			}
		} else {
			inputPaths[id] = "scenario/state.yml.steps." + id + ".optional_inputs"
			node.OptionalInputs = parseMaterialRefs(step.OptionalInputs)
		}
		if step.SkipIf != nil {
			expr, err := parseExpression(step.SkipIf)
			if err != nil {
				result.Diagnostics = append(result.Diagnostics, nodeDiag("E_SKIP_EXPRESSION_INVALID", "error", "scenario/state.yml.steps."+id+".skip_if", id, err.Error()))
			} else {
				node.SkipIf = expr
			}
		}
		graph.Nodes[id] = node
		if node.Input != nil {
			graph.InputExpressions[id] = *node.Input
		}
		graph.OptionalInputs[id] = node.OptionalInputs
	}
	for i, slotID := range graph.Runtime.PublisherOwnedSlots {
		if !knownMaterials[slotID] {
			result.Diagnostics = append(result.Diagnostics, materialDiag(
				"E_RUNTIME_PUBLISHER_SLOT_UNKNOWN", "error",
				fmt.Sprintf("workflow.yaml.runtime.publisher_owned_slots[%d]", i), slotID,
				"runtime publisher-owned slot is not declared: "+slotID,
			))
		}
	}
	if stepID := strings.TrimSpace(graph.Runtime.CompletedEditStep); stepID != "" {
		graph.Runtime.CompletedEditStep = stepID
		if _, ok := workflowSteps[stepID]; !ok {
			result.Diagnostics = append(result.Diagnostics, nodeDiag(
				"E_RUNTIME_EDIT_STEP_UNKNOWN", "error", "workflow.yaml.runtime.completed_edit_step",
				stepID, "runtime completed edit step is not declared: "+stepID,
			))
		}
	}
	for i, stepID := range graph.Runtime.CompletedContinueSteps {
		stepID = strings.TrimSpace(stepID)
		graph.Runtime.CompletedContinueSteps[i] = stepID
		if _, ok := workflowSteps[stepID]; !ok {
			result.Diagnostics = append(result.Diagnostics, nodeDiag(
				"E_RUNTIME_CONTINUE_STEP_UNKNOWN", "error", fmt.Sprintf("workflow.yaml.runtime.completed_continue_steps[%d]", i),
				stepID, "runtime completed continue step is not declared: "+stepID,
			))
		}
	}
	clarificationIDs := map[string]bool{}
	for i := range graph.Runtime.ClarificationFields {
		field := &graph.Runtime.ClarificationFields[i]
		path := fmt.Sprintf("workflow.yaml.runtime.clarification_fields[%d]", i)
		field.ID = strings.TrimSpace(field.ID)
		field.Label = strings.TrimSpace(field.Label)
		field.Question = strings.TrimSpace(field.Question)
		field.Type = strings.ToLower(strings.TrimSpace(field.Type))
		if field.Type == "" {
			field.Type = "text"
		}
		if field.ID == "" {
			result.Diagnostics = append(result.Diagnostics, diag(
				"E_RUNTIME_CLARIFICATION_ID_REQUIRED", "error", path+".id",
				"runtime clarification field id is required",
			))
		} else if clarificationIDs[field.ID] {
			result.Diagnostics = append(result.Diagnostics, diag(
				"E_RUNTIME_CLARIFICATION_ID_DUPLICATE", "error", path+".id",
				"runtime clarification field id is duplicated: "+field.ID,
			))
		} else {
			clarificationIDs[field.ID] = true
		}
		if field.Label == "" {
			field.Label = field.ID
		}
		if field.Question == "" {
			result.Diagnostics = append(result.Diagnostics, diag(
				"E_RUNTIME_CLARIFICATION_QUESTION_REQUIRED", "error", path+".question",
				"runtime clarification field question is required",
			))
		}
		switch field.Type {
		case "text", "boolean", "single", "multiple":
		default:
			result.Diagnostics = append(result.Diagnostics, diag(
				"E_RUNTIME_CLARIFICATION_TYPE_INVALID", "error", path+".type",
				"runtime clarification field type must be text, boolean, single, or multiple",
			))
		}
		choices := make([]string, 0, len(field.Choices))
		seenChoices := map[string]bool{}
		for _, rawChoice := range field.Choices {
			choice := strings.TrimSpace(rawChoice)
			if choice != "" && !seenChoices[choice] {
				seenChoices[choice] = true
				choices = append(choices, choice)
			}
		}
		field.Choices = choices
		if (field.Type == "single" || field.Type == "multiple") && len(field.Choices) == 0 {
			result.Diagnostics = append(result.Diagnostics, diag(
				"E_RUNTIME_CLARIFICATION_CHOICES_REQUIRED", "error", path+".choices",
				"single and multiple runtime clarification fields require choices",
			))
		}
	}

	// Material producer table. External is explicit; implicit unproduced slots are
	// diagnosed instead of silently treated as user input.
	for id := range external {
		if external[id] {
			graph.MaterialProducers[id] = ProducerRef{
				Kind: "external", Optional: optionalExternal[id],
			}
		}
	}
	for id, node := range graph.Nodes {
		inputs := map[string]bool{}
		for _, m := range expressionMaterials(node.Input) {
			inputs[m] = true
		}
		for _, m := range node.OptionalInputs {
			inputs[m.Material] = true
		}
		for j, material := range node.Outputs {
			path := fmt.Sprintf("scenario/state.yml.steps.%s.outputs[%d]", id, j)
			if !knownMaterials[material] {
				result.Diagnostics = append(result.Diagnostics, materialDiag("E_MATERIAL_UNKNOWN", "error", path, material, "step outputs an undefined material: "+material))
			}
			if inputs[material] {
				result.Diagnostics = append(result.Diagnostics, materialNodeDiag("E_MATERIAL_SELF_OVERWRITE", "error", path, id, material, "a step cannot consume and produce the same material"))
			}
			if previous, ok := graph.MaterialProducers[material]; ok {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "E_MATERIAL_MULTIPLE_PRODUCERS", Severity: "error", Path: path, NodeID: id, MaterialID: material, Message: "material has multiple producers: " + material, Details: map[string]any{"previous_producer": previous}, Fixable: true})
			} else {
				graph.MaterialProducers[material] = ProducerRef{Kind: "step", StepID: id}
			}
		}
	}
	for material := range knownMaterials {
		if _, ok := graph.MaterialProducers[material]; !ok {
			result.Diagnostics = append(result.Diagnostics, materialDiag("E_MATERIAL_PRODUCER_MISSING", "error", "workflow.yaml.slots", material, "material must declare external producer or be produced by exactly one step"))
		}
	}

	// Edges and graph structure.
	allNodes := map[string]bool{"__start__": true, "__end__": true}
	for id := range graph.Nodes {
		allNodes[id] = true
	}
	seenEdges := map[string]bool{}
	for from, edges := range state.Transitions {
		if !allNodes[from] || from == "__end__" {
			result.Diagnostics = append(result.Diagnostics, nodeDiag("E_EDGE_SOURCE_UNKNOWN", "error", "scenario/state.yml.transitions."+from, from, "transition source is not a declared step"))
		}
		for i, edge := range edges {
			path := fmt.Sprintf("scenario/state.yml.transitions.%s[%d]", from, i)
			id := from + "->" + edge.To
			if edge.To == "" || !allNodes[edge.To] {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "E_EDGE_TARGET_UNKNOWN", Severity: "error", Path: path + ".to", EdgeID: id, Message: "transition target is not a declared step: " + edge.To, Fixable: true})
			}
			if from == edge.To {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "E_EDGE_SELF_LOOP", Severity: "error", Path: path, EdgeID: id, Message: "self-loop is not allowed", Fixable: true})
			}
			if seenEdges[id] {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "E_EDGE_DUPLICATE", Severity: "error", Path: path, EdgeID: id, Message: "duplicate control edge: " + id, Fixable: true})
			}
			seenEdges[id] = true
			compiled := CompiledEdge{ID: id, From: from, To: edge.To, When: strings.TrimSpace(edge.When)}
			switch value := edge.Condition.(type) {
			case nil:
			case string:
				if strings.TrimSpace(value) != "" {
					if compiled.When == "" {
						compiled.When = strings.TrimSpace(value)
					}
					result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "W_ROUTE_CONDITION_MIGRATED", Severity: "warning", Path: path + ".condition", EdgeID: id, Message: "natural-language route condition was accepted; save it as `when`", Fixable: true})
				}
			default:
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "E_ROUTE_MATERIAL_CONDITION_UNSUPPORTED", Severity: "error", Path: path + ".condition", EdgeID: id, Message: "route conditions must be natural-language `when` hints; material expressions are only supported by skip_if and inputs", Fixable: true})
			}
			graph.ControlEdges = append(graph.ControlEdges, compiled)
		}
	}
	if len(state.Transitions["__start__"]) == 0 {
		// Legacy initial is a deterministic format migration.
		if state.Initial != "" && state.Initial != "__start__" && allNodes[state.Initial] {
			graph.ControlEdges = append(graph.ControlEdges, CompiledEdge{ID: "__start__->" + state.Initial, From: "__start__", To: state.Initial})
		} else {
			result.Diagnostics = append(result.Diagnostics, diag("E_START_MISSING", "error", "scenario/state.yml.transitions.__start__", "state graph has no entry transition"))
		}
	}

	adj, reverse := adjacency(graph.ControlEdges, allNodes)
	dominators := computeDominators("__start__", allNodes, reverse)
	guaranteedMaterials := map[string]bool{}
	for _, node := range graph.Nodes {
		for _, material := range node.RequiredOutputs {
			guaranteedMaterials[material] = true
		}
	}
	cycle := cycleNodes(adj)
	for _, id := range cycle {
		result.Diagnostics = append(result.Diagnostics, nodeDiag("E_GRAPH_CYCLE", "error", "scenario/state.yml.transitions", id, "node participates in a directed cycle"))
	}
	reachable := traverse("__start__", adj)
	canEnd := traverse("__end__", reverse)
	for id := range graph.Nodes {
		if !reachable[id] {
			result.Diagnostics = append(result.Diagnostics, nodeDiag("E_NODE_UNREACHABLE", "error", "scenario/state.yml.steps."+id, id, "node is not reachable from __start__"))
		}
		if !canEnd[id] {
			result.Diagnostics = append(result.Diagnostics, nodeDiag("E_NODE_CANNOT_REACH_END", "error", "scenario/state.yml.steps."+id, id, "node cannot reach __end__"))
		}
	}
	graph.StaticOrder = topoOrder(allNodes, adj)
	// Expressions and producer ancestry.
	for id, node := range graph.Nodes {
		inputPath := inputPaths[id]
		result.Diagnostics = append(result.Diagnostics, validateExpression(node.Input, inputPath, id, knownMaterials)...)
		result.Diagnostics = append(result.Diagnostics, validateInputExpressionShape(node.Input, inputPath, id)...)
		result.Diagnostics = append(result.Diagnostics, validateExpression(node.SkipIf, "scenario/state.yml.steps."+id+".skip_if", id, knownMaterials)...)
		result.Diagnostics = append(result.Diagnostics, validateSkipExpressionShape(node.SkipIf, "scenario/state.yml.steps."+id+".skip_if", id)...)
		for _, ref := range node.OptionalInputs {
			if ref.BindAs != "" {
				result.Diagnostics = append(result.Diagnostics, nodeDiag("E_BIND_ALIAS_UNSUPPORTED", "error", inputPath, id, "bind_as is no longer supported; reference materials by their unique ids"))
			}
			if !knownMaterials[ref.Material] {
				result.Diagnostics = append(result.Diagnostics, materialNodeDiag("E_MATERIAL_UNKNOWN", "error", inputPath, id, ref.Material, "optional input references an unknown material"))
			}
		}
		materials := expressionMaterials(node.Input)
		for _, ref := range node.OptionalInputs {
			materials = append(materials, ref.Material)
		}
		for _, material := range materials {
			producer, ok := graph.MaterialProducers[material]
			if ok && producer.Kind == "step" && !traverse(producer.StepID, adj)[id] {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "E_MATERIAL_PRODUCER_NOT_UPSTREAM", Severity: "error", Path: inputPath, NodeID: id, MaterialID: material, Message: "material producer must be a control ancestor of the consumer", Details: map[string]any{"producer_step_id": producer.StepID}, Fixable: true})
			}
		}
		if node.SkipIf != nil {
			if expressionGuaranteed(node.SkipIf, id, graph.MaterialProducers, guaranteedMaterials, dominators, false) {
				result.Diagnostics = append(result.Diagnostics, nodeDiag("E_SKIP_ALWAYS_TRUE", "error", "scenario/state.yml.steps."+id+".skip_if", id, "skip_if is always true because its materials are guaranteed by control-dominating producers"))
			}
			for _, material := range expressionMaterials(node.SkipIf) {
				producer := graph.MaterialProducers[material]
				if producer.Kind == "step" && (producer.StepID == id || !dominators[id][producer.StepID]) {
					result.Diagnostics = append(result.Diagnostics, materialNodeDiag("E_SKIP_RACY_MATERIAL", "error", "scenario/state.yml.steps."+id+".skip_if", id, material, "skip_if depends on a material not guaranteed to be upstream"))
				}
			}
			graph.SkipExpansions = append(graph.SkipExpansions, CompiledBypass{NodeID: id, From: reverse[id], To: adj[id]})
		}
	}
	result.Diagnostics = append(result.Diagnostics, validateUI(plugin.UI, knownMaterials, exposed, materialSpecs, profile)...)
	if scenario != "" {
		for id := range graph.Nodes {
			if !strings.Contains(scenario, id) {
				result.Diagnostics = append(result.Diagnostics, nodeDiag("W_SCENARIO_STEP_MISSING", "warning", "scenario/scenario.md", id, "scenario does not mention step "+id))
			}
		}
	}

	canonical, _ := canonicalGraphJSON(graph)
	sum := sha256.Sum256(canonical)
	graph.GraphHash = hex.EncodeToString(sum[:])
	result.GraphHash, result.Graph = graph.GraphHash, graph
	result.Valid = !hasErrors(result.Diagnostics)
	return result
}

// canonicalGraphJSON produces stable hash input without changing runtime edge
// ordering. YAML mappings are decoded into Go maps, so iterating transition
// sources and skip nodes can otherwise reorder slices between compilations.
func canonicalGraphJSON(graph *CompiledStateGraph) ([]byte, error) {
	canonical := *graph
	canonical.GraphHash = ""
	edgesBySource := make(map[string][]CompiledEdge)
	for _, edge := range graph.ControlEdges {
		edgesBySource[edge.From] = append(edgesBySource[edge.From], edge)
	}
	sources := make([]string, 0, len(edgesBySource))
	for source := range edgesBySource {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	canonical.ControlEdges = nil
	for _, source := range sources {
		// Preserve declared order among exits from the same source because it is
		// semantically significant for machine-decided choice routes.
		canonical.ControlEdges = append(canonical.ControlEdges, edgesBySource[source]...)
	}
	canonical.SkipExpansions = append([]CompiledBypass(nil), graph.SkipExpansions...)
	for i := range canonical.SkipExpansions {
		canonical.SkipExpansions[i].From = append([]string(nil), canonical.SkipExpansions[i].From...)
		canonical.SkipExpansions[i].To = append([]string(nil), canonical.SkipExpansions[i].To...)
		sort.Strings(canonical.SkipExpansions[i].From)
		sort.Strings(canonical.SkipExpansions[i].To)
	}
	sort.Slice(canonical.SkipExpansions, func(i, j int) bool {
		return canonical.SkipExpansions[i].NodeID < canonical.SkipExpansions[j].NodeID
	})
	return json.Marshal(&canonical)
}

func expressionGuaranteed(expr *Expression, decisionNode string, producers map[string]ProducerRef, guaranteedMaterials map[string]bool, dominators map[string]map[string]bool, allowSelf bool) bool {
	if expr == nil {
		return true
	}
	if expr.Material != "" {
		producer, ok := producers[expr.Material]
		return ok && guaranteedMaterials[expr.Material] && producer.Kind == "step" && (allowSelf || producer.StepID != decisionNode) && dominators[decisionNode][producer.StepID]
	}
	if len(expr.All) > 0 {
		for i := range expr.All {
			if !expressionGuaranteed(&expr.All[i], decisionNode, producers, guaranteedMaterials, dominators, allowSelf) {
				return false
			}
		}
		return true
	}
	if len(expr.Any) > 0 {
		for i := range expr.Any {
			if expressionGuaranteed(&expr.Any[i], decisionNode, producers, guaranteedMaterials, dominators, allowSelf) {
				return true
			}
		}
	}
	return false
}

func normalizeSteps(value any) (map[string]rawStep, []Diagnostic) {
	out := map[string]rawStep{}
	var diags []Diagnostic
	if value == nil {
		return out, []Diagnostic{diag("E_STEPS_MISSING", "error", "scenario/state.yml.steps", "state graph has no steps")}
	}
	data, _ := yaml.Marshal(value)
	var mapping map[string]map[string]any
	if yaml.Unmarshal(data, &mapping) == nil && mapping != nil {
		for id, raw := range mapping {
			out[id] = decodeRawStep(id, raw)
		}
		return out, diags
	}
	var list []map[string]any
	if yaml.Unmarshal(data, &list) == nil {
		for i, raw := range list {
			id := scalar(raw["id"])
			if id == "" {
				diags = append(diags, diag("E_STEP_ID_REQUIRED", "error", fmt.Sprintf("scenario/state.yml.steps[%d].id", i), "step id is required"))
				continue
			}
			if _, exists := out[id]; exists {
				diags = append(diags, nodeDiag("E_STEP_DUPLICATE", "error", fmt.Sprintf("scenario/state.yml.steps[%d].id", i), id, "step id is duplicated"))
			}
			out[id] = decodeRawStep(id, raw)
		}
		return out, diags
	}
	return out, []Diagnostic{diag("E_STEPS_INVALID", "error", "scenario/state.yml.steps", "steps must be a mapping or list")}
}

func decodeRawStep(id string, raw map[string]any) rawStep {
	return rawStep{ID: id, Label: scalar(raw["label"]), Route: scalar(raw["route"]),
		Inputs: raw["inputs"], InputExpression: raw["input_expression"], OptionalInputs: raw["optional_inputs"],
		Outputs: raw["outputs"], SkipIf: firstNonNil(raw["skip_if"], raw["skipif"]),
		Prompt: scalar(raw["prompt"]), Acceptance: raw["acceptance_criteria"],
		Capabilities: raw["capabilities"], Tools: raw["tools"], TerminalTools: raw["terminal_tools"],
		ToolsOnly: boolValue(raw["tools_only"]), StreamHeartbeat: boolValue(raw["stream_heartbeat"]), Mode: scalar(raw["mode"])}
}

func stringList(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if strings.TrimSpace(scalar(value)) == "" {
			return nil
		}
		return []string{strings.TrimSpace(scalar(value))}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(scalar(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func parseExpression(value any) (*Expression, error) {
	if _, ok := value.(string); ok {
		return nil, fmt.Errorf("expression must be an object, not natural language")
	}
	b, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var expr Expression
	if err := yaml.Unmarshal(b, &expr); err != nil {
		return nil, err
	}
	if expr.Material == "" && len(expr.All) == 0 && len(expr.Any) == 0 {
		return nil, fmt.Errorf("expression must contain material, all, or any")
	}
	return &expr, nil
}

func parseUnifiedInputs(value any, nodeID string) (*Expression, []MaterialRef, []Diagnostic) {
	if value == nil {
		return nil, nil, nil
	}
	b, _ := yaml.Marshal(value)
	var items []any
	if yaml.Unmarshal(b, &items) != nil {
		return nil, nil, []Diagnostic{nodeDiag("E_INPUTS_INVALID", "error", "scenario/state.yml.steps."+nodeID+".inputs", nodeID, "inputs must be a list")}
	}
	var required []Expression
	var optional []MaterialRef
	var diags []Diagnostic
	for index, item := range items {
		path := fmt.Sprintf("scenario/state.yml.steps.%s.inputs[%d]", nodeID, index)
		switch v := item.(type) {
		case string:
			if v != "" {
				optional = append(optional, MaterialRef{Material: v})
			}
		case map[string]any:
			id := scalar(firstNonNil(v["slot"], v["material"], v["id"]))
			if id == "" {
				diags = append(diags, nodeDiag("E_INPUT_MATERIAL_REQUIRED", "error", path+".material", nodeID, "input material is required"))
				continue
			}
			requiredValue, hasRequired := v["required"]
			if !hasRequired {
				diags = append(diags, nodeDiag("E_INPUT_REQUIRED_FLAG_MISSING", "error", path+".required", nodeID, "input must explicitly declare required: true or false"))
			}
			if boolValue(requiredValue) {
				alternatives := parseMaterialList(v["alternatives"])
				if len(alternatives) == 0 {
					required = append(required, Expression{Material: id})
				} else {
					choices := []Expression{{Material: id}}
					for _, alternative := range alternatives {
						choices = append(choices, Expression{Material: alternative})
					}
					required = append(required, Expression{Any: choices})
				}
			} else {
				if len(parseMaterialList(v["alternatives"])) > 0 {
					diags = append(diags, nodeDiag("E_OPTIONAL_ALTERNATIVES_UNSUPPORTED", "error", path+".alternatives", nodeID, "optional inputs cannot declare alternatives"))
				}
				optional = append(optional, MaterialRef{Material: id})
			}
		}
	}
	if len(required) == 0 {
		return nil, optional, diags
	}
	if len(required) == 1 {
		return &required[0], optional, diags
	}
	return &Expression{All: required}, optional, diags
}

func parseMaterialList(value any) []string {
	refs := parseMaterialRefs(value)
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Material != "" {
			out = append(out, ref.Material)
		}
	}
	return out
}

func parseOutputs(value any) (all []string, required []string) {
	if value == nil {
		return nil, nil
	}
	b, _ := yaml.Marshal(value)
	var items []any
	if yaml.Unmarshal(b, &items) != nil {
		return nil, nil
	}
	for _, item := range items {
		isRequired := true
		var id string
		switch output := item.(type) {
		case string:
			id = strings.TrimSpace(output)
		case map[string]any:
			id = scalar(firstNonNil(output["material"], output["slot"], output["id"]))
			if raw, exists := output["required"]; exists {
				isRequired = boolValue(raw)
			}
		}
		if id == "" {
			continue
		}
		all = append(all, id)
		if isRequired {
			required = append(required, id)
		}
	}
	return all, required
}
func parseMaterialRefs(value any) []MaterialRef {
	if value == nil {
		return nil
	}
	b, _ := yaml.Marshal(value)
	var items []any
	if yaml.Unmarshal(b, &items) != nil {
		return nil
	}
	var out []MaterialRef
	for _, item := range items {
		switch v := item.(type) {
		case string:
			if v != "" {
				out = append(out, MaterialRef{Material: v})
			}
		case map[string]any:
			id := scalar(firstNonNil(v["material"], v["slot"], v["id"]))
			if id != "" {
				out = append(out, MaterialRef{Material: id, BindAs: scalar(v["bind_as"])})
			}
		}
	}
	return out
}

func adjacency(edges []CompiledEdge, nodes map[string]bool) (map[string][]string, map[string][]string) {
	a, r := map[string][]string{}, map[string][]string{}
	for id := range nodes {
		a[id] = nil
		r[id] = nil
	}
	for _, e := range edges {
		if nodes[e.From] && nodes[e.To] {
			a[e.From] = append(a[e.From], e.To)
			r[e.To] = append(r[e.To], e.From)
		}
	}
	return a, r
}
func traverse(start string, adj map[string][]string) map[string]bool {
	seen := map[string]bool{}
	q := []string{start}
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		q = append(q, adj[id]...)
	}
	return seen
}

func computeDominators(start string, nodes map[string]bool, reverse map[string][]string) map[string]map[string]bool {
	dom := map[string]map[string]bool{}
	for node := range nodes {
		dom[node] = map[string]bool{}
		if node == start {
			dom[node][start] = true
		} else {
			for candidate := range nodes {
				dom[node][candidate] = true
			}
		}
	}
	changed := true
	for changed {
		changed = false
		for node := range nodes {
			if node == start || len(reverse[node]) == 0 {
				continue
			}
			next := map[string]bool{node: true}
			for candidate := range nodes {
				present := true
				for _, predecessor := range reverse[node] {
					if !dom[predecessor][candidate] {
						present = false
						break
					}
				}
				if present {
					next[candidate] = true
				}
			}
			if len(next) != len(dom[node]) {
				dom[node] = next
				changed = true
				continue
			}
			for candidate := range next {
				if !dom[node][candidate] {
					dom[node] = next
					changed = true
					break
				}
			}
		}
	}
	return dom
}

func cycleNodes(adj map[string][]string) []string {
	state := map[string]int{}
	found := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if state[id] == 1 {
			found[id] = true
			return
		}
		if state[id] == 2 {
			return
		}
		state[id] = 1
		for _, n := range adj[id] {
			if state[n] == 1 {
				found[id] = true
				found[n] = true
			}
			visit(n)
		}
		state[id] = 2
	}
	for id := range adj {
		visit(id)
	}
	out := make([]string, 0, len(found))
	for id := range found {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func topoOrder(nodes map[string]bool, adj map[string][]string) []string {
	in := map[string]int{}
	for id := range nodes {
		in[id] = 0
	}
	for _, ns := range adj {
		for _, n := range ns {
			in[n]++
		}
	}
	var q []string
	for id, d := range in {
		if d == 0 {
			q = append(q, id)
		}
	}
	sort.Strings(q)
	var out []string
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		out = append(out, id)
		for _, n := range adj[id] {
			in[n]--
			if in[n] == 0 {
				q = append(q, n)
				sort.Strings(q)
			}
		}
	}
	return out
}
func validateUI(
	ui map[string]any,
	known, exposed map[string]bool,
	specs map[string]uiMaterialSpec,
	profile Profile,
) []Diagnostic {
	if ui == nil {
		return nil
	}
	b, _ := yaml.Marshal(ui["tabs"])
	var tabs []map[string]any
	if yaml.Unmarshal(b, &tabs) != nil {
		return []Diagnostic{diag("E_UI_TABS_INVALID", "error", "workflow.yaml.ui.tabs", "ui tabs must be a list")}
	}
	placed := map[string]int{}
	var out []Diagnostic
	widgets := map[string]map[string]any{}
	if rawWidgets, exists := ui["slots"]; exists {
		widgetBytes, _ := yaml.Marshal(rawWidgets)
		if yaml.Unmarshal(widgetBytes, &widgets) != nil {
			out = append(out, diag("E_UI_WIDGETS_INVALID", "error", "workflow.yaml.ui.slots", "ui slots must be a mapping"))
		}
	}
	for materialID, widget := range widgets {
		path := "workflow.yaml.ui.slots." + materialID
		if !known[materialID] {
			out = append(out, materialDiag("E_UI_WIDGET_MATERIAL_UNKNOWN", "error", path, materialID, "UI widget references an unknown material"))
			continue
		}
		if scalar(widget["widgetType"]) == "html-slide" && specs[materialID].Type != "text" {
			out = append(out, materialDiag("E_UI_WIDGET_INCOMPATIBLE", "error", path+".widgetType", materialID, "html-slide requires a text material"))
		}
	}
	for i, tab := range tabs {
		refs := parseMaterialRefs(tab["slots"])
		tabMaterials := map[string]bool{}
		if len(refs) == 0 {
			out = append(out, diag("E_UI_TAB_EMPTY", "error", fmt.Sprintf("workflow.yaml.ui.tabs[%d].slots", i), "UI tab has no materials"))
		}
		for _, ref := range refs {
			tabMaterials[ref.Material] = true
			if !known[ref.Material] {
				out = append(out, materialDiag("E_UI_MATERIAL_UNKNOWN", "error", fmt.Sprintf("workflow.yaml.ui.tabs[%d].slots", i), ref.Material, "UI references an unknown material"))
			}
			placed[ref.Material]++
		}

		actionsBytes, _ := yaml.Marshal(tab["actions"])
		var actions []map[string]any
		if tab["actions"] != nil && yaml.Unmarshal(actionsBytes, &actions) != nil {
			out = append(out, diag("E_UI_ACTIONS_INVALID", "error", fmt.Sprintf("workflow.yaml.ui.tabs[%d].actions", i), "tab actions must be a list"))
			continue
		}
		actionIDs := map[string]bool{}
		for j, action := range actions {
			path := fmt.Sprintf("workflow.yaml.ui.tabs[%d].actions[%d]", i, j)
			actionID := scalar(action["id"])
			if actionID == "" {
				out = append(out, diag("E_UI_ACTION_ID_REQUIRED", "error", path+".id", "tab action id is required"))
			} else if actionIDs[actionID] {
				out = append(out, diag("E_UI_ACTION_ID_DUPLICATE", "error", path+".id", "tab action id is duplicated: "+actionID))
			}
			actionIDs[actionID] = true
			if scalar(action["type"]) != "export" {
				out = append(out, diag("E_UI_ACTION_TYPE_INVALID", "error", path+".type", "tab action type must be export"))
			}
			if scalar(action["provider"]) == "" {
				out = append(out, diag("E_UI_ACTION_PROVIDER_REQUIRED", "error", path+".provider", "export action provider is required"))
			}

			inputs := map[string]string{}
			inputBytes, _ := yaml.Marshal(action["inputs"])
			if action["inputs"] == nil || yaml.Unmarshal(inputBytes, &inputs) != nil || len(inputs) == 0 {
				out = append(out, diag("E_UI_ACTION_INPUTS_REQUIRED", "error", path+".inputs", "export action inputs must map provider input names to material ids"))
				continue
			}
			for inputName, materialID := range inputs {
				inputPath := path + ".inputs." + inputName
				materialID = strings.TrimSpace(materialID)
				if !known[materialID] {
					out = append(out, materialDiag("E_UI_ACTION_INPUT_UNKNOWN", "error", inputPath, materialID, "export action input references an unknown material"))
					continue
				}
				if !tabMaterials[materialID] {
					out = append(out, materialDiag("E_UI_ACTION_INPUT_OUTSIDE_TAB", "error", inputPath, materialID, "export action input must be placed in the same tab"))
				}
				if scalar(action["alignment"]) == "sort_order" {
					spec := specs[materialID]
					if spec.Cardinality != "list" || !spec.Ordered {
						out = append(out, materialDiag("E_UI_ACTION_ALIGNMENT_INCOMPATIBLE", "error", inputPath, materialID, "sort_order aligned export inputs must be ordered list materials"))
					}
				}
			}
			if alignment := scalar(action["alignment"]); alignment != "" && alignment != "sort_order" {
				out = append(out, diag("E_UI_ACTION_ALIGNMENT_INVALID", "error", path+".alignment", "export action alignment must be sort_order"))
			}
			if len(stringList(action["formats"])) == 0 {
				out = append(out, diag("E_UI_ACTION_FORMATS_REQUIRED", "error", path+".formats", "export action formats are required"))
			}
		}
	}
	for id := range exposed {
		if exposed[id] && placed[id] == 0 {
			out = append(out, materialDiag("E_UI_EXPOSED_MATERIAL_UNPLACED", "error", "workflow.yaml.ui.tabs", id, "exposed material is not placed in the UI"))
		}
		if exposed[id] && placed[id] > 1 {
			out = append(out, materialDiag("E_UI_MATERIAL_DUPLICATE", "error", "workflow.yaml.ui.tabs", id, "material is placed more than once in the UI"))
		}
	}
	return out
}
func scalar(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
func boolValue(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true")
	default:
		return false
	}
}
func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}
func diag(code, severity, path, message string) Diagnostic {
	return Diagnostic{Code: code, Severity: severity, Path: path, Message: message, Fixable: true}
}
func nodeDiag(code, severity, path, node, message string) Diagnostic {
	d := diag(code, severity, path, message)
	d.NodeID = node
	return d
}
func materialDiag(code, severity, path, material, message string) Diagnostic {
	d := diag(code, severity, path, message)
	d.MaterialID = material
	return d
}
func materialNodeDiag(code, severity, path, node, material, message string) Diagnostic {
	d := materialDiag(code, severity, path, material, message)
	d.NodeID = node
	return d
}
func hasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
