package chat

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
)

func workflowQuestionFixture(t *testing.T) ([]orm.ChatHistory, map[string]any) {
	t.Helper()
	pending := map[string]any{"ask_id": "ask", "workflow": map[string]string{
		"workflow_ref": "builtin:academic", "revision_id": "r1", "startup_request_id": "launch-1",
		"original_request": "Write a paper", "values_json": `{"research_topic":"RAG","output_format":"docx"}`,
	}, "questions": []any{map[string]any{"id": "word_target", "text": "Length?", "type": "text",
		"required": true, "validation": map[string]any{"format": "positive_integer", "max": 500000}}}}
	ext, err := json.Marshal(map[string]any{"ask_pending": pending})
	if err != nil {
		t.Fatal(err)
	}
	submitted := map[string]any{"ask_id": "ask", "questions": []any{map[string]any{
		"id": "word_target", "text": "Length?", "type": "text", "choices": []any{}, "custom_choices": []any{},
		"answer": map[string]any{"type": "text", "value": "8000"},
	}}}
	return []orm.ChatHistory{{ID: "owned", Ext: ext}}, submitted
}

func TestWorkflowAskContinuationUsesOwnedIdentityAndStableField(t *testing.T) {
	history, submission := workflowQuestionFixture(t)
	submission["workflow"] = map[string]any{"workflow_ref": "forged", "revision_id": "r2"}
	if err := validateWorkspaceAskSubmission(history, map[string]any{"ask_answers_structured": submission}); err != nil {
		t.Fatal(err)
	}
	state := workflowAskContinuation(history, submission)
	values, _ := state["values"].(map[string]any)
	if state["workflow_ref"] != "builtin:academic" || state["revision_id"] != "r1" ||
		state["startup_request_id"] != "launch-1" || state["original_request"] != "Write a paper" ||
		values["word_target"] != "8000" || values["research_topic"] != "RAG" || values["output_format"] != "docx" {
		t.Fatalf("invalid continuation: %#v", state)
	}
}

func TestWorkflowAskRejectsMissingInvalidStaleOrForgedAnswer(t *testing.T) {
	for _, scenario := range []string{"missing", "zero", "fraction", "overflow", "field", "ask", "type", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			history, submission := workflowQuestionFixture(t)
			question := submission["questions"].([]any)[0].(map[string]any)
			answer := question["answer"].(map[string]any)
			switch scenario {
			case "missing":
				question["answer"] = nil
			case "zero":
				answer["value"] = "0"
			case "fraction":
				answer["value"] = "1.5"
			case "overflow":
				answer["value"] = "500001"
			case "field":
				question["id"] = "other_field"
			case "ask":
				submission["ask_id"] = "unknown"
			case "type":
				answer["value"] = 8000
			case "stale":
				var ext map[string]any
				if err := json.Unmarshal(history[0].Ext, &ext); err != nil {
					t.Fatal(err)
				}
				ext["ask_answered"] = true
				history[0].Ext, _ = json.Marshal(ext)
			}
			if validateWorkspaceAskSubmission(history, map[string]any{"ask_answers_structured": submission}) == nil ||
				workflowAskContinuation(history, submission) != nil {
				t.Fatal("invalid answer accepted")
			}
		})
	}
	if validateWorkspaceAskSubmission(nil, map[string]any{"ask_answers_structured": "invalid"}) == nil {
		t.Fatal("malformed payload accepted")
	}
}
