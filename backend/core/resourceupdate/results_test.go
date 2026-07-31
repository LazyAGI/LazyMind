package resourceupdate

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// TestParseSkillFrontmatter_OK parses valid frontmatter with name, description and body.
func TestParseSkillFrontmatter_OK(t *testing.T) {
	content := "---\nname: MySkill\ndescription: A test skill\ncategory: tools\n---\n\nThis is the body.\n"
	meta, err := parseSkillFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Name != "MySkill" {
		t.Fatalf("name = %q, want MySkill", meta.Name)
	}
	if meta.Description != "A test skill" {
		t.Fatalf("description = %q", meta.Description)
	}
	if meta.Category != "tools" {
		t.Fatalf("category = %q, want tools", meta.Category)
	}
}

// TestParseSkillFrontmatter_MissingFrontmatter returns error when no YAML frontmatter.
func TestParseSkillFrontmatter_MissingFrontmatter(t *testing.T) {
	_, err := parseSkillFrontmatter("no frontmatter here")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
	if !errors.Is(err, errReviewInvalid) {
		t.Fatalf("error = %v, want errReviewInvalid", err)
	}
}

// TestParseSkillFrontmatter_MissingClosingSeparator returns error.
func TestParseSkillFrontmatter_MissingClosingSeparator(t *testing.T) {
	content := "---\nname: MySkill\n"
	_, err := parseSkillFrontmatter(content)
	if err == nil {
		t.Fatal("expected error for missing closing separator")
	}
}

// TestParseSkillFrontmatter_MissingBody returns error.
func TestParseSkillFrontmatter_MissingBody(t *testing.T) {
	content := "---\nname: MySkill\n---\n"
	_, err := parseSkillFrontmatter(content)
	if err == nil {
		t.Fatal("expected error for missing body")
	}
}

