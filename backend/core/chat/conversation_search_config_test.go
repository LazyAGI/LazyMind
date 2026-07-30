package chat

import (
	"reflect"
	"testing"
)

// TestUniqueNonEmptyStrings_NoDuplicates passes through unique strings unchanged.
func TestUniqueNonEmptyStrings_NoDuplicates(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"a", "b", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestUniqueNonEmptyStrings_Dedup removes duplicate entries, preserving first occurrence order.
func TestUniqueNonEmptyStrings_Dedup(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestUniqueNonEmptyStrings_EmptyAndWhitespace filters out empty and whitespace-only values.
func TestUniqueNonEmptyStrings_EmptyAndWhitespace(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"", "  ", "x", "\t", "y"})
	want := []string{"x", "y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestUniqueNonEmptyStrings_AllEmpty returns empty slice for all-empty input.
func TestUniqueNonEmptyStrings_AllEmpty(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"", "  "})
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestUniqueNonEmptyStrings_NilInput returns empty slice.
func TestUniqueNonEmptyStrings_NilInput(t *testing.T) {
	got := uniqueNonEmptyStrings(nil)
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestUniqueNonEmptyStrings_SingleElement handles a single non-empty value.
func TestUniqueNonEmptyStrings_SingleElement(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"only"})
	want := []string{"only"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
