package versionfs

import (
	"strings"
	"testing"

	"lazymind/core/filediff"
)

func TestApplyTextReviewAcceptsWholeLineGroupAndRejectsLocalEdit(t *testing.T) {
	oldLines := []string{
		"- 当遇到错误时详细解释旧逻辑",
		"- 根据旧参数调用原来的处理流程",
		"preference: keep concise",
	}
	newLines := []string{
		"- 2222222222222222222222",
		"- 3333333333333333333333",
		"preference: keep very concise",
	}
	headContent := strings.Join(oldLines, "\n") + "\n"
	draftContent := strings.Join(newLines, "\n") + "\n"
	diff, err := filediff.CompareContent(
		filediff.Content{Path: "memory/user.md", Data: []byte(headContent), EditableText: true},
		filediff.Content{Path: "memory/user.md", Data: []byte(draftContent), EditableText: true},
		filediff.Options{},
	)
	if err != nil {
		t.Fatalf("CompareContent returned error: %v", err)
	}

	file := ReviewFile{Path: diff.Path, Type: EntryTypeFile, Status: diff.Status}
	for _, line := range diff.DiffEntryLines {
		file.DiffLines = append(file.DiffLines, DiffLine{
			Type: line.Type, Text: line.Text, HTML: line.HTML,
			OldLine: line.OldLine, NewLine: line.NewLine,
			OldStart: line.OldStart, OldLines: line.OldLines,
			NewStart: line.NewStart, NewLines: line.NewLines,
		})
	}
	AnnotateReviewFile(&file, ReviewSessionMeta{ID: "review-1"}, nil, false)
	hunks := HunkLines(file)
	if len(hunks) != 2 {
		t.Fatalf("hunk count = %d, want 2", len(hunks))
	}

	decisions := map[string]string{
		DecisionKey(file.Path, hunks[0].HunkID): DecisionAccepted,
		DecisionKey(file.Path, hunks[1].HunkID): DecisionRejected,
	}
	got, err := ApplyTextReview(headContent, draftContent, file, decisions)
	if err != nil {
		t.Fatalf("ApplyTextReview returned error: %v", err)
	}
	wantLines := append(append([]string{}, newLines[:2]...), oldLines[2:]...)
	want := strings.Join(wantLines, "\n") + "\n"
	if got != want {
		t.Fatalf("merged content mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}
