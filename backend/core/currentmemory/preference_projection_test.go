package currentmemory

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

func TestPreferenceProjectionMatchesSharedGoldenFixtures(t *testing.T) {
	fixtureRoot := filepath.Join("..", "..", "..", "tests", "fixtures", "preference_projection")
	full, err := os.ReadFile(filepath.Join(fixtureRoot, "full.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join(fixtureRoot, "compact.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	expectedFirstTwo, err := os.ReadFile(filepath.Join(fixtureRoot, "compact-first-two.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := ParsePreferences(full)
	if err != nil {
		t.Fatal(err)
	}

	all := make([]preferencePromptItem, 0, len(document.Preferences))
	for _, item := range document.Preferences {
		all = append(all, preferencePromptItem{Summary: item.Summary, Ref: item.Ref})
	}
	if got := renderPreferencePromptItems(all); got != string(expected) {
		t.Fatalf("full projection mismatch\ngot:\n%s\nwant:\n%s", got, expected)
	}

	complete := BuildPreferenceProjectionState(document, 100, 5000)
	if complete.FullProjectionChars != utf8.RuneCount(expected) ||
		complete.ProjectedChars != utf8.RuneCount(expected) ||
		complete.ProjectedItems != 3 || complete.ProjectionTruncated {
		t.Fatalf("unexpected complete projection state: %#v", complete)
	}

	truncated := BuildPreferenceProjectionState(document, 2, 5000)
	if got := renderPreferencePromptItems(all[:2]); got != string(expectedFirstTwo) {
		t.Fatalf("truncated projection mismatch\ngot:\n%s\nwant:\n%s", got, expectedFirstTwo)
	}
	if truncated.ProjectedChars != utf8.RuneCount(expectedFirstTwo) ||
		truncated.ProjectedItems != 2 || !truncated.ProjectionTruncated {
		t.Fatalf("unexpected truncated projection state: %#v", truncated)
	}
}
