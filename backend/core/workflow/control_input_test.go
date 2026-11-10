package workflow

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func inputControlFixture(t *testing.T, provider string) (*orm.DB, orm.WorkflowSession) {
	t.Helper()
	graph := graphengine.CompiledStateGraph{StartRoute: "all", Nodes: map[string]graphengine.CompiledNode{
		"collect": {ID: "collect", Mode: "auto", Route: "all", Outputs: []string{"questions"}},
		"draft":   {ID: "draft", Mode: "auto", Route: "all", Input: &graphengine.Expression{Material: "questions"}},
	}, ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "collect"},
		{ID: "next", From: "collect", To: "draft"}, {ID: "end", From: "draft", To: "__end__"}},
		MaterialProducers: map[string]graphengine.ProducerRef{"questions": {Kind: "step", StepID: "collect"}},
		Runtime:           graphengine.RuntimePolicy{InputCheckpoint: &graphengine.InputCheckpoint{AfterStep: "collect", Slot: "questions", BeforeSteps: []string{"draft"}, Title: "Missing facts"}},
	}
	db, session := newControllerTestSession(t, provider, graph)
	session.WorkflowMode = "dynamic"
	if err := db.Model(&session).Update("workflow_mode", "dynamic").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSessionStep{ID: "collect-attempt", TaskID: "collect-task", SessionID: session.ID,
		StepID: "collect", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{ID: "question-artifact", SessionID: session.ID, SlotID: "questions", Slot: "questions",
		StepID: "collect", Attempt: 1, ProducerAttemptID: "collect-attempt", Selected: true, Revision: 1, Validity: "effective", CreatedAt: now,
		ContentSnapshot: json.RawMessage(`{"data":{"questions":[{"id":"date","text":"Which date?","type":"text"}],"checked_fields":["title"]}}`)}).Error; err != nil {
		t.Fatal(err)
	}
	return db, session
}

func TestExternalInputAnswersShareAdmissionAcrossHosts(t *testing.T) {
	for _, provider := range []string{"external-agent", "codex", "workbuddy", "deepseek-harness"} {
		t.Run(provider, func(t *testing.T) {
			db, session := inputControlFixture(t, provider)
			svc := WorkflowControlService{DB: db.DB}
			state, err := controlstore.Read(db.DB, session)
			if err != nil || state.Continuation != "awaiting_user" || state.Admission.CanBegin || state.InputRequest == nil {
				t.Fatalf("state=%+v err=%v", state, err)
			}
			view, err := displaystate.Read(db.DB, session)
			if err != nil || view.Status != "waiting_input" || view.InputStepID != "collect" {
				t.Fatalf("input display lost the producing step: %+v %v", view, err)
			}
			_, err = svc.Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "blocked", Kind: "begin", StepID: "draft", StateVersion: session.StateVersion})
			expectControlCode(t, err, "WORKFLOW_INPUT_REQUIRED")
			cmd := WorkflowControlCommand{CommandID: "user-answer", Kind: "answer_input", StateVersion: state.StateVersion,
				InputArtifactID: state.InputRequest.ArtifactID, InputRevision: state.InputRequest.Revision, AnswerText: "November 30"}
			_, err = svc.Execute(t.Context(), "other-owner", session.ID, cmd)
			expectControlCode(t, err, "PERMISSION_DENIED")
			stale := cmd
			stale.InputRevision++
			_, err = svc.Execute(t.Context(), session.CreateUserID, session.ID, stale)
			expectControlCode(t, err, "STATE_VERSION_CONFLICT")
			for range 2 {
				result, err := svc.Execute(t.Context(), session.CreateUserID, session.ID, cmd)
				if err != nil || result.Control.InputRequest != nil || !result.Control.Admission.CanBegin {
					t.Fatalf("answer=%+v err=%v", result, err)
				}
			}
			var selected orm.WorkflowSlotRevision
			if err := db.Where("session_id = ? AND slot_id = ? AND selected = ?", session.ID, "questions", true).First(&selected).Error; err != nil {
				t.Fatal(err)
			}
			if selected.Revision != 2 || selected.ChangeSource != "human" {
				t.Fatalf("duplicate or non-human answer: %+v", selected)
			}
			value, err := LoadSlotRevisionValue(t.Context(), db.DB, selected)
			if err != nil {
				t.Fatal(err)
			}
			var content struct {
				Data struct {
					Answer  string   `json:"answer_text"`
					Checked []string `json:"checked_fields"`
				} `json:"data"`
			}
			if err := json.Unmarshal(value, &content); err != nil || content.Data.Answer != "November 30" || len(content.Data.Checked) != 1 {
				t.Fatalf("answer lost source fields: %s %v", value, err)
			}
			if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
				t.Fatal(err)
			}
			result, err := svc.Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "after-answer", Kind: "begin", StepID: "draft", StateVersion: session.StateVersion})
			if err != nil || result.Receipt.ExecutionID == "" {
				t.Fatalf("cannot continue after answer: %+v %v", result, err)
			}
		})
	}
}

