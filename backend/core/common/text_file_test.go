package common

import (
	"reflect"
	"sort"
	"testing"
)

// TestIsTextFileExtension covers known text extensions (positive), unknown binary
// extensions (negative), and edge cases like dots, casing, and whitespace.
func TestIsTextFileExtension(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		// Known text extensions.
		{"txt", true},
		{"md", true},
		{"lmd", true},
		{"json", true},
		{"yaml", true},
		{"go", true},
		{"py", true},
		{"js", true},
		{"ts", true},
		{"java", true},
		{"cpp", true},
		{"html", true},
		{"css", true},
		{"csv", true},
		{"sql", true},
		{"sh", true},
		{"rs", true},
		// Leading dot and case variants.
		{".txt", true},
		{".go", true},
		{"TXT", true},
		{"Go", true},
		{"  json  ", true},
		// Unknown / binary extensions.
		{"exe", false},
		{"bin", false},
		{"zip", false},
		{"png", false},
		{"jpg", false},
		{"pdf", false},
		// Edge cases.
		{"", false},
		{".", false},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := IsTextFileExtension(tt.ext)
			if got != tt.want {
				t.Fatalf("IsTextFileExtension(%q) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}

func TestTextFileExtensionsReturnsSortedCopy(t *testing.T) {
	first := TextFileExtensions()
	if len(first) == 0 || !sort.StringsAreSorted(first) {
		t.Fatalf("extensions=%v", first)
	}
	original := append([]string(nil), first...)
	first[0] = "mutated"
	if got := TextFileExtensions(); !reflect.DeepEqual(got, original) {
		t.Fatalf("shared extensions mutated: %v", got)
	}
}
