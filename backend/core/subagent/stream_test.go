package subagent

import (
	"testing"

	"lazymind/core/common/orm"
)

// TestIsTerminal identifies terminal task statuses.
func TestIsTerminal(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{StatusSucceeded, true},
		{StatusFailed, true},
		{StatusInterrupted, true},
		{StatusCanceled, true},
		{StatusRunning, false},
		{StatusPending, false},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isTerminal(tt.status); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestArtifactDedupKey builds slot#seq key for deduplication.
func TestArtifactDedupKey(t *testing.T) {
	a := &orm.SubAgentArtifact{Slot: "result", Seq: 5}
	got := artifactDedupKey(a)
	want := "result#5"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestIsWriterDraftStreamTaskUsesWorkflowIdentity(t *testing.T) {
	task := &orm.SubAgentTask{
		AgentType: "workflow_step",
		Params:    []byte(`{"workflow_id":"writer-workflow","step_id":"write_document"}`),
	}
	if !isWriterDraftStreamTask(task) {
		t.Fatal("expected Writer workflow write_document task to enable stream heartbeats")
	}

	task.Params = []byte(`{"workflow_id":"writer-workflow","step_id":"outline"}`)
	if isWriterDraftStreamTask(task) {
		t.Fatal("outline step must not enable Draft stream heartbeats")
	}

	task.AgentType = "plugin_step"
	task.Params = []byte(`{"plugin_id":"writer-plugin","step_id":"write_document"}`)
	if isWriterDraftStreamTask(task) {
		t.Fatal("legacy plugin identity must not match the Workflow runtime")
	}
}

// TestStepToTaskEvent maps SubAgentStep to TaskEvent by role.
func TestStepToTaskEvent(t *testing.T) {
	// Text role step becomes a text event with content.
	s := &orm.SubAgentStep{Seq: 1, Role: "text", Content: []byte(`{"content":"hello"}`)}
	ev := stepToTaskEvent("task-1", s)
	if ev == nil {
		t.Fatal("expected non-nil event for text step")
	}
	if ev.TaskID != "task-1" {
		t.Fatalf("TaskID: got %q, want task-1", ev.TaskID)
	}
	if ev.Type != "text" {
		t.Fatalf("Type: got %q, want text", ev.Type)
	}

	// Think role step becomes a think event.
	s2 := &orm.SubAgentStep{Seq: 2, Role: "think", Content: []byte(`{"content":"reasoning..."}`)}
	ev2 := stepToTaskEvent("task-2", s2)
	if ev2 == nil {
		t.Fatal("expected non-nil event for think step")
	}
	if ev2.Type != "think" {
		t.Fatalf("Type: got %q, want think", ev2.Type)
	}

	// Empty content text step returns nil.
	s3 := &orm.SubAgentStep{Seq: 3, Role: "text", Content: []byte(`{"content":""}`)}
	if ev3 := stepToTaskEvent("task-3", s3); ev3 != nil {
		t.Fatalf("empty content: got %v, want nil", ev3)
	}

	// Unknown role returns nil.
	s4 := &orm.SubAgentStep{Seq: 4, Role: "unknown", Content: []byte(`{}`)}
	if ev4 := stepToTaskEvent("task-4", s4); ev4 != nil {
		t.Fatalf("unknown role: got %v, want nil", ev4)
	}

	// Assistant role with tool_calls.
	s5 := &orm.SubAgentStep{Seq: 5, Role: "assistant", Content: []byte(`{"tool_calls":[{"name":"read_file"}]}`)}
	ev5 := stepToTaskEvent("task-5", s5)
	if ev5 == nil {
		t.Fatal("expected non-nil event for assistant step")
	}
	if ev5.Type != "tool_calls" {
		t.Fatalf("Type: got %q, want tool_calls", ev5.Type)
	}

	// Tool role with tool_results.
	s6 := &orm.SubAgentStep{Seq: 6, Role: "tool", Content: []byte(`{"tool_results":[{"output":"result"}]}`)}
	ev6 := stepToTaskEvent("task-6", s6)
	if ev6 == nil {
		t.Fatal("expected non-nil event for tool step")
	}
	if ev6.Type != "tool_results" {
		t.Fatalf("Type: got %q, want tool_results", ev6.Type)
	}
}
