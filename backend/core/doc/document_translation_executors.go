package doc

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type backendTranslationUnit struct {
	ID   string
	Text string
}

const (
	translationAttributionText = "由 LazyMind 免费翻译"
	translationAttributionURL  = "https://github.com/LazyAGI/LazyMind"
)

type backendDocumentTranslationExecutor interface {
	Units() []backendTranslationUnit
	Build(context.Context, map[string]string, string) (int, error)
}

type plainDocumentExecutor struct {
	parts []string
	units []backendTranslationUnit
}

// markdownDocumentExecutor keeps Markdown syntax in immutable parts and only
// exposes visible prose to the translator. Translation providers therefore
// cannot accidentally rewrite heading levels, list nesting, links, or code.
type markdownDocumentExecutor struct {
	parts []string
	units []backendTranslationUnit
}

var (
	markdownFenceRE       = regexp.MustCompile(`^ {0,3}(` + "`{3,}" + `|~{3,})`)
	markdownHeadingRE     = regexp.MustCompile(`^( {0,3}#{1,6}[\t ]+)`)
	markdownHeadingEndRE  = regexp.MustCompile(`([\t ]+#+[\t ]*)$`)
	markdownReferenceRE   = regexp.MustCompile(`^ {0,3}\[[^]]+\]:[\t ]*\S+`)
	markdownTableDivider  = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(?:\|\s*:?-{3,}:?\s*)+\|?\s*$`)
	markdownThematicBreak = regexp.MustCompile(`^ {0,3}(?:\*[\t ]*){3,}$|^ {0,3}(?:-[\t ]*){3,}$|^ {0,3}(?:_[\t ]*){3,}$`)
)

func newMarkdownDocumentExecutor(source []byte) *markdownDocumentExecutor {
	e := &markdownDocumentExecutor{}
	lines := strings.SplitAfter(string(source), "\n")
	inFence := false
	fenceChar := byte(0)
	fenceLength := 0
	inFrontMatter := len(lines) > 0 && strings.TrimRight(lines[0], "\r\n") == "---"

	for lineIndex, lineWithEnding := range lines {
		line := strings.TrimSuffix(lineWithEnding, "\n")
		newline := ""
		if line != lineWithEnding {
			newline = "\n"
		}
		content := strings.TrimSuffix(line, "\r")
		if len(content) != len(line) {
			newline = "\r" + newline
		}

		trimmed := strings.TrimSpace(content)
		if inFrontMatter {
			e.appendMarkdownRaw(content + newline)
			if lineIndex > 0 && (trimmed == "---" || trimmed == "...") {
				inFrontMatter = false
			}
			continue
		}
		if match := markdownFenceRE.FindStringSubmatch(content); len(match) > 0 {
			marker := match[1]
			if !inFence {
				inFence, fenceChar, fenceLength = true, marker[0], len(marker)
			} else if marker[0] == fenceChar && len(marker) >= fenceLength {
				inFence = false
			}
			e.appendMarkdownRaw(content + newline)
			continue
		}
		if inFence || trimmed == "" || markdownReferenceRE.MatchString(content) ||
			markdownTableDivider.MatchString(content) || markdownThematicBreak.MatchString(content) ||
			strings.HasPrefix(strings.TrimLeft(content, " \t"), "<!--") || strings.HasPrefix(content, "\t") {
			e.appendMarkdownRaw(content + newline)
			continue
		}

		prefixLength := markdownBlockPrefixLength(content)
		prefix, body := content[:prefixLength], content[prefixLength:]
		e.appendMarkdownRaw(prefix)
		suffix := ""
		if markdownHeadingRE.MatchString(prefix) {
			if loc := markdownHeadingEndRE.FindStringIndex(body); loc != nil {
				suffix, body = body[loc[0]:], body[:loc[0]]
			}
		}
		e.appendMarkdownInline(body)
		e.appendMarkdownRaw(suffix + newline)
	}
	return e
}

func markdownBlockPrefixLength(line string) int {
	position := 0
	for position < len(line) && (line[position] == ' ' || line[position] == '\t') {
		position++
	}
	for {
		start := position
		if position < len(line) && line[position] == '>' {
			position++
			for position < len(line) && (line[position] == ' ' || line[position] == '\t') {
				position++
			}
			continue
		}
		markerEnd := position
		if markerEnd < len(line) && strings.ContainsRune("-*+", rune(line[markerEnd])) {
			markerEnd++
		} else {
			for markerEnd < len(line) && line[markerEnd] >= '0' && line[markerEnd] <= '9' {
				markerEnd++
			}
			if markerEnd == position || markerEnd >= len(line) || (line[markerEnd] != '.' && line[markerEnd] != ')') {
				markerEnd = position
			} else {
				markerEnd++
			}
		}
		if markerEnd > position && markerEnd < len(line) && (line[markerEnd] == ' ' || line[markerEnd] == '\t') {
			position = markerEnd
			for position < len(line) && (line[position] == ' ' || line[position] == '\t') {
				position++
			}
			if position+3 <= len(line) && line[position] == '[' && line[position+2] == ']' && strings.ContainsRune(" xX", rune(line[position+1])) {
				position += 3
				for position < len(line) && (line[position] == ' ' || line[position] == '\t') {
					position++
				}
			}
			continue
		}
		if start == position {
			break
		}
	}
	if match := markdownHeadingRE.FindStringIndex(line[position:]); match != nil && match[0] == 0 {
		position += match[1]
	}
	return position
}

func (e *markdownDocumentExecutor) appendMarkdownRaw(value string) {
	if value != "" {
		e.parts = append(e.parts, value)
	}
}

func (e *markdownDocumentExecutor) appendMarkdownText(value string) {
	if value == "" {
		return
	}
	left := len(value) - len(strings.TrimLeft(value, " \t"))
	rightTrimmed := strings.TrimRight(value[left:], " \t")
	e.appendMarkdownRaw(value[:left])
	if rightTrimmed != "" {
		index := len(e.parts)
		e.parts = append(e.parts, rightTrimmed)
		e.units = append(e.units, backendTranslationUnit{ID: fmt.Sprintf("markdown-%d", index), Text: rightTrimmed})
	}
	e.appendMarkdownRaw(value[left+len(rightTrimmed):])
}

func (e *markdownDocumentExecutor) appendMarkdownInline(value string) {
	plainStart := 0
	flush := func(end int) {
		e.appendMarkdownText(value[plainStart:end])
	}
	for position := 0; position < len(value); {
		end := markdownProtectedSpanEnd(value, position)
		if end == position {
			position++
			continue
		}
		flush(position)
		e.appendMarkdownRaw(value[position:end])
		position, plainStart = end, end
	}
	flush(len(value))
}

func markdownProtectedSpanEnd(value string, position int) int {
	if value[position] == '\\' && position+1 < len(value) {
		return position + 2
	}
	if value[position] == '`' {
		end := position
		for end < len(value) && value[end] == '`' {
			end++
		}
		if closeAt := strings.Index(value[end:], value[position:end]); closeAt >= 0 {
			return end + closeAt + end - position
		}
	}
	if value[position] == '$' {
		end := position
		for end < len(value) && value[end] == '$' {
			end++
		}
		if closeAt := strings.Index(value[end:], value[position:end]); closeAt >= 0 {
			return end + closeAt + end - position
		}
	}
	if strings.HasPrefix(value[position:], "![") {
		if end := markdownLinkEnd(value, position+1); end > 0 {
			return end
		}
	}
	if value[position] == '[' {
		// Preserve link syntax as a whole. This deliberately leaves link labels
		// unchanged so URLs, anchors, and reference identifiers remain reliable.
		if end := markdownLinkEnd(value, position); end > 0 {
			return end
		}
	}
	if value[position] == '<' {
		if relative := strings.IndexByte(value[position:], '>'); relative >= 0 {
			return position + relative + 1
		}
	}
	if strings.HasPrefix(value[position:], "http://") || strings.HasPrefix(value[position:], "https://") {
		end := position
		for end < len(value) && !strings.ContainsRune(" \t<>", rune(value[end])) {
			end++
		}
		return end
	}
	if strings.ContainsRune("*_~|", rune(value[position])) {
		end := position + 1
		for end < len(value) && value[end] == value[position] {
			end++
		}
		return end
	}
	return position
}

