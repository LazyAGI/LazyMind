package mcpbridge

// Connection-wide guidance stays independent of a Workflow's business domain.
// Operation details belong to tool schemas and current-state responses.
const workflowInstructions = `LazyMind executes revision-bound Workflows. Use workflow.list and workflow.get to discover a Workflow and its startup inputs, then workflow.start to create a run. For an existing run, read workflow.state. Follow returned control.continuation, admission and agent_instruction for the next action.

Execute the current step_contract using its prompt, execution Skills and constraints, inputs, outputs and acceptance criteria. Read the supplied Skills in full. Use the host's own capabilities first for business operations. When those capabilities are insufficient, discover an authorized fallback with workflow.tool.list and invoke it with workflow.tool.call. Follow the listed schema, availability and returned error guidance.

The bound controller holds the execution_handle and submits Workflow state changes. Prefer native subagents for step execution when the host supports them; the controller coordinates workers and publishes their results. When subagents are unavailable, execute directly. Give workers the complete step contract and inputs, with no execution_handle or credentials. The executor checks the actual results against the step contract and returns artifacts and concise verification evidence. Subagents are optional at the protocol level. Respect host concurrency limits and identify same-agent validation accurately.

Publish results through workflow.artifact.publish and complete the step after publication acknowledgements. Follow the contract's output order. The user supplies requested answers and review decisions. Report completion only when workflow.state confirms completed, then deliver the saved artifacts. Keep routine narration brief and communicate required input, review, configuration, failures and final delivery.`
