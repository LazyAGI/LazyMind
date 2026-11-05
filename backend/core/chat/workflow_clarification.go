package chat

import (
	"encoding/json"

	"lazymind/core/common/askuser"
	"lazymind/core/common/orm"
)

// Only the persisted, owned card determines the workflow identity and previous
// values. The browser submits answers, never a replacement launch context.
func workflowAskContinuation(histories []orm.ChatHistory, submission map[string]any) map[string]any {
	index := submittedAskHistoryIndex(histories, submission)
	if index < 0 {
		return nil
	}
	var ext struct {
		Pending struct {
			Workflow map[string]string `json:"workflow"`
		} `json:"ask_pending"`
	}
	if json.Unmarshal(histories[index].Ext, &ext) != nil || ext.Pending.Workflow["workflow_ref"] == "" {
		return nil
	}
	metadata := ext.Pending.Workflow
	values := map[string]any{}
	if encoded := metadata["values_json"]; encoded != "" {
		if json.Unmarshal([]byte(encoded), &values) != nil {
			return nil
		}
	}
	body, _ := json.Marshal(submission)
	var payload askAnswersStructuredPayload
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	for _, question := range payload.Questions {
		if question.ID == "" || len(question.Answer) == 0 || string(question.Answer) == "null" {
			continue
		}
		var answer struct {
			Value any    `json:"value"`
			Other string `json:"otherText"`
		}
		if json.Unmarshal(question.Answer, &answer) != nil {
			return nil
		}
		if text, ok := answer.Value.(string); ok && text == askuser.Other {
			answer.Value = answer.Other
		}
		if list, ok := answer.Value.([]any); ok {
			for i, value := range list {
				if value == askuser.Other {
					list[i] = answer.Other
				}
			}
		}
		values[question.ID] = answer.Value
	}
	return map[string]any{"workflow_ref": metadata["workflow_ref"], "revision_id": metadata["revision_id"],
		"startup_request_id": metadata["startup_request_id"], "original_request": metadata["original_request"],
		"values": values}
}
