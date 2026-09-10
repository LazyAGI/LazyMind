package doc

import "testing"

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
