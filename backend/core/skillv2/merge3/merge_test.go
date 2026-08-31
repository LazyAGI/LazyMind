package merge3

import (
	"strings"
	"testing"
)

func TestMergeTextMergesNonOverlappingChanges(t *testing.T) {
	base := []byte("one\ntwo\nthree\nfour\n")
	ours := []byte("ONE\ntwo\nthree\nfour\n")
	theirs := []byte("one\ntwo\nthree\nFOUR\n")
	merged, conflict := MergeText(base, ours, theirs)
	if conflict {
		t.Fatal("non-overlapping changes conflicted")
	}
	if got, want := string(merged), "ONE\ntwo\nthree\nFOUR\n"; got != want {
		t.Fatalf("merged = %q, want %q", got, want)
	}
}

func TestMergeTextUsesPlatformContentForConflictWithoutMarkers(t *testing.T) {
	base := []byte("before\nkeep\nanswer after each question\nkeep too\nafter\n")
	ours := []byte("BEFORE\nkeep\nanswers at document end\nkeep too\nafter\n")
	theirs := []byte("before\nkeep\nanswers at chapter end\nkeep too\nAFTER\n")
	merged, conflict := MergeText(base, ours, theirs)
	if !conflict {
		t.Fatal("overlapping change did not conflict")
	}
	got := string(merged)
	if strings.Contains(got, "<<<<<<<") || got != "BEFORE\nkeep\nanswers at chapter end\nkeep too\nAFTER\n" {
		t.Fatalf("merged = %q", got)
	}
}

func TestMergeTextPreservesMissingFinalNewline(t *testing.T) {
	merged, conflict := MergeText([]byte("a\nb"), []byte("A\nb"), []byte("a\nB"))
	if conflict || string(merged) != "A\nB" {
		t.Fatalf("merged=%q conflict=%v", merged, conflict)
	}
}

func TestMergeTextHandlesInsertions(t *testing.T) {
	t.Run("different locations", func(t *testing.T) {
		merged, conflict := MergeText([]byte("a\nb\n"), []byte("user\na\nb\n"), []byte("a\nb\nplatform\n"))
		if conflict || string(merged) != "user\na\nb\nplatform\n" {
			t.Fatalf("merged=%q conflict=%v", merged, conflict)
		}
	})
	t.Run("same location", func(t *testing.T) {
		merged, conflict := MergeText([]byte("a\nb\n"), []byte("a\nuser\nb\n"), []byte("a\nplatform\nb\n"))
		if !conflict || string(merged) != "a\nplatform\nb\n" {
			t.Fatalf("merged=%q conflict=%v", merged, conflict)
		}
	})
}

func TestMergeTreesHandlesFileLevelConflicts(t *testing.T) {
	base := map[string]File{
		"delete.md":  {Data: []byte("base")},
		"binary.png": {Data: []byte{1}, Binary: true},
	}
	ours := map[string]File{
		"delete.md":  {Data: []byte("user")},
		"binary.png": {Data: []byte{2}, Binary: true},
		"new.md":     {Data: []byte("user")},
	}
	theirs := map[string]File{
		"binary.png": {Data: []byte{3}, Binary: true},
		"new.md":     {Data: []byte("platform")},
	}
	result := MergeTrees(base, ours, theirs)
	if _, exists := result.Files["delete.md"]; exists {
		t.Fatal("platform deletion was not selected for conflict candidate")
	}
	if got := string(result.Files["new.md"].Data); got != "platform" {
		t.Fatalf("new.md = %q", got)
	}
	if len(result.Conflicts) != 3 {
		t.Fatalf("conflicts = %#v", result.Conflicts)
	}
}

func TestMergeTreesTakesOnlyChangedSide(t *testing.T) {
	base := map[string]File{"a.md": {Data: []byte("base")}, "b.md": {Data: []byte("base")}}
	ours := map[string]File{"a.md": {Data: []byte("user")}, "b.md": {Data: []byte("base")}}
	theirs := map[string]File{"a.md": {Data: []byte("base")}, "b.md": {Data: []byte("platform")}}
	result := MergeTrees(base, ours, theirs)
	if len(result.Conflicts) != 0 || string(result.Files["a.md"].Data) != "user" || string(result.Files["b.md"].Data) != "platform" {
		t.Fatalf("result = %#v", result)
	}
}
