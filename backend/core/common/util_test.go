package common

import (
	"strings"
	"testing"
)

// TestGenerateID_Length ensures the generated ID is exactly 32 hex characters.
func TestGenerateID_Length(t *testing.T) {
	id := GenerateID()
	if len(id) != 32 {
		t.Fatalf("GenerateID length: got %d, want 32", len(id))
	}
}

// TestGenerateID_OnlyHex ensures the generated ID contains only hexadecimal characters.
func TestGenerateID_OnlyHex(t *testing.T) {
	id := GenerateID()
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("GenerateID contains non-hex character %q in %q", c, id)
		}
	}
}

// TestGenerateID_NotIdentical verifies that two successive calls produce different IDs.
func TestGenerateID_NotIdentical(t *testing.T) {
	a := GenerateID()
	b := GenerateID()
	if a == b {
		t.Fatal("two successive GenerateID calls produced the same value")
	}
}

// TestGeneratePrefixedID_Happy verifies a typical prefixed ID generation.
func TestGeneratePrefixedID_Happy(t *testing.T) {
	id := GeneratePrefixedID("codex-", 40)
	if !strings.HasPrefix(id, "codex-") {
		t.Fatalf("GeneratePrefixedID missing prefix: %q", id)
	}
	if len(id) > 40 {
		t.Fatalf("GeneratePrefixedID too long: %d > 40", len(id))
	}
	suffix := strings.TrimPrefix(id, "codex-")
	if len(suffix) == 0 {
		t.Fatal("GeneratePrefixedID has empty suffix")
	}
	if len(suffix) > 32 {
		t.Fatalf("suffix longer than UUID: %d > 32", len(suffix))
	}
}

// TestGeneratePrefixedID_ShortMaxLen verifies truncation with a short maxLen.
func TestGeneratePrefixedID_ShortMaxLen(t *testing.T) {
	id := GeneratePrefixedID("p_", 8)
	if !strings.HasPrefix(id, "p_") {
		t.Fatalf("GeneratePrefixedID missing prefix: %q", id)
	}
	if len(id) != 8 {
		t.Fatalf("GeneratePrefixedID length: got %d, want 8", len(id))
	}
}

// TestGeneratePrefixedID_ExactMaxLen verifies the result when maxLen equals prefix length plus UUID length.
func TestGeneratePrefixedID_ExactMaxLen(t *testing.T) {
	id := GeneratePrefixedID("ab", 34)
	if !strings.HasPrefix(id, "ab") {
		t.Fatalf("GeneratePrefixedID missing prefix: %q", id)
	}
	if len(id) != 34 {
		t.Fatalf("GeneratePrefixedID length: got %d, want 34", len(id))
	}
}

// TestGeneratePrefixedID_PanicsOnTooSmallMaxLen ensures the function panics when maxLen < len(prefix).
func TestGeneratePrefixedID_PanicsOnTooSmallMaxLen(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when maxLen <= len(prefix)")
		}
	}()
	GeneratePrefixedID("long-prefix", 5)
}

// TestGeneratePrefixedID_PanicsOnEqualMaxLen ensures the function panics when maxLen == len(prefix).
func TestGeneratePrefixedID_PanicsOnEqualMaxLen(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when maxLen == len(prefix)")
		}
	}()
	GeneratePrefixedID("abc", 3)
}
