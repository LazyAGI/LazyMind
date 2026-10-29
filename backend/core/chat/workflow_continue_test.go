package chat

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow"
	"lazymind/core/workflow/displaystate"
)

func TestExactWorkflowContinueAllowlist(t *testing.T) {
	session := orm.WorkflowSession{ID: "run", StateVersion: 7, ControllerHost: "lazymind"}
	view := displaystate.View{Status: "waiting", ApprovalStepID: "review"}
	for _, text := range []string{
		"继续", "继续执行", "继续吧", "请继续", "请继续执行", "接着做", "继续下一步", "执行下一步", "进入下一步", "下一步",
		"生成", "继续生成",
		"确认", "确认继续", "确认执行", "同意", "同意继续", "批准", "批准继续", "通过", "审核通过",
		"好", "好的", "好，继续", "好的，继续", "可以", "可以继续", "没问题", "没问题，继续",
		"continue", "Continue", "CONTINUE", "please continue", "Please continue",
		"proceed", "Proceed", "go ahead", "Go ahead", "go on", "next", "Next", "next step",
		"ok", "OK", "Ok", "okay", "Okay", "yes", "Yes", "YES", "sure", "Sure",
		"confirm", "Confirm", "approve", "Approve", "approved", "Approved",
	} {
		for _, query := range []string{text, " \t" + text + "\n\u3000"} {
			command := exactWorkflowContinueCommand(query, session, view)
			if command == nil || command.Kind != "approve" || command.StepID != "review" || command.ExpectedStateVersion != 7 {
				t.Fatalf("exact consent %q: %+v", query, command)
			}
			resume := exactWorkflowContinueCommand(query, session, displaystate.View{Status: "paused"})
			if resume == nil || resume.Kind != "resume" || resume.StepID != "" {
				t.Fatalf("exact resume %q: %+v", query, resume)
			}
		}
		for _, query := range []string{"不要" + text, text + "，但先修改", text + "?", text + "\n不要执行"} {
			if exactWorkflowContinueCommand(query, session, view) != nil {
				t.Fatalf("extended utterance must stay on LLM path: %q", query)
			}
		}
	}
	for _, query := range []string{"", "不要继续", "先别继续", "继续，但把数量减到2张", "先解释一下再继续", "do not continue", "cOnTiNuE", "继续。", "continue!", "继 续", "继续\n不要执行", "尚未确认", "不同意", "不可以", "好的，但先别执行", "yes, but wait", "not approved", "go  ahead", "next\nstep"} {
		if command := exactWorkflowContinueCommand(query, session, view); command != nil {
			t.Fatalf("non-allowlisted text must remain on LLM path: %q: %+v", query, command)
		}
	}
	for _, status := range []string{"running", "failed", "completed", "waiting"} {
		if command := exactWorkflowContinueCommand("继续", session, displaystate.View{Status: status}); command != nil {
			t.Fatalf("no valid checkpoint for %s: %+v", status, command)
		}
	}
	session.Dismissed = true
	if exactWorkflowContinueCommand("继续", session, view) != nil {
		t.Fatal("dismissed workflow must not continue")
	}
}

func TestExactContinueUsesNativeAdmissionAndVersionCheck(t *testing.T) {
	db := newSidechatTestDB(t)
	sidechatTestConversation(t, db, "exact-chat", "owner", "continue")
	now := time.Now().UTC()
	session := orm.WorkflowSession{ID: "exact-run", ConversationID: "exact-chat", CreateUserID: "owner",
		WorkflowID: "test", Status: "waiting", ControllerHost: "lazymind", LastStoppedAt: &now, StateVersion: 2}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db, session)
	if err != nil {
		t.Fatal(err)
	}
	command := exactWorkflowContinueCommand(" 继续 ", session, view)
	if command == nil || command.Kind != "resume" {
		t.Fatalf("expected resume: %+v, %+v", view, command)
	}
	command.HistoryID = "continued-turn"
	stale := *command
	stale.ExpectedStateVersion--
	if _, err := workflow.BeginNativeControlTurn(t.Context(), db, "owner", "exact-chat", stale); err == nil {
		t.Fatal("stale continuation accepted")
	}
	if _, err := workflow.BeginNativeControlTurn(t.Context(), db, "other", "exact-chat", *command); err == nil {
		t.Fatal("other owner continuation accepted")
	}
	version, err := workflow.BeginNativeControlTurn(t.Context(), db, "owner", "exact-chat", *command)
	if err != nil {
		t.Fatal(err)
	}
	if err := workflow.FinishNativeControlTurn(t.Context(), db, session.ID, version); err != nil {
		t.Fatal(err)
	}
}
