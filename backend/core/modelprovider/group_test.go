package modelprovider

import (
	"reflect"
	"testing"
)

// --- normalizeBaseURLForCompare ---

// TestNormalizeBaseURLForCompare_TrimsTrailingSlashes removes all trailing slashes.
func TestNormalizeBaseURLForCompare_TrimsTrailingSlashes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://api.example.com/", "https://api.example.com"},
		{"https://api.example.com//", "https://api.example.com"},
		{"https://api.example.com", "https://api.example.com"},
		{"https://api.example.com/v1/", "https://api.example.com/v1"},
		{"  https://example.com/  ", "https://example.com"},
		{"", ""},
		{"/", ""},
		{"  ", ""},
	}
	for _, tt := range tests {
		got := normalizeBaseURLForCompare(tt.input)
		if got != tt.want {
			t.Fatalf("normalizeBaseURLForCompare(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShouldSeedSenseNovaModelScopesClassicAndTokenPlan(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		useTokenPlan bool
		want         bool
	}{
		{name: "classic model in classic group", model: "SenseChat-5", want: true},
		{name: "token model excluded from classic", model: "sensenova-6.7-flash-lite", want: false},
		{name: "token model in token group", model: "sensenova-6.7-flash-lite", useTokenPlan: true, want: true},
		{name: "classic model excluded from token", model: "SenseChat-5", useTokenPlan: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSeedSenseNovaModel(tt.model, tt.useTokenPlan); got != tt.want {
				t.Fatalf("shouldSeedSenseNovaModel(%q, %v) = %v, want %v",
					tt.model, tt.useTokenPlan, got, tt.want)
			}
		})
	}
}

// --- splitAPIKeys ---

// TestSplitAPIKeys_NewlineSeparated splits keys by newline.
func TestSplitAPIKeys_NewlineSeparated(t *testing.T) {
	raw := "key1\nkey2\nkey3"
	got := splitAPIKeys(raw)
	want := []string{"key1", "key2", "key3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestSplitAPIKeys_TrimsWhitespace strips leading/trailing whitespace per key.
func TestSplitAPIKeys_TrimsWhitespace(t *testing.T) {
	raw := "  key1  \n  key2  "
	got := splitAPIKeys(raw)
	want := []string{"key1", "key2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestSplitAPIKeys_SkipsEmptyLines ignores empty lines including whitespace-only lines.
func TestSplitAPIKeys_SkipsEmptyLines(t *testing.T) {
	raw := "key1\n\n  \nkey3"
	got := splitAPIKeys(raw)
	want := []string{"key1", "key3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestSplitAPIKeys_EmptyOrWhitespace returns nil.
func TestSplitAPIKeys_EmptyOrWhitespace(t *testing.T) {
	if got := splitAPIKeys(""); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := splitAPIKeys("  \n  "); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestSplitAPIKeys_SingleKey handles one key with no newlines.
func TestSplitAPIKeys_SingleKey(t *testing.T) {
	got := splitAPIKeys("single-key")
	want := []string{"single-key"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
