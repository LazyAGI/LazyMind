package filediff

import (
	"strings"
	"testing"
)

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

func TestCompareContentSplitsAdjacentLocalEditsPerLine(t *testing.T) {
	oldLines := []string{
		"- 审查初始需求中的功能范围、依赖关系和安全要求",
		"- 识别是否存在多层级嵌套、跨系统依赖或超出工具处理能力的特性",
		"- 检查是否已确认工具可用至安全性和正确参数格式",
	}
	newLines := []string{
		"- 审查初始需求22222中的功能范围、依赖关系和安全要求",
		"- 识别是否存在多22222222222层级嵌套、跨系统依赖或超出工具处理能力的特性",
		"- 检查是否已确认工具222222222222可用至安全性和正确参数格式",
	}
	diff := compareText(t, "memory.md", oldLines, newLines)

	hunks := hunkLines(diff.DiffEntryLines)
	if len(hunks) != 3 {
		t.Fatalf("hunk count = %d, want 3; lines = %#v", len(hunks), diff.DiffEntryLines)
	}
	for index, hunk := range hunks {
		if hunk.OldLines != 1 || hunk.NewLines != 1 {
			t.Fatalf("hunk %d ranges = old:%d new:%d, want 1/1", index, hunk.OldLines, hunk.NewLines)
		}
	}
	assertDiffLineTypes(t, diff.DiffEntryLines,
		"HUNK", "DELETION", "ADDITION",
		"HUNK", "DELETION", "ADDITION",
		"HUNK", "DELETION", "ADDITION",
	)
}

func TestCompareContentGroupsAdjacentWholeLineReplacements(t *testing.T) {
	oldLines := []string{
		"- 当遇到明确错误信息时，立即解析错误提示",
		"- 按照错误要求调整参数格式，而不是反复使用相同的错误参数尝试",
		"- 常见的格式问题包括 YAML frontmatter 缺失和必需字段不完整",
	}
	newLines := []string{
		"- 22222222222222222222222222222222",
		"- 33333333333333333333333333333333",
		"- 44444444444444444444444444444444",
	}
	diff := compareText(t, "skill.md", oldLines, newLines)

	hunks := hunkLines(diff.DiffEntryLines)
	if len(hunks) != 1 || hunks[0].OldLines != 3 || hunks[0].NewLines != 3 {
		t.Fatalf("unexpected whole-line replacement hunk: %#v", hunks)
	}
	assertDiffLineTypes(t, diff.DiffEntryLines,
		"HUNK", "DELETION", "DELETION", "DELETION", "ADDITION", "ADDITION", "ADDITION",
	)
	for index := 0; index < 3; index++ {
		if diff.DiffEntryLines[index+1].Text != oldLines[index] || diff.DiffEntryLines[index+4].Text != newLines[index] {
			t.Fatalf("whole-line replacement order mismatch: %#v", diff.DiffEntryLines)
		}
	}
}

func TestCompareContentSeparatesLocalAndWholeLineReplacementGroups(t *testing.T) {
	oldLines := []string{
		"- 保持回答简洁",
		"- 当遇到错误时详细解释旧逻辑",
		"- 根据旧参数调用原来的处理流程",
		"- 修改后运行测试",
	}
	newLines := []string{
		"- 保持回答非常简洁",
		"- 2222222222222222222222",
		"- 3333333333333333333333",
		"- 修改完成后运行测试",
	}
	diff := compareText(t, "memory/user.md", oldLines, newLines)

	hunks := hunkLines(diff.DiffEntryLines)
	if len(hunks) != 3 {
		t.Fatalf("hunk count = %d, want 3; lines = %#v", len(hunks), diff.DiffEntryLines)
	}
	wantRanges := []int{1, 2, 1}
	for index, want := range wantRanges {
		if hunks[index].OldLines != want || hunks[index].NewLines != want {
			t.Fatalf("hunk %d ranges = old:%d new:%d, want %d", index, hunks[index].OldLines, hunks[index].NewLines, want)
		}
	}
}

func TestWholeLineReplacementRequiresSeventyPercentChangeOnBothSides(t *testing.T) {
	tests := []struct {
		name    string
		oldText string
		newText string
		want    bool
	}{
		{name: "sixty percent remains local", oldText: "abcdefghij", newText: "abcdKLMNOP", want: false},
		{name: "seventy percent is whole line", oldText: "abcdefghij", newText: "abcKLMNOPQ", want: true},
		{name: "expansion remains local", oldText: "核心内容", newText: "核心内容以及新增的详细说明", want: false},
		{name: "common markdown marker is ignored", oldText: "- abcdefghij", newText: "- abcKLMNOPQ", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRunes, newRunes := []rune(tt.oldText), []rune(tt.newText)
			oldChanged, newChanged := changedRuneCounts(oldRunes, newRunes)
			if got := isWholeLineReplacement(tt.oldText, tt.newText, oldChanged, newChanged); got != tt.want {
				t.Fatalf("isWholeLineReplacement() = %v, want %v; changed = %d/%d", got, tt.want, oldChanged, newChanged)
			}
		})
	}
}

func TestCompareContentGroupsUnequalWholeLineReplacement(t *testing.T) {
	diff := compareText(t, "memory.md",
		[]string{"旧说明第一行", "旧说明第二行", "旧说明第三行"},
		[]string{"222222222222"},
	)
	assertDiffLineTypes(t, diff.DiffEntryLines, "HUNK", "DELETION", "DELETION", "DELETION", "ADDITION")
}

func compareText(t *testing.T, path string, oldLines, newLines []string) FileDiff {
	t.Helper()
	oldText := strings.Join(oldLines, "\n") + "\n"
	newText := strings.Join(newLines, "\n") + "\n"
	diff, err := CompareContent(
		Content{Path: path, Data: []byte(oldText), EditableText: true},
		Content{Path: path, Data: []byte(newText), EditableText: true},
		Options{},
	)
	if err != nil {
		t.Fatalf("CompareContent returned error: %v", err)
	}
	return diff
}

func assertDiffLineTypes(t *testing.T, lines []DiffEntryLine, want ...string) {
	t.Helper()
	if len(lines) != len(want) {
		t.Fatalf("line count = %d, want %d; lines = %#v", len(lines), len(want), lines)
	}
	for index, line := range lines {
		if line.Type != want[index] {
			t.Fatalf("line %d type = %s, want %s; lines = %#v", index, line.Type, want[index], lines)
		}
	}
}
