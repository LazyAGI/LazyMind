package resourcechange

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// TestParsePositiveQueryInt parses and clamps integer values.
func TestParsePositiveQueryInt(t *testing.T) {
	tests := []struct {
		value string
		def   int
		max   int
		want  int
	}{
		{"50", 20, 100, 50},
		{"0", 20, 100, 20},
		{"-5", 20, 100, 20},
		{"", 20, 100, 20},
		{"abc", 20, 100, 20},
		{"150", 20, 100, 100}, // clamped to max
		{"50", 10, 0, 50},     // max=0 means no upper bound
		{"  30  ", 10, 100, 30},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := parsePositiveQueryInt(tt.value, tt.def, tt.max); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// TestVersionToResponse maps all ORM fields to the response DTO.
func TestVersionToResponse(t *testing.T) {
	now := time.Now().UTC()
	row := orm.ResourceVersion{
		ID:            "v1",
		ResourceType:  "memory",
		ResourceID:    "res-1",
		UserID:        "user-1",
		ChangeSource:  ChangeSourceDirectSave,
		FromVersion:   1,
		ToVersion:     2,
		SourceRefType: SourceRefTypeSkillReviewResult,
		SourceRefID:   "ref-1",
		BeforeContent: "before",
		AfterContent:  "after",
		Diff:          "+after",
		CreatedAt:     now,
	}
	resp := versionToResponse(row)
	if resp.ID != "v1" || resp.ResourceType != "memory" {
		t.Fatalf("basic fields: %+v", resp)
	}
	if resp.ChangeSource != ChangeSourceDirectSave {
		t.Fatalf("ChangeSource: got %q", resp.ChangeSource)
	}
	if resp.FromVersion != 1 || resp.ToVersion != 2 {
		t.Fatalf("version numbers: %+v", resp)
	}
	if resp.BeforeContent != "before" || resp.AfterContent != "after" {
		t.Fatalf("content mismatch: %+v", resp)
	}
}

// TestCompactStrings deduplicates and trims strings.
func TestCompactStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"no_dups", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with_dups", []string{"a", "b", "a", "c"}, []string{"a", "b", "c"}},
		{"with_empty", []string{"", "a", "  ", "b", ""}, []string{"a", "b"}},
		{"all_empty", []string{"", "  "}, []string{}},
		{"nil", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compactStrings(tt.input)
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
