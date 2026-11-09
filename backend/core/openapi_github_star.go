package main

func githubStarSchemas() map[string]any {
	claim := objReq([]string{"task_id"}, prop("task_id", strSchema()))
	claim["additionalProperties"] = false
	support := objReq([]string{"supported"}, prop("supported", map[string]any{"type": "boolean", "enum": []bool{true}}))
	support["additionalProperties"] = false
	return map[string]any{
		"GitHubStarPromptClaim":   claim,
		"GitHubStarPromptSupport": support,
		"GitHubStarPromptDecision": objReq([]string{"show", "reason", "task_id", "min_successful_tasks", "cooldown_days", "max_prompts"},
			prop("show", boolSchema()), prop("reason", strSchema()), prop("task_id", strSchema()), prop("min_successful_tasks", intSchema()), prop("cooldown_days", intSchema()), prop("max_prompts", intSchema())),
	}
}

func githubStarPaths() map[string]any {
	return map[string]any{
		"/task-center/github-star-prompt/claim": map[string]any{"post": op("Atomically claim an optional GitHub Star invitation after a successful complete Work task", nil, jsonBody(refSchema("GitHubStarPromptClaim"), true), response(200, "Invitation eligibility", refSchema("GitHubStarPromptDecision")))},
		"/user/github-star-prompt/support":      map[string]any{"put": op("Stop future invitations for the authenticated user; does not perform or prove a GitHub write", nil, jsonBody(refSchema("GitHubStarPromptSupport"), true), response(200, "Invitation preference saved", obj(prop("code", intSchema()), prop("message", strSchema()), prop("data", obj(prop("supported", boolSchema()))))))},
	}
}
