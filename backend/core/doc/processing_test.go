package doc

import "testing"

func TestResolveReparseStrategyRespectsProcessingLevel(t *testing.T) {
	tests := []struct {
		level, mode, want string
		wantErr           bool
	}{
		{ProcessingLevelStored, "slice_missing", "rebuild", false},
		{ProcessingLevelParsed, "slice_missing", "rebuild", false},
		{ProcessingLevelChunked, "slice_missing", "slice_missing", false},
		{ProcessingLevelChunked, "slice_and_embed", "", true},
		{ProcessingLevelIndexed, "slice_and_embed", "reembed", false},
	}
	for _, tt := range tests {
		got, err := resolveReparseStrategy(tt.level, tt.mode)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Fatalf("resolveReparseStrategy(%q, %q) = %q, %v; want %q, error=%v", tt.level, tt.mode, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestNormalizeProcessingLevelDefaultsToIndexed(t *testing.T) {
	got, err := normalizeProcessingLevel("")
	if err != nil || got != ProcessingLevelIndexed {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestCapabilitiesForProcessingLevel(t *testing.T) {
	tests := []struct {
		level            string
		search, retrieve bool
	}{
		{ProcessingLevelStored, false, false},
		{ProcessingLevelParsed, false, false},
		{ProcessingLevelChunked, true, false},
		{ProcessingLevelIndexed, true, true},
	}
	for _, tt := range tests {
		got := capabilitiesForProcessingLevel(tt.level)
		if !got.List || !got.Read || got.Search != tt.search || got.Retrieve != tt.retrieve {
			t.Errorf("%s capabilities = %#v", tt.level, got)
		}
	}
}

func TestNormalizeProcessingLevelRejectsUnknown(t *testing.T) {
	if _, err := normalizeProcessingLevel("vectorized"); err == nil {
		t.Fatal("expected validation error")
	}
}
