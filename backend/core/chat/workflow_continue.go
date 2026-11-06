package chat

import (
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/workflow"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
)

// Only trim surrounding whitespace. Never strip punctuation, fold case or
// search for a substring: every other utterance stays on the normal LLM path.
func isExactWorkflowContinue(query string) bool {
	switch strings.TrimSpace(query) {
	case "继续", "继续执行", "继续吧", "请继续", "请继续执行", "接着做", "继续下一步", "执行下一步", "进入下一步", "下一步",
		"生成", "继续生成",
		"确认", "确认继续", "确认执行", "同意", "同意继续", "批准", "批准继续", "通过", "审核通过",
		"好", "好的", "好，继续", "好的，继续", "可以", "可以继续", "没问题", "没问题，继续",
		"continue", "Continue", "CONTINUE", "please continue", "Please continue",
		"proceed", "Proceed", "go ahead", "Go ahead", "go on", "next", "Next", "next step",
		"ok", "OK", "Ok", "okay", "Okay", "yes", "Yes", "YES", "sure", "Sure",
		"confirm", "Confirm", "approve", "Approve", "approved", "Approved":
		return true
	default:
		return false
	}
}

// This only prepares a command; BeginNativeControlTurn rechecks ownership,
// version and checkpoint atomically after the Chat turn has been admitted.
func exactWorkflowContinueCommand(query string, session orm.WorkflowSession, view displaystate.View) *workflow.NativeControlCommand {
	if !isExactWorkflowContinue(query) || session.Dismissed || controlstore.Controlled(session) {
		return nil
	}
	command := &workflow.NativeControlCommand{SessionID: session.ID, ExpectedStateVersion: session.StateVersion}
	switch {
	case view.Status == "waiting" && view.ApprovalStepID != "":
		command.Kind, command.StepID = "approve", view.ApprovalStepID
	case view.Status == "paused":
		command.Kind = "resume"
	default:
		return nil
	}
	return command
}
