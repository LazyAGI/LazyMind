package chat

import "encoding/json"

// Ordinary clients receive lifecycle notifications and hydrate public snapshots.
// In particular, embedded raw task events and driver prompts never cross this path.
func ordinaryConversationEvent(event *ConvEvent) *ConvEvent {
	switch event.Type {
	case "task_created", "task_updated", "artifact_created", "driver_input", "auto_chat_started",
		"workflow_step_feedback", "workflow_runtime_updated", "step_waiting", "workflow_completed",
		"workflow_error", "step_partial_done", "intent_updated", "workflow_artifact_updated",
		"workflow_session_created", "ask_pending", "max_retries_exceeded", "driver_fallback":
	default:
		return nil
	}
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return nil
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	public := map[string]any{}
	for _, key := range []string{"task_id", "session_id", "conversation_id", "history_id", "trigger_history_id", "step_id", "artifact_id", "agent_type", "status"} {
		if value, ok := payload[key].(string); ok {
			public[key] = value
		}
	}
	return &ConvEvent{Type: event.Type, Payload: public, Replayed: event.Replayed}
}
