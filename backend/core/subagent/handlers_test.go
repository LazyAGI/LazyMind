package subagent

import (
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
)

// TestToTaskDTO maps SubAgentTask fields to DTO.
func TestToTaskDTO(t *testing.T) {
	ot := &orm.SubAgentTask{
		ID:                "task-1",
		ConversationID:    "conv-1",
		TriggerHistoryID:  "hist-1",
		SeqInConversation: 1,
		AgentType:         "code-gen",
		Title:             "Refactor",
		Objective:         "Clean up module",
		Mode:              "auto",
		Status:            "running",
		ProgressPct:       50,
		CurrentPhase:      "analyzing",
		EstimatedSec:      120,
		Summary:           "Working on it",
	}
	dto := toTaskDTO(ot)
	if dto.TaskID != "task-1" || dto.ConversationID != "conv-1" {
		t.Fatalf("basic fields: %+v", dto)
	}
	if dto.Title != "Refactor" || dto.Status != "running" {
		t.Fatalf("title/status: %+v", dto)
	}
	if dto.Progress != 50 || dto.EstimatedSec != 120 {
		t.Fatalf("progress/estimated: %+v", dto)
	}
}

// TestToStepDTO maps SubAgentStep fields to DTO.
func TestToStepDTO(t *testing.T) {
	s := &orm.SubAgentStep{
		Seq:     3,
		Role:    "text",
		Content: []byte(`{"key":"value"}`),
	}
	dto := toStepDTO(s)
	if dto.Seq != 3 || dto.Role != "text" {
		t.Fatalf("got seq=%d role=%s", dto.Seq, dto.Role)
	}
	if len(dto.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
}

// TestRequestUserID returns default "0" when no user context is present.
func TestRequestUserID(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	got := requestUserID(req)
	if got != "0" {
		t.Fatalf("got %q, want 0", got)
	}
}
