package currentmemory

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

func TestPreferenceProjectionMatchesSharedGoldenFixtures(t *testing.T) {
	for _, variant := range []string{"", "edge-"} {
		t.Run(variant+"projection", func(t *testing.T) { testSharedProjection(t, variant) })
	}
}

func TestPreferenceProjectionCharacterBoundaries(t *testing.T) {
	document := PreferenceDocument{}
	for i := 0; i < 101; i++ {
		document.Preferences = append(document.Preferences, PreferenceItem{Summary: "中😀", Ref: "references/a.md"})
	}
	full := BuildPreferenceProjectionState(document, 100000)
	exact := BuildPreferenceProjectionState(document, full.FullProjectionChars)
	if exact.ProjectedItems != 101 || exact.ProjectionTruncated || exact.ProjectedChars != exact.MaxChars {
		t.Fatalf("exact budget or old item limit: %#v", exact)
	}
	below := BuildPreferenceProjectionState(document, full.FullProjectionChars-1)
	if below.ProjectedItems != 100 || !below.ProjectionTruncated {
		t.Fatalf("must retain whole prefix: %#v", below)
	}
	for _, doc := range []PreferenceDocument{{}, document} {
		for _, budget := range []int{1, 14, 15, 16} {
			state := BuildPreferenceProjectionState(doc, budget)
			if state.ProjectedChars > budget || state.ProjectedItems != 0 {
				t.Fatalf("tiny budget: %#v", state)
			}
		}
	}
}

func TestPreferenceOrganizationTriggerUsesOnlyCharacters(t *testing.T) {
	for _, test := range []struct {
		chars int
		want  bool
	}{{3999, false}, {4000, true}, {5000, true}, {6000, true}} {
		state := PreferenceProjectionState{MaxChars: 5000, FullProjectionChars: test.chars, StoredItems: 150}
		if state.NeedsOrganization() != test.want {
			t.Fatalf("trigger at %d characters = %v", test.chars, state.NeedsOrganization())
		}
	}
}

func testSharedProjection(t *testing.T, variant string) {
	fixtureRoot := filepath.Join("..", "..", "..", "tests", "fixtures", "preference_projection")
	full, err := os.ReadFile(filepath.Join(fixtureRoot, variant+"full.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join(fixtureRoot, variant+"compact.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	expectedFirstTwo, err := os.ReadFile(filepath.Join(fixtureRoot, variant+"compact-first-two.yaml"))
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

	complete := BuildPreferenceProjectionState(document, 5000)
	if complete.FullProjectionChars != utf8.RuneCount(expected) ||
		complete.ProjectedChars != utf8.RuneCount(expected) ||
		complete.ProjectedItems != len(document.Preferences) || complete.ProjectionTruncated {
		t.Fatalf("unexpected complete projection state: %#v", complete)
	}

	truncated := BuildPreferenceProjectionState(document, utf8.RuneCount(expectedFirstTwo))
	if got := renderPreferencePromptItems(all[:2]); got != string(expectedFirstTwo) {
		t.Fatalf("truncated projection mismatch\ngot:\n%s\nwant:\n%s", got, expectedFirstTwo)
	}
	if truncated.ProjectedChars != utf8.RuneCount(expectedFirstTwo) ||
		truncated.ProjectedItems != 2 || !truncated.ProjectionTruncated {
		t.Fatalf("unexpected truncated projection state: %#v", truncated)
	}
}
