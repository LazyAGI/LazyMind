package filediff

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"
)

const defaultMaxTextBytes = 512 * 1024
const maxInlineDiffCells = 20000
const wholeLineChangeThresholdPercent = 70

func CompareContent(old Content, next Content, opts Options) (FileDiff, error) {
	maxBytes := defaultMaxTextBytes
	result := FileDiff{
		Path:         firstNonEmpty(next.Path, old.Path),
		Status:       "modified",
		Binary:       old.Binary || next.Binary,
		EditableText: old.EditableText && next.EditableText,
		Supported:    true,
	}
	if string(old.Data) == string(next.Data) && old.Binary == next.Binary {
		result.Status = "unchanged"
	}
	if old.Size == 0 {
		old.Size = int64(len(old.Data))
	}
	if next.Size == 0 {
		next.Size = int64(len(next.Data))
	}
	if result.Binary {
		result.Supported = false
		result.UnsupportedReason = "binary content is not supported"
		return result, nil
	}
	if !result.EditableText {
		result.Supported = false
		result.UnsupportedReason = "content is not editable text"
		return result, nil
	}
	if old.Size > int64(maxBytes) || next.Size > int64(maxBytes) {
		result.Supported = false
		result.UnsupportedReason = "content is too large"
		result.TooLarge = true
		return result, nil
	}
	if !utf8.Valid(old.Data) || !utf8.Valid(next.Data) {
		result.Supported = false
		result.UnsupportedReason = "content is not valid utf-8"
		result.Binary = true
		return result, nil
	}
	if strings.EqualFold(opts.Mode, "context") {
		text := injectedContextText(string(next.Data), opts)
		result.DiffEntryLines = []DiffEntryLine{{
			Type:    "INJECTED_CONTEXT",
			Text:    text,
			HTML:    html.EscapeString(text),
			NewLine: opts.NewStart,
			OldLine: opts.OldStart,
		}}
		return result, nil
	}
	result.DiffEntryLines = buildTextDiff(string(old.Data), string(next.Data))
	result.HunkCount = countHunks(result.DiffEntryLines)
	return result, nil
}

func buildTextDiff(oldText, newText string) []DiffEntryLine {
	oldLines, oldHasFinalNewline := splitLines(oldText)
	newLines, newHasFinalNewline := splitLines(newText)
	lines := []DiffEntryLine{}
	oldLine, newLine := 1, 1
	for len(oldLines) > 0 || len(newLines) > 0 {
		switch {
		case len(oldLines) > 0 && len(newLines) > 0 && oldLines[0] == newLines[0]:
			lines = append(lines, diffLine("CONTEXT", oldLines[0], oldLine, newLine))
			oldLines = oldLines[1:]
			newLines = newLines[1:]
			oldLine++
			newLine++
		case len(oldLines) > 0 && len(newLines) > 0:
			deletion, addition := changedLinePair(oldLines[0], newLines[0], oldLine, newLine)
			lines = append(lines, deletion, addition)
			oldLines = oldLines[1:]
			newLines = newLines[1:]
			oldLine++
			newLine++
		case len(oldLines) > 0:
			lines = append(lines, highlightedDiffLine("DELETION", oldLines[0], oldLine, 0, "diff-deletion"))
			oldLines = oldLines[1:]
			oldLine++
		default:
			lines = append(lines, highlightedDiffLine("ADDITION", newLines[0], 0, newLine, "diff-addition"))
			newLines = newLines[1:]
			newLine++
		}
	}
	if oldHasFinalNewline != newHasFinalNewline && len(lines) > 0 {
		lines[len(lines)-1].DisplayNoNewLineWarning = true
	}
	return insertHunkHeaders(lines)
}

func splitLines(text string) ([]string, bool) {
	if text == "" {
		return nil, true
	}
	hasFinalNewline := strings.HasSuffix(text, "\n")
	parts := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	return parts, hasFinalNewline
}