func markdownLinkEnd(value string, open int) int {
	closeLabel := strings.IndexByte(value[open+1:], ']')
	if closeLabel < 0 {
		return 0
	}
	closeLabel += open + 1
	if closeLabel+1 >= len(value) || (value[closeLabel+1] != '(' && value[closeLabel+1] != '[') {
		return 0
	}
	opening, closing := value[closeLabel+1], byte(')')
	if opening == '[' {
		closing = ']'
	}
	depth := 1
	for i := closeLabel + 2; i < len(value); i++ {
		if value[i] == '\\' {
			i++
			continue
		}
		if value[i] == opening {
			depth++
		} else if value[i] == closing {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

func (e *markdownDocumentExecutor) Units() []backendTranslationUnit { return e.units }
func (e *markdownDocumentExecutor) Build(_ context.Context, translations map[string]string, output string) (int, error) {
	parts := append([]string(nil), e.parts...)
	for _, unit := range e.units {
		var index int
		translated, ok := translations[unit.ID]
		if ok {
			if _, err := fmt.Sscanf(unit.ID, "markdown-%d", &index); err == nil {
				parts[index] = translated
			}
		}
	}
	content := strings.TrimRight(strings.Join(parts, ""), "\r\n")
	content += "\n\n---\n\n[" + translationAttributionText + "](" + translationAttributionURL + ")\n"
	return 0, os.WriteFile(output, []byte(content), 0o640)
}

type htmlDocumentExecutor struct {
	parts []string
	units []backendTranslationUnit
}

func newHTMLDocumentExecutor(source []byte) *htmlDocumentExecutor {
	parts := regexp.MustCompile(`(?s)(<[^>]+>)`).Split(string(source), -1)
	tags := regexp.MustCompile(`(?s)(<[^>]+>)`).FindAllString(string(source), -1)
	interleaved := make([]string, 0, len(parts)+len(tags))
	units := make([]backendTranslationUnit, 0)
	inIgnoredElement := false
	for index, part := range parts {
		interleaved = append(interleaved, part)
		if !inIgnoredElement && strings.TrimSpace(html.UnescapeString(part)) != "" {
			units = append(units, backendTranslationUnit{ID: fmt.Sprintf("html-%d", len(interleaved)-1), Text: html.UnescapeString(part)})
		}
		if index < len(tags) {
			tag := tags[index]
			lower := strings.ToLower(strings.TrimSpace(tag))
			if strings.HasPrefix(lower, "<script") || strings.HasPrefix(lower, "<style") {
				inIgnoredElement = true
			} else if strings.HasPrefix(lower, "</script") || strings.HasPrefix(lower, "</style") {
				inIgnoredElement = false
			}
			interleaved = append(interleaved, tag)
		}
	}
	return &htmlDocumentExecutor{parts: interleaved, units: units}
}

func (e *htmlDocumentExecutor) Units() []backendTranslationUnit { return e.units }
func (e *htmlDocumentExecutor) Build(_ context.Context, translations map[string]string, output string) (int, error) {
	parts := append([]string(nil), e.parts...)
	for _, unit := range e.units {
		var index int
		if _, err := fmt.Sscanf(unit.ID, "html-%d", &index); err == nil {
			parts[index] = html.EscapeString(translations[unit.ID])
		}
	}
	content := strings.Join(parts, "")
	attribution := `<footer style="margin-top:2rem;padding-top:.75rem;border-top:1px solid #e5e7eb;color:#6b7280;font-size:12px"><a href="` + translationAttributionURL + `" style="color:inherit">` + translationAttributionText + `</a></footer>`
	if index := strings.LastIndex(strings.ToLower(content), "</body>"); index >= 0 {
		content = content[:index] + attribution + content[index:]
	} else {
		content += attribution
	}
	return 0, os.WriteFile(output, []byte(content), 0o640)
}

func newPlainDocumentExecutor(source []byte) *plainDocumentExecutor {
	parts := regexp.MustCompile(`(?m)(\n\s*\n)`).Split(string(source), -1)
	separators := regexp.MustCompile(`(?m)(\n\s*\n)`).FindAllString(string(source), -1)
	interleaved := make([]string, 0, len(parts)+len(separators))
	units := make([]backendTranslationUnit, 0, len(parts))
	for i, part := range parts {
		interleaved = append(interleaved, part)
		if strings.TrimSpace(part) != "" && !strings.HasPrefix(strings.TrimSpace(part), "```") {
			units = append(units, backendTranslationUnit{ID: fmt.Sprintf("text-%d", len(interleaved)-1), Text: part})
		}
		if i < len(separators) {
			interleaved = append(interleaved, separators[i])
		}
	}
	return &plainDocumentExecutor{parts: interleaved, units: units}
}

func (e *plainDocumentExecutor) Units() []backendTranslationUnit { return e.units }
func (e *plainDocumentExecutor) Build(_ context.Context, translations map[string]string, output string) (int, error) {
	parts := append([]string(nil), e.parts...)
	for _, unit := range e.units {
		var index int
		if _, err := fmt.Sscanf(unit.ID, "text-%d", &index); err == nil {
			parts[index] = translations[unit.ID]
		}
	}
	content := strings.TrimRight(strings.Join(parts, ""), "\r\n")
	content += "\n\n— " + translationAttributionText + " · " + translationAttributionURL + "\n"
	return 0, os.WriteFile(output, []byte(content), 0o640)
}

type openXMLFile struct {
	name string
	raw  []byte
}

type openXMLDocumentExecutor struct {
	extension    string
	files        []openXMLFile
	otherEntries map[string][]byte
	units        []backendTranslationUnit
	paragraphRE  *regexp.Regexp
	textRE       *regexp.Regexp
}

func openXMLPatterns(extension string) (*regexp.Regexp, *regexp.Regexp, *regexp.Regexp) {
	switch extension {
	case ".docx":
		return regexp.MustCompile(`^word/(document|header\d+|footer\d+|footnotes|endnotes|comments)\.xml$`),
			regexp.MustCompile(`(?s)<w:p(?:\s[^>]*)?>.*?</w:p>`), regexp.MustCompile(`(?s)(<w:t(?:\s[^>]*)?>)(.*?)(</w:t>)`)
	case ".pptx":
		return regexp.MustCompile(`^ppt/(slides/slide\d+|notesSlides/notesSlide\d+)\.xml$`),
			regexp.MustCompile(`(?s)<a:p(?:\s[^>]*)?>.*?</a:p>`), regexp.MustCompile(`(?s)(<a:t(?:\s[^>]*)?>)(.*?)(</a:t>)`)
	default:
		return regexp.MustCompile(`^xl/sharedStrings\.xml$`), regexp.MustCompile(`(?s)<si(?:\s[^>]*)?>.*?</si>`),
			regexp.MustCompile(`(?s)(<t(?:\s[^>]*)?>)(.*?)(</t>)`)
	}
}

func newOpenXMLDocumentExecutor(path, extension string) (*openXMLDocumentExecutor, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	pathRE, paragraphRE, textRE := openXMLPatterns(extension)
	executor := &openXMLDocumentExecutor{extension: extension, otherEntries: map[string][]byte{}, paragraphRE: paragraphRE, textRE: textRE}
	for _, file := range reader.File {
		in, err := file.Open()
		if err != nil {
			return nil, err
		}
		raw, readErr := io.ReadAll(in)
		_ = in.Close()
		if readErr != nil {
			return nil, readErr
		}
		if !pathRE.MatchString(file.Name) {
			executor.otherEntries[file.Name] = raw
			continue
		}
		fileIndex := len(executor.files)
		for paragraphIndex, paragraph := range paragraphRE.FindAll(raw, -1) {
			matches := textRE.FindAllSubmatch(paragraph, -1)
			var text strings.Builder
			for _, match := range matches {
				text.WriteString(html.UnescapeString(string(match[2])))
			}
			if strings.TrimSpace(text.String()) != "" {
				executor.units = append(executor.units, backendTranslationUnit{ID: fmt.Sprintf("xml-%d-%d", fileIndex, paragraphIndex), Text: text.String()})
			}
		}
		executor.files = append(executor.files, openXMLFile{name: file.Name, raw: raw})
	}
	return executor, nil
}

func (e *openXMLDocumentExecutor) Units() []backendTranslationUnit { return e.units }
func (e *openXMLDocumentExecutor) Build(_ context.Context, translations map[string]string, output string) (int, error) {
	entries := make(map[string][]byte, len(e.otherEntries)+len(e.files))
	for name, raw := range e.otherEntries {
		entries[name] = raw
	}
	for fileIndex, file := range e.files {
		paragraphIndex := 0
		entries[file.name] = e.paragraphRE.ReplaceAllFunc(file.raw, func(paragraph []byte) []byte {
			id := fmt.Sprintf("xml-%d-%d", fileIndex, paragraphIndex)
			paragraphIndex++
			translated, ok := translations[id]
			if !ok {
				return paragraph
			}
			textIndex := 0
			return e.textRE.ReplaceAllFunc(paragraph, func(textNode []byte) []byte {
				matches := e.textRE.FindSubmatch(textNode)
				content := ""
				if textIndex == 0 {
					content = html.EscapeString(translated)
				}
				textIndex++
				return append(append(append([]byte{}, matches[1]...), []byte(content)...), matches[3]...)
			})
		})
	}
	appendOpenXMLTranslationAttribution(entries, e.extension)
	out, err := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	zw := zip.NewWriter(out)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		writer, createErr := zw.Create(name)
		if createErr != nil {
			_ = zw.Close()
			_ = out.Close()
			return 0, createErr
		}
		if _, err = io.Copy(writer, bytes.NewReader(entries[name])); err != nil {
			_ = zw.Close()
			_ = out.Close()
			return 0, err
		}
	}
	if err = zw.Close(); err != nil {
		_ = out.Close()
		return 0, err
	}
	return 0, out.Close()
}

func appendOpenXMLTranslationAttribution(entries map[string][]byte, extension string) {
	label := html.EscapeString(translationAttributionText + " · " + translationAttributionURL)
	switch extension {
	case ".docx":
		name := "word/document.xml"
		content := string(entries[name])
		paragraph := `<w:p><w:pPr><w:spacing w:before="160"/><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:color w:val="808080"/><w:sz w:val="18"/></w:rPr><w:t>` + label + `</w:t></w:r></w:p>`
		if index := strings.LastIndex(content, "</w:body>"); index >= 0 {
			entries[name] = []byte(content[:index] + paragraph + content[index:])
		}
	case ".pptx":
		name := "ppt/slides/slide1.xml"
		content := string(entries[name])
		shape := `<p:sp><p:nvSpPr><p:cNvPr id="999999" name="LazyMind translation attribution"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="457200" y="6500000"/><a:ext cx="8229600" cy="300000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:pPr algn="ctr"/><a:r><a:rPr lang="zh-CN" sz="900"><a:solidFill><a:srgbClr val="808080"/></a:solidFill></a:rPr><a:t>` + label + `</a:t></a:r></a:p></p:txBody></p:sp>`
		if index := strings.LastIndex(content, "</p:spTree>"); index >= 0 {
			entries[name] = []byte(content[:index] + shape + content[index:])
		}
	case ".xlsx":
		name := "xl/worksheets/sheet1.xml"
		content := string(entries[name])
		rowNumber := 1
		rowRE := regexp.MustCompile(`<row[^>]*\br="(\d+)"`)
		for _, match := range rowRE.FindAllStringSubmatch(content, -1) {
			var current int
			_, _ = fmt.Sscanf(match[1], "%d", &current)
			if current >= rowNumber {
				rowNumber = current + 2
			}
		}
		row := fmt.Sprintf(`<row r="%d"><c r="A%d" t="inlineStr"><is><t>%s</t></is></c></row>`, rowNumber, rowNumber, label)
		if index := strings.LastIndex(content, "</sheetData>"); index >= 0 {
			entries[name] = []byte(content[:index] + row + content[index:])
		}
	}
}

func newBackendDocumentTranslationExecutor(path string) (backendDocumentTranslationExecutor, error) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".docx", ".pptx", ".xlsx":
		return newOpenXMLDocumentExecutor(path, extension)
	case ".md", ".markdown", ".txt", ".html", ".htm":
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if extension == ".html" || extension == ".htm" {
			return newHTMLDocumentExecutor(raw), nil
		}
		if extension == ".md" || extension == ".markdown" {
			return newMarkdownDocumentExecutor(raw), nil
		}
		return newPlainDocumentExecutor(raw), nil
	default:
		return nil, fmt.Errorf("unsupported document translation executor: %s", extension)
	}
}