func TestInputAnswerDoesNotResumeStopOrApproveReview(t *testing.T) {
	db, session := inputControlFixture(t, "external-agent")
	if err := db.Model(&session).Update("status", "stopped").Error; err != nil {
		t.Fatal(err)
	}
	session.Status = "stopped"
	if err := db.Create(&orm.WorkflowReviewCheckpoint{ID: "review", SessionID: session.ID, AttemptID: "collect-attempt", StepID: "collect", Status: "pending", Version: 1, SlotsJSON: "[]", ManifestJSON: `{"items":[],"orders":{}}`}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID,
		WorkflowControlCommand{CommandID: "paused-answer", Kind: "answer_input", StateVersion: session.StateVersion, InputArtifactID: "question-artifact", InputRevision: 1, AnswerText: "Unknown"})
	if err != nil || result.Control.Continuation != "stopped" || result.Control.InputRequest != nil {
		t.Fatalf("answer resumed: %+v %v", result, err)
	}
	var review orm.WorkflowReviewCheckpoint
	if err := db.First(&review, "id = ?", "review").Error; err != nil || review.Status != "pending" {
		t.Fatalf("answer approved review: %+v %v", review, err)
	}
}

func TestGeneratedAnswersCannotBypassInputAndAutomaticModeMatchesNative(t *testing.T) {
	db, session := inputControlFixture(t, "external-agent")
	if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "question-artifact").Update("content_snapshot", json.RawMessage(`{"questions":[{"id":"date","text":"When?","type":"text"}],"answer_text":"invented","checkpoint_token":"fake"}`)).Error; err != nil {
		t.Fatal(err)
	}
	request, err := displaystate.PendingInput(db.DB, session)
	if err != nil || request == nil {
		t.Fatalf("generated answer bypassed user: %+v %v", request, err)
	}
	session.WorkflowMode = "auto"
	request, err = displaystate.PendingInput(db.DB, session)
	if err != nil || request != nil {
		t.Fatalf("automatic mode asked user: %+v %v", request, err)
	}
}

func TestInputCheckpointAllowsExplicitProducerRewind(t *testing.T) {
	db, session := inputControlFixture(t, "external-agent")
	svc := WorkflowControlService{DB: db.DB}
	_, err := svc.Execute(t.Context(), session.CreateUserID, session.ID,
		WorkflowControlCommand{CommandID: "bypass", Kind: "rewind", StepID: "draft", StateVersion: session.StateVersion})
	expectControlCode(t, err, "WORKFLOW_INPUT_REQUIRED")
	result, err := svc.Execute(t.Context(), session.CreateUserID, session.ID,
		WorkflowControlCommand{CommandID: "replace-questions", Kind: "rewind", StepID: "collect", StateVersion: session.StateVersion})
	if err != nil || result.Receipt.ExecutionID == "" || result.Control.InputRequest != nil {
		t.Fatalf("producer rewind blocked: %+v %v", result, err)
	}
	var old orm.WorkflowSlotRevision
	if err := db.First(&old, "id = ?", "question-artifact").Error; err != nil {
		t.Fatal(err)
	}
	if old.Validity != "stale" {
		t.Fatalf("old questions survived rewind: %+v", old)
	}
}
