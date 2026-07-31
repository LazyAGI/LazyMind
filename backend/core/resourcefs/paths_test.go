package resourcefs

import (
	"testing"
)

// TestFixedPath_Memory returns the memory path.
func TestFixedPath_Memory(t *testing.T) {
	got, err := FixedPath(ResourceTypeMemory)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != MemoryPath {
		t.Fatalf("got %q, want %q", got, MemoryPath)
	}
}

// TestFixedPath_UserPreference returns the user preference path.
func TestFixedPath_UserPreference(t *testing.T) {
	got, err := FixedPath(ResourceTypeUserPreference)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != UserPreferencePath {
		t.Fatalf("got %q, want %q", got, UserPreferencePath)
	}
}

// TestFixedPath_Invalid returns error for unknown resource type.
func TestFixedPath_Invalid(t *testing.T) {
	_, err := FixedPath("unknown")
	if err == nil {
		t.Fatal("expected error for unknown resource type")
	}
}

// TestNormalizePath strips leading slash and trims whitespace.
func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/memory/memory.md", "memory/memory.md"},
		{"  /memory/user.md  ", "memory/user.md"},
		{"no/leading/slash", "no/leading/slash"},
		{"", ""},
		{"  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := NormalizePath(tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResourceTypeForPath_Memory returns Memory type.
func TestResourceTypeForPath_Memory(t *testing.T) {
	got, err := ResourceTypeForPath(MemoryPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ResourceTypeMemory {
		t.Fatalf("got %q, want %q", got, ResourceTypeMemory)
	}
}

// TestResourceTypeForPath_UserPreference returns UserPreference type.
func TestResourceTypeForPath_UserPreference(t *testing.T) {
	got, err := ResourceTypeForPath(UserPreferencePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ResourceTypeUserPreference {
		t.Fatalf("got %q, want %q", got, ResourceTypeUserPreference)
	}
}

// TestResourceTypeForPath_Unknown returns error.
func TestResourceTypeForPath_Unknown(t *testing.T) {
	_, err := ResourceTypeForPath("random/path.txt")
	if err == nil {
		t.Fatal("expected error for unknown path")
	}
}

// TestIsPersonalResourcePath detects memory paths.
func TestIsPersonalResourcePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{MemoryPath, true},
		{"/" + MemoryPath, true},
		{UserPreferencePath, true},
		{"random/file.txt", false},
		{"memory", true}, // NormalizePath("memory") == "memory" matches fallback
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsPersonalResourcePath(tt.path); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