// TestParseSkillFrontmatter_MissingName returns error.
func TestParseSkillFrontmatter_MissingName(t *testing.T) {
	content := "---\ndescription: A skill\n---\n\nbody\n"
	_, err := parseSkillFrontmatter(content)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

// TestParseSkillFrontmatter_InvalidYAML returns error.
func TestParseSkillFrontmatter_InvalidYAML(t *testing.T) {
	content := "---\n[invalid yaml\n---\n\nbody\n"
	_, err := parseSkillFrontmatter(content)
	if err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

// TestNormalizeReviewTarget maps known resource types correctly.
func TestNormalizeReviewTarget(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{orm.ResourceUpdateResourceTypeMemory, orm.ResourceUpdateResourceTypeMemory},
		{orm.ResourceUpdateResourceTypeUserPreference, orm.ResourceUpdateResourceTypeUserPreference},
		{"  memory  ", orm.ResourceUpdateResourceTypeMemory},
		{"  user_preference  ", orm.ResourceUpdateResourceTypeUserPreference},
		{"unknown_type", "unknown_type"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeReviewTarget(tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsAutoApplyActiveStatus checks statuses that allow auto-apply.
func TestIsAutoApplyActiveStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{orm.ResourceUpdateTaskStatusPending, true},
		{orm.ResourceUpdateTaskStatusRunning, true},
		{orm.ResourceUpdateTaskStatusDone, false},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isAutoApplyActiveStatus(tt.status); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNullableString returns nil for empty/whitespace and pointer for non-empty.
func TestNullableString(t *testing.T) {
	tests := []struct {
		input string
		isNil bool
	}{
		{"hello", false},
		{"", true},
		{"  ", true},
		{"  value  ", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := nullableString(tt.input)
			if tt.isNil && got != nil {
				t.Fatalf("got %q, want nil", *got)
			}
			if !tt.isNil && got == nil {
				t.Fatal("got nil, want non-nil")
			}
			if got != nil && *got != "value" && tt.input == "  value  " {
				t.Fatalf("got %q, want \"value\"", *got)
			}
		})
	}
}

// TestPersonalResourcePath returns correct path for each resource type.
func TestPersonalResourcePath(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{orm.ResourceUpdateResourceTypeMemory, "memory/memory.md"},
		{orm.ResourceUpdateResourceTypeUserPreference, "memory/user.md"},
		{"unknown", "memory/memory.md"},
		{"", "memory/memory.md"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			if got := personalResourcePath(tt.target); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTaskReviewResultID uses ReviewResultID when available.
func TestTaskReviewResultID(t *testing.T) {
	// Prefer ReviewResultID
	task := orm.ResourceUpdateTask{ReviewResultID: "review-1", TriggerID: "trigger-1"}
	if got := taskReviewResultID(task); got != "review-1" {
		t.Fatalf("got %q, want review-1", got)
	}

	// Fallback to TriggerID
	task2 := orm.ResourceUpdateTask{ReviewResultID: "", TriggerID: "trigger-2"}
	if got := taskReviewResultID(task2); got != "trigger-2" {
		t.Fatalf("got %q, want trigger-2", got)
	}

	// Both empty/whitespace
	task3 := orm.ResourceUpdateTask{ReviewResultID: "  ", TriggerID: "  "}
	if got := taskReviewResultID(task3); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestMapReviewError maps known error types to HTTP status codes.
func TestMapReviewError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not_found", errReviewNotFound, http.StatusNotFound},
		{"gorm_not_found", gorm.ErrRecordNotFound, http.StatusNotFound},
		{"conflict", errReviewConflict, http.StatusConflict},
		{"duplicate_key", gorm.ErrDuplicatedKey, http.StatusConflict},
		{"invalid", errReviewInvalid, http.StatusBadRequest},
		{"generic", errors.New("generic error"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mapReviewError(rec, tt.err, "fallback")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

// TestParsePositiveQueryInt_Results mirrors parsePositiveQueryInt in results.go.
func TestParsePositiveQueryInt_Results(t *testing.T) {
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
		{"150", 20, 100, 100},
		{"50", 10, 0, 50},
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

// TestValidateSkillReviewContent_OK validates matching name and valid content.
func TestValidateSkillReviewContent_OK(t *testing.T) {
	content := "---\nname: MySkill\ndescription: desc\n---\n\nbody\n"
	meta, err := validateSkillReviewContent("MySkill", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Name != "MySkill" {
		t.Fatalf("name = %q", meta.Name)
	}
}

// TestValidateSkillReviewContent_NameMismatch returns error when names differ.
func TestValidateSkillReviewContent_NameMismatch(t *testing.T) {
	content := "---\nname: OtherSkill\ndescription: desc\n---\n\nbody\n"
	_, err := validateSkillReviewContent("MySkill", content)
	if err == nil {
		t.Fatal("expected error for name mismatch")
	}
}

// TestValidateSkillReviewContent_EmptyName returns error.
func TestValidateSkillReviewContent_EmptyName(t *testing.T) {
	_, err := validateSkillReviewContent("", "content")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

// TestValidateSkillReviewContent_EmptyContent returns error.
func TestValidateSkillReviewContent_EmptyContent(t *testing.T) {
	_, err := validateSkillReviewContent("MySkill", "")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

// TestValidatePathSegment_OK accepts valid path segments.
func TestValidatePathSegment_OK(t *testing.T) {
	for _, seg := range []string{"tools", "my-skill", "category_1", "abc123"} {
		if err := validatePathSegment(seg); err != nil {
			t.Fatalf("%q should be valid: %v", seg, err)
		}
	}
}

// TestValidatePathSegment_RejectsInvalidSegments returns error for empty, dot, or slash.
func TestValidatePathSegment_RejectsInvalidSegments(t *testing.T) {
	for _, seg := range []string{"", ".", "..", "a/b", "a\\b"} {
		if err := validatePathSegment(seg); err == nil {
			t.Fatalf("%q should be invalid", seg)
		}
	}
}
