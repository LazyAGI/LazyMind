package filediff

import "testing"

func TestCompareContentSplitsSeparatedChangeBlocksIntoHunks(t *testing.T) {
	oldText := "test123\nfirst\n\n\nsecond\n\n\n\nmiddle\n\n\nthird\n\n\ntail\n"
	newText := "test123\nfirst changed\n\n\nsecond changed\n\n\n\nmiddle\n\n\nthird changed\n\n\ntail\n"

	diff, err := CompareContent(
		Content{Path: "567", Data: []byte(oldText), EditableText: true, Size: int64(len(oldText))},
		Content{Path: "567", Data: []byte(newText), EditableText: true, Size: int64(len(newText))},
		Options{ContextLines: 3},
	)
	if err != nil {
		t.Fatalf("CompareContent returned error: %v", err)
	}
	if diff.HunkCount != 3 {
		t.Fatalf("HunkCount = %d, want 3; lines = %#v", diff.HunkCount, diff.DiffEntryLines)
	}

	hunks := hunkLines(diff.DiffEntryLines)
	if len(hunks) != 3 {
		t.Fatalf("hunk lines = %d, want 3", len(hunks))
	}
	if hunks[0].OldStart != 1 || hunks[0].OldLines != 4 || hunks[0].NewStart != 1 || hunks[0].NewLines != 4 {
		t.Fatalf("first hunk range mismatch: %#v", hunks[0])
	}
	if hunks[1].OldStart != 5 || hunks[1].OldLines != 7 || hunks[1].NewStart != 5 || hunks[1].NewLines != 7 {
		t.Fatalf("second hunk range mismatch: %#v", hunks[1])
	}
	if hunks[2].OldStart != 12 || hunks[2].OldLines != 4 || hunks[2].NewStart != 12 || hunks[2].NewLines != 4 {
		t.Fatalf("third hunk range mismatch: %#v", hunks[2])
	}
}

func hunkLines(lines []DiffEntryLine) []DiffEntryLine {
	out := []DiffEntryLine{}
	for _, line := range lines {
		if line.Type == "HUNK" {
			out = append(out, line)
		}
	}
	return out
}
