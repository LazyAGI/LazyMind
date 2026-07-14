package graphengine

import "sort"

func DecideRoute(graph *CompiledStateGraph, from string, materials []MaterialValue) RouteDecision {
	decision := RouteDecision{}
	var decideFrom func(string)
	decideFrom = func(source string) {
		route := graph.StartRoute
		if node, ok := graph.Nodes[source]; ok {
			route = node.Route
		}
		matched := false
		for _, edge := range graph.ControlEdges {
			if edge.From != source {
				continue
			}
			llmDecided := edge.When != "" || edge.Legacy != ""
			evaluation := Evaluation{Satisfied: true}
			if !llmDecided {
				evaluation = Evaluate(edge.Condition, materials)
			}
			if !evaluation.Satisfied || (route == "choice" && matched && !llmDecided) {
				decision.Pruned = append(decision.Pruned, edge.To)
				continue
			}
			if !llmDecided {
				matched = true
			}
			decision.Witnesses = append(decision.Witnesses, evaluation.Witnesses...)
			node, isNode := graph.Nodes[edge.To]
			if isNode && node.SkipIf != nil {
				skip := Evaluate(node.SkipIf, materials)
				if skip.Satisfied {
					decision.Bypassed = append(decision.Bypassed, edge.To)
					decision.Witnesses = append(decision.Witnesses, skip.Witnesses...)
					decideFrom(edge.To)
					continue
				}
			}
			decision.Activated = append(decision.Activated, edge.To)
		}
	}
	decideFrom(from)
	return decision
}

