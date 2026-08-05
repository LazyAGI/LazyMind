package resourceupdate

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gorm.io/gorm"
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
