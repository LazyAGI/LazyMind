package subagent

import (
	"testing"
)

// TestTaskStreamKey formats the Redis stream key.
func TestTaskStreamKey(t *testing.T) {
	got := taskStreamKey("task-123")
	want := "rag/subagent/stream:task-123"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTaskStatusKey formats the Redis status key.
func TestTaskStatusKey(t *testing.T) {
	got := taskStatusKey("task-456")
	want := "rag/subagent/status:task-456"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