func diffLine(typ, text string, oldLine, newLine int) DiffEntryLine {
	return DiffEntryLine{
		Type:    typ,
		Text:    text,
		HTML:    html.EscapeString(text),
		OldLine: oldLine,
		NewLine: newLine,
	}
}

func highlightedDiffLine(typ, text string, oldLine, newLine int, class string) DiffEntryLine {
	line := diffLine(typ, text, oldLine, newLine)
	line.HTML = wrapDiffSpan(class, html.EscapeString(text))
	return line
}

func changedLinePair(oldText, newText string, oldLine, newLine int) (DiffEntryLine, DiffEntryLine) {
	oldHTML, newHTML, wholeLineReplacement := inlineDiffHTML(oldText, newText)
	deletion := diffLine("DELETION", oldText, oldLine, 0)
	addition := diffLine("ADDITION", newText, 0, newLine)
	deletion.HTML = oldHTML
	addition.HTML = newHTML
	deletion.wholeLineReplacement = wholeLineReplacement
	addition.wholeLineReplacement = wholeLineReplacement
	return deletion, addition
}

func inlineDiffHTML(oldText, newText string) (string, string, bool) {
	oldRunes := []rune(oldText)
	newRunes := []rune(newText)
	if len(oldRunes) == 0 {
		return "", wrapDiffSpan("diff-addition", html.EscapeString(newText)), false
	}
	if len(newRunes) == 0 {
		return wrapDiffSpan("diff-deletion", html.EscapeString(oldText)), "", false
	}
	if len(oldRunes)*len(newRunes) > maxInlineDiffCells {
		oldChanged, newChanged := changedRuneCounts(oldRunes, newRunes)
		return wrapDiffSpan("diff-deletion", html.EscapeString(oldText)), wrapDiffSpan("diff-addition", html.EscapeString(newText)), isWholeLineReplacement(oldText, newText, oldChanged, newChanged)
	}

	oldChanged, newChanged := changedRuneMasks(oldRunes, newRunes)
	oldChangedCount := countChangedRunes(oldChanged)
	newChangedCount := countChangedRunes(newChanged)
	return renderInlineDiffHTML(oldRunes, oldChanged, "diff-deletion"), renderInlineDiffHTML(newRunes, newChanged, "diff-addition"), isWholeLineReplacement(oldText, newText, oldChangedCount, newChangedCount)
}

func changedRuneMasks(oldRunes, newRunes []rune) ([]bool, []bool) {
	m, n := len(oldRunes), len(newRunes)
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldRunes[i] == newRunes[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
				continue
			}
			if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	oldChanged := make([]bool, m)
	newChanged := make([]bool, n)
	for i := range oldChanged {
		oldChanged[i] = true
	}
	for i := range newChanged {
		newChanged[i] = true
	}
	for i, j := 0, 0; i < m && j < n; {
		if oldRunes[i] == newRunes[j] {
			oldChanged[i] = false
			newChanged[j] = false
			i++
			j++
			continue
		}
		if lcs[i+1][j] >= lcs[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return oldChanged, newChanged
}

func renderInlineDiffHTML(runes []rune, changed []bool, class string) string {
	var builder strings.Builder
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && changed[j] == changed[i] {
			j++
		}
		escaped := html.EscapeString(string(runes[i:j]))
		if changed[i] {
			builder.WriteString(wrapDiffSpan(class, escaped))
		} else {
			builder.WriteString(escaped)
		}
		i = j
	}
	return builder.String()
}

func wrapDiffSpan(class, escapedText string) string {
	return `<span class="` + class + `">` + escapedText + `</span>`
}

func insertHunkHeaders(lines []DiffEntryLine) []DiffEntryLine {
	blocks := buildHunkBlocks(lines)
	if len(blocks) == 0 {
		return nil
	}

	out := make([]DiffEntryLine, 0, len(lines)+len(blocks))
	for index, block := range blocks {
		out = append(out, newHunkHeader(index+1))
		out = append(out, block...)
	}
	updateHunkHeaders(out)
	return out
}

func changedRuneCounts(oldRunes, newRunes []rune) (int, int) {
	if len(oldRunes) == 0 || len(newRunes) == 0 {
		return len(oldRunes), len(newRunes)
	}
	if len(oldRunes)*len(newRunes) <= maxInlineDiffCells {
		oldChanged, newChanged := changedRuneMasks(oldRunes, newRunes)
		return countChangedRunes(oldChanged), countChangedRunes(newChanged)
	}

	prefix := 0
	for prefix < len(oldRunes) && prefix < len(newRunes) && oldRunes[prefix] == newRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldRunes)-prefix && suffix < len(newRunes)-prefix && oldRunes[len(oldRunes)-1-suffix] == newRunes[len(newRunes)-1-suffix] {
		suffix++
	}
	return len(oldRunes) - prefix - suffix, len(newRunes) - prefix - suffix
}

