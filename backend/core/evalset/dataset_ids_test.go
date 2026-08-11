package evalset

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
)

// TestNormalizeDatasetIDs deduplicates and removes empty/whitespace IDs.
func TestNormalizeDatasetIDs(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"no_dups", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with_dups", []string{"a", "b", "a"}, []string{"a", "b"}},
		{"with_empty", []string{"", "a", "  ", "b"}, []string{"a", "b"}},
		{"nil", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeDatasetIDs(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len: got %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestDatasetIDsJSON marshals normalized IDs or returns empty array on error.
func TestDatasetIDsJSON(t *testing.T) {
	// Valid IDs
	raw := datasetIDsJSON([]string{"a", "b", "  a  "})
	if string(raw) != `["a","b"]` {
		t.Fatalf("got %s", string(raw))
	}

	// Empty input
	raw2 := datasetIDsJSON(nil)
	if string(raw2) != `[]` {
		t.Fatalf("got %s", string(raw2))
	}
}

// TestParseDatasetIDsJSON parses JSON array of IDs with normalization.
func TestParseDatasetIDsJSON(t *testing.T) {
	// Valid JSON
	ids := parseDatasetIDsJSON(json.RawMessage(`["a","b","a",""]`))
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("got %v", ids)
	}

	// Nil/empty
	if got := parseDatasetIDsJSON(nil); got != nil {
		t.Fatalf("nil got %v, want nil", got)
	}
	if got := parseDatasetIDsJSON(json.RawMessage{}); got != nil {
		t.Fatalf("empty got %v, want nil", got)
	}

	// Invalid JSON
	if got := parseDatasetIDsJSON(json.RawMessage("not json")); got != nil {
		t.Fatalf("invalid got %v, want nil", got)
	}
}

// TestDatasetNamesForIDs maps IDs to names, falling back to ID when name is empty.
func TestDatasetNamesForIDs(t *testing.T) {
	names := map[string]string{"a": "Alpha", "b": ""}
	got := datasetNamesForIDs([]string{"a", "b", "c"}, names)
	want := []string{"Alpha", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCollectEvalSetDatasetIDs collects unique dataset IDs across rows.
func TestCollectEvalSetDatasetIDs(t *testing.T) {
	rows := []orm.EvalSet{
		{DatasetIDs: json.RawMessage(`["a","b"]`)},
		{DatasetIDs: json.RawMessage(`["b","c"]`)},
		{DatasetIDs: json.RawMessage(`[]`)},
	}
	got := collectEvalSetDatasetIDs(rows)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	// Nil rows returns empty slice
	if got := collectEvalSetDatasetIDs(nil); len(got) != 0 {
		t.Fatalf("nil got %v, want empty", got)
	}

	// Empty rows returns empty slice
	if got := collectEvalSetDatasetIDs([]orm.EvalSet{}); len(got) != 0 {
		t.Fatalf("empty got %v, want empty", got)
	}
}

// TestFilterRowsByDatasetIDs filters rows that contain any matching dataset ID.
func TestFilterRowsByDatasetIDs(t *testing.T) {
	rows := []orm.EvalSet{
		{ID: "1", DatasetIDs: json.RawMessage(`["a","b"]`)},
		{ID: "2", DatasetIDs: json.RawMessage(`["c"]`)},
		{ID: "3", DatasetIDs: json.RawMessage(`[]`)},
	}
	filtered := filterRowsByDatasetIDs(rows, []string{"b"})
	if len(filtered) != 1 || filtered[0].ID != "1" {
		t.Fatalf("got %v", filtered)
	}

	// Empty filter returns all rows
	if got := filterRowsByDatasetIDs(rows, nil); len(got) != 3 {
		t.Fatalf("nil filter got %d rows, want 3", len(got))
	}
}
