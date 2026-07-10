package filediff

import (
	"html"
	"strings"
	"unicode/utf8"
)

const defaultMaxTextBytes = 512 * 1024
const maxInlineDiffCells = 20000

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
	lines := []DiffEntryLine{{Type: "HUNK", Text: "@@ -1 +1 @@", HTML: "@@ -1 +1 @@", HunkID: "hunk_1"}}
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
	return lines
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
	oldHTML, newHTML := inlineDiffHTML(oldText, newText)
	deletion := diffLine("DELETION", oldText, oldLine, 0)
	addition := diffLine("ADDITION", newText, 0, newLine)
	deletion.HTML = oldHTML
	addition.HTML = newHTML
	return deletion, addition
}

func inlineDiffHTML(oldText, newText string) (string, string) {
	oldRunes := []rune(oldText)
	newRunes := []rune(newText)
	if len(oldRunes) == 0 {
		return "", wrapDiffSpan("diff-addition", html.EscapeString(newText))
	}
	if len(newRunes) == 0 {
		return wrapDiffSpan("diff-deletion", html.EscapeString(oldText)), ""
	}
	if len(oldRunes)*len(newRunes) > maxInlineDiffCells {
		return wrapDiffSpan("diff-deletion", html.EscapeString(oldText)), wrapDiffSpan("diff-addition", html.EscapeString(newText))
	}

	oldChanged, newChanged := changedRuneMasks(oldRunes, newRunes)
	return renderInlineDiffHTML(oldRunes, oldChanged, "diff-deletion"), renderInlineDiffHTML(newRunes, newChanged, "diff-addition")
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