// SelectRouteTarget freezes an LLM-decided choice only after ChatAgent actually
// advances one of its candidates. Until then every hinted exit remains
// Reachable. Machine-decided schema-v3 routes keep their existing behavior.
func SelectRouteTarget(graph *CompiledStateGraph, from, target string, decision RouteDecision) RouteDecision {
	route := graph.StartRoute
	if node, ok := graph.Nodes[from]; ok {
		route = node.Route
	}
	if route != "choice" {
		return decision
	}
	hasLLMHint := false
	for _, edge := range graph.ControlEdges {
		if edge.From == from && (edge.When != "" || edge.Legacy != "") {
			hasLLMHint = true
			break
		}
	}
	if !hasLLMHint {
		return decision
	}
	selected := false
	for _, candidate := range decision.Activated {
		if candidate == target {
			selected = true
			break
		}
	}
	if !selected {
		return decision
	}
	result := decision
	result.Activated = []string{target}
	for _, candidate := range decision.Activated {
		if candidate != target {
			result.Pruned = appendUnique(result.Pruned, candidate)
		}
	}
	return result
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// Project calculates the full live state from immutable graph and persisted facts.
// It performs no writes and is safe to call both inside a transition transaction
// and from read-only projection handlers.
func Project(graph *CompiledStateGraph, snapshot RuntimeSnapshot) Projection {
	projection := Projection{Nodes: map[string]NodeProjection{}}
	latest := map[string]AttemptFact{}
	stale := map[string]bool{}
	for _, attempt := range snapshot.Attempts {
		if attempt.Validity == "stale" {
			stale[attempt.StepID] = true
			continue
		}
		latest[attempt.StepID] = attempt
	}
	materials := snapshot.Materials
	routes := map[string]RouteFact{}
	staleEdgeStates := map[string]bool{}
	for _, route := range snapshot.Routes {
		if route.Validity == "stale" {
			for _, to := range append(append([]string{}, route.Activated...), route.Pruned...) {
				staleEdgeStates[route.From+"->"+to] = true
			}
			for _, bypassID := range route.Bypassed {
				for _, edge := range graph.ControlEdges {
					if edge.From == bypassID || edge.To == bypassID {
						staleEdgeStates[projectedEdgeKey(edge)] = true
					}
				}
			}
		} else {
			routes[route.From] = route
		}
	}
	edgesByFrom := map[string][]CompiledEdge{}
	for _, edge := range graph.ControlEdges {
		edgesByFrom[edge.From] = append(edgesByFrom[edge.From], edge)
	}
	activeTargets, prunedTargets, bypassed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	edgeStates := map[string]string{}

	var activateFrom func(string)
	activateFrom = func(from string) {
		edges := edgesByFrom[from]
		if len(edges) == 0 {
			return
		}
		var activated, pruned, frozenBypassed []string
		if fact, ok := routes[from]; ok {
			activated, pruned, frozenBypassed = fact.Activated, fact.Pruned, fact.Bypassed
		} else {
			decision := DecideRoute(graph, from, materials)
			activated, pruned, frozenBypassed = decision.Activated, decision.Pruned, decision.Bypassed
		}
		for _, id := range frozenBypassed {
			bypassed[id] = true
			for _, edge := range graph.ControlEdges {
				if edge.From == id || edge.To == id {
					edgeStates[projectedEdgeKey(edge)] = "bypassed"
				}
			}
		}
		for _, to := range pruned {
			prunedTargets[to] = true
			edgeStates[from+"->"+to] = "pruned"
		}
		for _, to := range activated {
			edgeStates[from+"->"+to] = "active"
			if to == "__end__" {
				projection.EndReached = true
				continue
			}
			activeTargets[to] = true
		}
	}
	activateFrom("__start__")
	for id, attempt := range latest {
		if attempt.Status == "succeeded" {
			activateFrom(id)
		}
	}

	for id := range graph.Nodes {
		attempt, hasAttempt := latest[id]
		node := NodeProjection{ID: id, Execution: "none", Validity: "effective", Reachability: "unreachable", Readiness: "not_applicable", Branch: "active", Evaluation: Evaluation{Satisfied: true}}
		if stale[id] {
			projection.Stale = append(projection.Stale, id)
			if !hasAttempt {
				node.Validity = "stale"
			}
		}
		if hasAttempt {
			node.Execution = attempt.Status
			switch attempt.Status {
			case "succeeded":
				projection.Past = append(projection.Past, id)
			case "pending", "running", "waiting", "failed", "interrupted":
				projection.Current = append(projection.Current, id)
			}
		}
		if bypassed[id] {
			node.Branch = "bypassed"
			projection.Bypassed = append(projection.Bypassed, id)
		}
		if prunedTargets[id] && !activeTargets[id] {
			node.Branch = "pruned"
			projection.Pruned = append(projection.Pruned, id)
		}
		terminalAttempt := hasAttempt && (attempt.Status == "succeeded" || attempt.Status == "pending" || attempt.Status == "running" || attempt.Status == "waiting" || attempt.Status == "failed" || attempt.Status == "interrupted")
		if activeTargets[id] && !terminalAttempt && !bypassed[id] {
			node.Reachability = "reachable"
			node.Evaluation = Evaluate(graph.Nodes[id].Input, materials)
			projection.Reachable = append(projection.Reachable, id)
			if node.Evaluation.Satisfied {
				node.Readiness = "ready"
				projection.Ready = append(projection.Ready, id)
			} else {
				node.Readiness = "blocked"
				projection.Blocked = append(projection.Blocked, id)
			}
		}
		projection.Nodes[id] = node
	}
	for _, edge := range graph.ControlEdges {
		key := projectedEdgeKey(edge)
		state := edgeStates[key]
		if state == "" {
			if staleEdgeStates[key] {
				state = "stale"
			} else {
				state = "inactive"
			}
		}
		when := edge.When
		if when == "" {
			when = edge.Legacy
		}
		projection.Edges = append(projection.Edges, ProjectedEdge{From: edge.From, To: edge.To, State: state, When: when})
	}
	projection.Completed = projection.EndReached && len(projection.Current) == 0 && len(projection.Ready) == 0 && len(projection.Blocked) == 0
	sortProjection(&projection)
	return projection
}

func projectedEdgeKey(edge CompiledEdge) string {
	if edge.ID != "" {
		return edge.ID
	}
	return edge.From + "->" + edge.To
}

func sortProjection(p *Projection) {
	for _, values := range []*[]string{&p.Past, &p.Current, &p.Reachable, &p.Ready, &p.Blocked, &p.Stale, &p.Pruned, &p.Bypassed} {
		sort.Strings(*values)
	}
}
