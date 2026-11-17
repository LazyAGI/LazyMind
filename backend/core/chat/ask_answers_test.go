package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestAskAutosaveBindsCurrentQuestion(t *testing.T) {
	for _, askID := range []string{"current", "previous", ""} {
		t.Run("ask_"+askID, func(t *testing.T) {
			db := setupUserEnvTest(t)
			conv := orm.Conversation{ID: "ask-conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}
			if err := db.Create(&conv).Error; err != nil {
				t.Fatal(err)
			}
			h := orm.ChatHistory{ID: "ask-history", ConversationID: conv.ID, Ext: json.RawMessage(`{"ask_pending":{"ask_id":"current","questions":[{"text":"When?","type":"single","choices":["Monday"]}]}}`)}
			if err := db.Create(&h).Error; err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			body := fmt.Sprintf(`{"history_id":"ask-history","ask_id":%q,"answers":{"0":{"type":"single","value":"Monday","otherText":""}}}`, askID)
			SaveAskAnswers(rec, newUserEnvRequest(http.MethodPatch, "/unused", body, "owner", nil))
			var saved orm.ChatHistory
			if err := db.First(&saved, "id = ?", h.ID).Error; err != nil {
				t.Fatal(err)
			}
			if askID == "current" {
				if rec.Code != http.StatusNoContent || !strings.Contains(string(saved.Ext), "ask_saved_answers") {
					t.Fatalf("valid save: %d %s", rec.Code, saved.Ext)
				}
			} else {
				if rec.Code < 400 || string(saved.Ext) != string(h.Ext) {
					t.Fatalf("stale save accepted: %d %s", rec.Code, saved.Ext)
				}
			}
		})
	}
}

func TestAskSubmissionChecksSelectedOptions(t *testing.T) {
	for _, tc := range []struct {
		name, kind, value, other string
		choices                  []string
		valid                    bool
	}{
		{"single_valid", "single", `"Monday"`, "", []string{"Monday"}, true},
		{"single_stale", "single", `"OLD"`, "", []string{"Monday"}, false},
		{"boolean_valid", "boolean", `"__ask_user_no__"`, "", []string{"__ask_user_yes__", "__ask_user_no__"}, true},
		{"boolean_invalid", "boolean", `"maybe"`, "", []string{"__ask_user_yes__", "__ask_user_no__"}, false},
		{"multiple_valid", "multiple", `["Monday"]`, "", []string{"Monday", "Tuesday"}, true},
		{"multiple_stale", "multiple", `["Monday","OLD"]`, "", []string{"Monday", "Tuesday"}, false},
		{"other_valid", "single", `"__ask_user_other__"`, "Friday", []string{"Monday", "__ask_user_other__"}, true},
		{"other_empty", "single", `"__ask_user_other__"`, " ", []string{"Monday", "__ask_user_other__"}, false},
		{"other_not_allowed", "single", `"__ask_user_other__"`, "Friday", []string{"Monday"}, false},
		{"multiple_other_valid", "multiple", `["Monday","__ask_user_other__"]`, "Friday", []string{"Monday", "__ask_user_other__"}, true},
		{"multiple_other_empty", "multiple", `["__ask_user_other__"]`, "", []string{"Monday", "__ask_user_other__"}, false},
		{"text", "text", `"Anything"`, "", []string{}, true},
		{"unanswered", "single", `null`, "", []string{"Monday"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			choices, _ := json.Marshal(tc.choices)
			var pending, submission map[string]any
			json.Unmarshal([]byte(fmt.Sprintf(`{"ask_id":"card","questions":[{"text":"Question?","type":%q,"choices":%s}]}`, tc.kind, choices)), &pending)
			answer := fmt.Sprintf(`{"type":%q,"value":%s,"otherText":%q}`, tc.kind, tc.value, tc.other)
			if tc.value == "null" {
				answer = "null"
			}
			json.Unmarshal([]byte(fmt.Sprintf(`{"ask_id":"card","questions":[{"text":"Question?","type":%q,"choices":%s,"custom_choices":%s,"answer":%s}]}`, tc.kind, choices, choices, answer)), &submission)
			if got := validAskSubmission(pending, submission); got != tc.valid {
				t.Fatalf("valid=%v, want %v", got, tc.valid)
			}
		})
	}
}

func TestAskSubmissionAllowsAllUnansweredOnlyForOptionalQuestions(t *testing.T) {
	questions := []any{}
	answers := []any{}
	for _, kind := range []string{"text", "single", "multiple", "boolean"} {
		questions = append(questions, map[string]any{
			"id": kind, "text": kind + "?", "type": kind, "required": false,
		})
		answers = append(answers, map[string]any{
			"id": kind, "text": kind + "?", "type": kind, "answer": nil,
		})
	}
	pending := map[string]any{"ask_id": "optional", "questions": questions}
	submission := map[string]any{"ask_id": "optional", "questions": answers}
	validate := func() error {
		ext, err := json.Marshal(map[string]any{"ask_pending": pending})
		if err != nil {
			t.Fatal(err)
		}
		return validateWorkspaceAskSubmission([]orm.ChatHistory{{Ext: ext}},
			map[string]any{"ask_answers_structured": submission})
	}
	if err := validate(); err != nil {
		t.Fatalf("all-optional empty submission rejected: %v", err)
	}
	for _, question := range questions {
		question := question.(map[string]any)
		question["required"] = true
		if validate() == nil {
			t.Fatalf("required %s question accepted without an answer", question["type"])
		}
		question["required"] = false
	}
}
