package workflowmcp

import "encoding/json"

// Core decides admission. This formatter describes only actions permitted by
// the returned state, and is shared by reads and mutation receipts.
func stateInstruction(state Projection) string {
	continuation, _ := state.Control["continuation"].(string)
	admission, _ := state.Control["admission"].(map[string]any)
	expired, _ := state.Control["expired_execution_ids"].([]any)
	if (continuation == "continue" || continuation == "draining") && len(expired) > 0 {
		ids, _ := json.Marshal(expired)
		return "An external step lease expired. Call workflow.step.resume for an execution_id from control.expired_execution_ids=" + string(ids) + ". Use the new handle and reuse saved outputs."
	}
	switch continuation {
	case "continue":
		if admission["can_begin"] != true {
			return "Follow the returned admission reason before beginning another step."
		}
		if retries, _ := state.Control["automatic_retry_steps"].([]any); len(retries) > 0 {
			steps, _ := json.Marshal(retries)
			return "A step failed but automatic retry budget remains. Call workflow.step.begin for control.automatic_retry_steps=" + string(steps) + ". Keep this run and correct the reported failure before retrying."
		}
		return "Call workflow.step.begin for a returned ready step. Use workflow.step.claim for an existing execution_id. Human review, when required, follows step execution."
	case "awaiting_user":
		if state.Control["input_request"] != nil {
			return "Ask the exact questions in control.input_request and wait for the user's actual reply. Save it with workflow.input.answer using the returned artifact_id, revision and state_version. Read workflow.state afterward."
		}
		return "Human review is required. If the user explicitly asks to rerun the step awaiting review, call workflow.step.begin for that exact projection.rewindable step with retry_origin=user and their actual user_input. Core replaces the pending result and review, then requires review after execution. Otherwise tell the user to review in the Workflow panel and END this turn. Never approve the result yourself or advance another step while review is pending."
	case "stopped":
		return "The Workflow is stopped. END this turn. On an explicit user request to continue, call workflow.session.resume. Claim a returned execution_id with workflow.step.claim, or read workflow.state."
	case "completed":
		return "The Workflow is completed. Deliver saved artifact links and END this turn. For a new user-requested edit, call workflow.edit.context on this run. An explicit rerun uses a projection.rewindable step with workflow.step.begin."
	case "failed":
		return "The Workflow failed. Report the failure and END this turn. An explicit user retry uses a projection.retryable step with workflow.step.begin and retry_origin=user. Use workflow.edit.context for requested artifact edits, or a projection.rewindable step for an explicit rerun. Keep this run."
	case "binding_required":
		return "The Workflow needs its host connection restored. Show the returned connection guidance and END this turn. Read workflow.state after reconnection."
	case "awaiting_executor", "draining":
		return "Execution is still settling. END this turn and wait for its notification. Read workflow.state on a new notification or explicit user input."
	default:
		return ""
	}
}
