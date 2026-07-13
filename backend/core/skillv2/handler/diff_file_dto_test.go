package handler

import (
	"testing"

	skilldiff "lazymind/core/skillv2/diff"
)

func TestDiffFileDTOUsesSnakeCaseDiffLines(t *testing.T) {
	dto := diffFileDTO(skilldiff.DiffFile{
		Path:   "SKILL.md",
		Type:   "file",
		Status: "modified",
		DiffEntryLines: []skilldiff.DiffEntryLine{{
			Type:   "HUNK",
			Text:   "@@ -1 +1 @@",
			HunkID: "hunk_1",
		}},
	})

	if _, ok := dto["diffEntryLines"]; ok {
		t.Fatalf("diffFileDTO returned deprecated diffEntryLines field: %#v", dto)
	}
	if _, ok := dto["diff_entry_lines"]; !ok {
		t.Fatalf("diffFileDTO missing diff_entry_lines field: %#v", dto)
	}
}
