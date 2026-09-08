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