func countChangedRunes(changed []bool) int {
	count := 0
	for _, isChanged := range changed {
		if isChanged {
			count++
		}
	}
	return count
}

func isWholeLineReplacement(oldText, newText string, oldChanged, newChanged int) bool {
	oldLength := utf8.RuneCountInString(oldText)
	newLength := utf8.RuneCountInString(newText)
	oldPrefix := markdownListPrefix(oldText)
	if oldPrefix != "" && oldPrefix == markdownListPrefix(newText) {
		prefixLength := utf8.RuneCountInString(oldPrefix)
		oldLength -= prefixLength
		newLength -= prefixLength
	}
	if oldLength <= 0 || newLength <= 0 {
		return false
	}
	return oldChanged*100 >= oldLength*wholeLineChangeThresholdPercent &&
		newChanged*100 >= newLength*wholeLineChangeThresholdPercent
}

func markdownListPrefix(text string) string {
	index := 0
	for index < len(text) && (text[index] == ' ' || text[index] == '\t') {
		index++
	}
	markerStart := index
	if index < len(text) && (text[index] == '-' || text[index] == '*' || text[index] == '+') {
		index++
	} else {
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
		}
		if index == markerStart || index >= len(text) || (text[index] != '.' && text[index] != ')') {
			return ""
		}
		index++
	}
	if index >= len(text) || text[index] != ' ' {
		return ""
	}
	return text[:index+1]
}

func buildHunkBlocks(lines []DiffEntryLine) [][]DiffEntryLine {
	var blocks [][]DiffEntryLine
	var leadingContext []DiffEntryLine
	for index := 0; index < len(lines); {
		if !startsDiffHunk(lines[index]) {
			if len(blocks) == 0 {
				leadingContext = append(leadingContext, lines[index])
			} else {
				blocks[len(blocks)-1] = append(blocks[len(blocks)-1], lines[index])
			}
			index++
			continue
		}

		runStart := index
		for index < len(lines) && startsDiffHunk(lines[index]) {
			index++
		}
		runBlocks := splitChangedRun(lines[runStart:index])
		if len(leadingContext) > 0 && len(runBlocks) > 0 {
			runBlocks[0] = append(append([]DiffEntryLine{}, leadingContext...), runBlocks[0]...)
			leadingContext = nil
		}
		blocks = append(blocks, runBlocks...)
	}
	return blocks
}

