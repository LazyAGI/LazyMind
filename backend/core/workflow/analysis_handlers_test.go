package workflow

import "testing"

func TestNormalizeWorkflowCandidatesAddsStableMissingIDs(t *testing.T) {
	candidates := []map[string]any{
		{"name": "report_writer"},
		{"id": "custom", "name": "custom candidate"},
		{"id": "custom", "name": "duplicate id"},
	}

	normalized := normalizeWorkflowCandidates(candidates)
	want := []string{"candidate-1", "custom", "candidate-3"}
	for index, candidate := range normalized {
		if got := candidate["id"]; got != want[index] {
			t.Fatalf("candidate %d id = %v, want %q", index, got, want[index])
		}
	}
}
