package chat

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
)

func TestValidateWorkspaceAskSubmissionMatchesLatestPendingCard(t *testing.T) {
	histories := []orm.ChatHistory{{ID: "history", Ext: json.RawMessage(`{"ask_pending":{"ask_id":"ask-current"}}`)}}
	for _, tc := range []struct {
		name string
		raw  map[string]any
		want bool
	}{
		{"matching", map[string]any{"ask_answers_structured": map[string]any{"ask_id": "ask-current"}}, true},
		{"different", map[string]any{"ask_answers_structured": map[string]any{"ask_id": "ask-other"}}, false},
		{"missing", map[string]any{"ask_answers_structured": map[string]any{}}, false},
		{"no submission", map[string]any{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateWorkspaceAskSubmission(histories, tc.raw) == nil; got != tc.want {
				t.Fatalf("valid=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestValidateWorkspaceAskSubmissionChecksQuestionStructure(t *testing.T) {
	histories := []orm.ChatHistory{{ID: "history", Ext: json.RawMessage(`{"ask_pending":{"ask_id":"ask-current","questions":[{"text":"Choose","type":"single","choices":["A","B"]},{"text":"Note","type":"text"}]}}`)}}
	valid := map[string]any{"ask_answers_structured": map[string]any{"ask_id": "ask-current", "questions": []any{
		map[string]any{"text": "Choose", "type": "single", "choices": []any{"A", "B"}, "custom_choices": []any{"A", "B"}, "answer": map[string]any{"type": "single", "value": "A"}},
		map[string]any{"text": "Note", "type": "text", "choices": []any{}, "custom_choices": []any{}, "answer": nil},
	}}}
	if err := validateWorkspaceAskSubmission(histories, valid); err != nil {
		t.Fatalf("valid submission rejected: %v", err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"question text": func(item map[string]any) { item["text"] = "Forged" },
		"answer type":   func(item map[string]any) { item["answer"] = map[string]any{"type": "multiple", "value": []any{"A"}} },
		"choices":       func(item map[string]any) { item["choices"] = []any{"A"} },
	} {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(valid)
			copy := map[string]any{}
			_ = json.Unmarshal(body, &copy)
			questions := copy["ask_answers_structured"].(map[string]any)["questions"].([]any)
			mutate(questions[0].(map[string]any))
			if validateWorkspaceAskSubmission(histories, copy) == nil {
				t.Fatal("invalid submission accepted")
			}
		})
	}
}
