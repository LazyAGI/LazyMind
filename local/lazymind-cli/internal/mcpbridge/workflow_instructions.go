package mcpbridge

// Execution guidance is unchanged and shared by every host. Only native
// identity and panel presentation are selected explicitly by host.
const workflowStartInstructions = "For a selected LazyMind Workflow, call workflow.start. "

const workflowExecutionInstructions = "Then call workflow.step.begin for a ready step according to state. When the user asks to continue, read workflow.state first; claim an existing active execution with workflow.step.claim instead of beginning a duplicate. Execute a step_contract only with an execution_handle; publish each output with workflow.artifact.publish as it is generated, then send only the outcome with workflow.step.complete using that handle unchanged. When executor_host is lazymind, end the current turn and wait for a notification. In external steps, save_artifact/save_artifacts means publish each output immediately (key becomes slot) with a stable positive seq per slot, wait for its acknowledgement, and complete only when execution finishes. Read inputs from step_contract.inputs; resolve references with workflow.artifact.get or workflow.input.get, and use workflow.artifact.list to find an artifact ID by slot key. Follow control.continuation. On awaiting_user, tell the user to review in the panel and END this turn immediately. Do not approve the review yourself, start another step, or poll while waiting. On awaiting_executor, stopped, completed, or binding_required, also end this turn. Resume only on a new notification or explicit user input, then read workflow.state first. "

const workflowFinishInstructions = "Use a stable idempotency_key for start retries. Report completion only when workflow.state confirms completed, then deliver artifact links from workflow.artifact.list."

const codexPanelInstructions = "Immediately after a successful start, automatically open the returned interaction_url in the Codex built-in browser using mcp__codex_app__open_in_codex (open_in_codex), with target.type=browser, target.url=interaction_url, and placement=bottom. Discover the host tool if needed. Do not ask the user to click a link. Open once per new session, not on each state read. If the host browser tool is unavailable or fails, provide the returned link and explain the limitation; do not claim the panel opened. Opening the browser does not itself advance steps or guarantee automatic agent wakeup after user interaction. "

const codexIdentityInstructions = "In Codex queue integration, workflow.start binds the current thread using host metadata; when metadata is missing, obtain CODEX_THREAD_ID from the current host shell and pass it as driver_session_id. Never invent the ID. "

const workflowInstructions = workflowStartInstructions + workflowExecutionInstructions + workflowFinishInstructions

var workflowHostHints = map[string]struct{ panel, identity string }{
	"codex": {panel: codexPanelInstructions, identity: codexIdentityInstructions},
	"workbuddy": {
		panel:    "The Workflow panel appears automatically at the end of this conversation after binding. ",
		identity: "Call workflow.start with an explicit unique idempotency_key of 16–256 characters; preserve it and the returned session_id on retries. Native conversation identity is resolved by the WorkBuddy CDP connector; never pass or guess driver_session_id. ",
	},
}

func workflowInstructionsForHost(provider string) string {
	hints, ok := workflowHostHints[provider]
	if !ok {
		return workflowInstructions
	}
	return workflowStartInstructions + hints.panel + workflowExecutionInstructions + hints.identity + workflowFinishInstructions
}