func splitChangedRun(lines []DiffEntryLine) [][]DiffEntryLine {
	var blocks [][]DiffEntryLine
	var wholeReplacements []DiffEntryLine
	flushWholeReplacements := func() {
		if len(wholeReplacements) == 0 {
			return
		}
		blocks = append(blocks, deletionsBeforeAdditions(wholeReplacements))
		wholeReplacements = nil
	}

	for index := 0; index < len(lines); {
		if lines[index].Type == "DELETION" && index+1 < len(lines) && lines[index+1].Type == "ADDITION" {
			pair := lines[index : index+2]
			if lines[index].wholeLineReplacement && lines[index+1].wholeLineReplacement {
				wholeReplacements = append(wholeReplacements, pair...)
			} else {
				flushWholeReplacements()
				blocks = append(blocks, append([]DiffEntryLine{}, pair...))
			}
			index += 2
			continue
		}
		wholeReplacements = append(wholeReplacements, lines[index])
		index++
	}
	flushWholeReplacements()
	return blocks
}

func deletionsBeforeAdditions(lines []DiffEntryLine) []DiffEntryLine {
	out := make([]DiffEntryLine, 0, len(lines))
	for _, line := range lines {
		if line.Type == "DELETION" {
			out = append(out, line)
		}
	}
	for _, line := range lines {
		if line.Type != "DELETION" {
			out = append(out, line)
		}
	}
	return out
}

func newHunkHeader(index int) DiffEntryLine {
	return DiffEntryLine{Type: "HUNK", HunkID: fmt.Sprintf("hunk_%d", index)}
}

func isChangedDiffLine(line DiffEntryLine) bool {
	return line.Type == "ADDITION" || line.Type == "DELETION"
}

func startsDiffHunk(line DiffEntryLine) bool {
	return isChangedDiffLine(line) || line.DisplayNoNewLineWarning
}

func updateHunkHeaders(lines []DiffEntryLine) {
	for i := range lines {
		if lines[i].Type != "HUNK" {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if lines[j].Type == "HUNK" {
				end = j
				break
			}
		}
		oldStart, oldLines, newStart, newLines := hunkRange(lines[i+1 : end])
		lines[i].OldStart = oldStart
		lines[i].OldLines = oldLines
		lines[i].NewStart = newStart
		lines[i].NewLines = newLines
		lines[i].Text = formatHunkHeader(oldStart, oldLines, newStart, newLines)
		lines[i].HTML = html.EscapeString(lines[i].Text)
	}
}

func hunkRange(lines []DiffEntryLine) (int, int, int, int) {
	oldStart, newStart := 0, 0
	oldLines, newLines := 0, 0
	for _, line := range lines {
		switch line.Type {
		case "CONTEXT":
			if line.OldLine > 0 {
				if oldStart == 0 {
					oldStart = line.OldLine
				}
				oldLines++
			}
			if line.NewLine > 0 {
				if newStart == 0 {
					newStart = line.NewLine
				}
				newLines++
			}
		case "DELETION":
			if line.OldLine > 0 {
				if oldStart == 0 {
					oldStart = line.OldLine
				}
				oldLines++
			}
		case "ADDITION":
			if line.NewLine > 0 {
				if newStart == 0 {
					newStart = line.NewLine
				}
				newLines++
			}
		}
	}
	if oldStart == 0 {
		oldStart = 1
	}
	if newStart == 0 {
		newStart = 1
	}
	return oldStart, oldLines, newStart, newLines
}

func formatHunkHeader(oldStart, oldLines, newStart, newLines int) string {
	return fmt.Sprintf("@@ -%s +%s @@", formatHunkRange(oldStart, oldLines), formatHunkRange(newStart, newLines))
}

func formatHunkRange(start, lines int) string {
	if lines == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, lines)
}

func injectedContextText(text string, opts Options) string {
	lines, _ := splitLines(text)
	if len(lines) == 0 {
		return ""
	}
	start := opts.NewStart
	if start <= 0 {
		start = 1
	}
	count := opts.Lines
	if count <= 0 {
		count = 1
	}
	start--
	if start >= len(lines) {
		return ""
	}
	end := start + count
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}

func countHunks(lines []DiffEntryLine) int {
	count := 0
	for _, line := range lines {
		if strings.EqualFold(line.Type, "HUNK") {
			count++
		}
	}
	return count
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
