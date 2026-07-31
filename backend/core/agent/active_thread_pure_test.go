package agent

import (
	"testing"
)

// TestIsTerminalUserActiveThreadStatus identifies terminal states.
func TestIsTerminalUserActiveThreadStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"finished", true},
		{"FINISHED", true},
		{"failed", true},
		{"Failed", true},
		{"cancelled", true},
		{"CANCELLED", true},
		{"running", false},
		{"pending", false},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isTerminalUserActiveThreadStatus(tt.status); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsThreadFlowRunning detects running/pending/paused or active task IDs.
func TestIsThreadFlowRunning(t *testing.T) {
	if isThreadFlowRunning(nil) {
		t.Fatal("nil flowStatus should not be running")
	}
	tests := []struct {
		name   string
		status string
		tasks  []string
		want   bool
	}{
		{"running", "running", nil, true},
		{"pending", "pending", nil, true},
		{"paused", "paused", nil, true},
		{"finished_no_tasks", "finished", nil, false},
		{"finished_with_tasks", "finished", []string{"t1"}, true},
		{"empty", "", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &threadFlowStatusResponse{Status: tt.status, ActiveTaskIDs: tt.tasks}
			if got := isThreadFlowRunning(fs); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
