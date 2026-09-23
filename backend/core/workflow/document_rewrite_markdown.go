package workflow

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type rewriteMarkdownBlock struct {
	start, end   int
	raw, visible string
	blockType    string
}

// Use the same inline text boundaries as the algorithm: heading/list markers
// stay outside replacement ranges, and nested list items are separate blocks.
var rewriteTaskMarker = regexp.MustCompile(`^\[[ xX]\][ \t]+`)

func rewriteMarkdownBlocks(source string) []rewriteMarkdownBlock {
	data := []byte(source)
	tree := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(data))
	blocks := []rewriteMarkdownBlock{}
	_ = ast.Walk(tree, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Type() == ast.TypeInline || node.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}
		kind := ""
		switch node.Kind() {
		case ast.KindParagraph, ast.KindTextBlock:
			kind = "paragraph"
		case ast.KindHeading:
			kind = "heading"
		}
		for parent := node.Parent(); parent != nil && kind != ""; parent = parent.Parent() {
			switch parent.Kind() {
			case ast.KindListItem:
				if kind == "paragraph" {
					kind = "list_item"
				}
			case ast.KindDocument, ast.KindList:
			default:
				kind = ""
			}
		}
		start := node.Lines().At(0).Start
		end := node.Lines().At(node.Lines().Len() - 1).Stop
		raw := strings.TrimRight(source[start:end], " \t\r\n")
		end = start + len(raw)
		if kind == "list_item" {
			start += len(rewriteTaskMarker.FindString(raw))
			raw = source[start:end]
		}
		if raw == "" {
			return ast.WalkContinue, nil
		}
		var visible strings.Builder
		_ = ast.Walk(node, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch child := child.(type) {
			case *ast.Text:
				value := child.Segment.Value(data)
				if child.Parent() == nil || child.Parent().Kind() != ast.KindCodeSpan {
					// Preserve entity spelling when matching rendered quotes.
					value = util.UnescapePunctuations(value)
				}
				visible.Write(value)
				if child.SoftLineBreak() || child.HardLineBreak() {
					visible.WriteByte(' ')
				}
			case *ast.String:
				visible.Write(child.Value)
			case *ast.AutoLink:
				visible.Write(child.Label(data))
			}
			return ast.WalkContinue, nil
		})
		quote := visible.String()
		if kind == "list_item" {
			quote = rewriteTaskMarker.ReplaceAllString(quote, "")
		}
		blocks = append(blocks, rewriteMarkdownBlock{
			start: utf8.RuneCountInString(source[:start]), end: utf8.RuneCountInString(source[:end]),
			raw: raw, visible: normalizeRewriteQuote(quote), blockType: kind,
		})
		return ast.WalkSkipChildren, nil
	})
	return blocks
}

func rewriteMarkdownStructure(source string) []string {
	tree := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader([]byte(source)))
	structure := []string{}
	_ = ast.Walk(tree, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if node.Type() == ast.TypeInline {
			return ast.WalkSkipChildren, nil
		}
		value := fmt.Sprintf("%s:%t", node.Kind(), entering)
		switch node := node.(type) {
		case *ast.Heading:
			value += fmt.Sprintf(":%d", node.Level)
		case *ast.List:
			value += fmt.Sprintf(":%c:%d:%t", node.Marker, node.Start, node.IsTight)
		}
		structure = append(structure, value)
		return ast.WalkContinue, nil
	})
	return structure
}

func normalizeRewriteQuote(value string) string { return strings.Join(strings.Fields(value), " ") }

func selectedRewriteMarkdownBlocks(source string, selections []map[string]any) ([]rewriteMarkdownBlock, bool) {
	blocks := rewriteMarkdownBlocks(source)
	selected := map[int]bool{}
	runes := []rune(source)
	for _, selection := range selections {
		found := 0
		if start, hasOffsets := selection["start"].(int); hasOffsets {
			end := selection["end"].(int)
			for index, block := range blocks {
				from, to := max(start, block.start), min(end, block.end)
				if from >= to || strings.TrimSpace(string(runes[from:to])) == "" {
					continue
				}
				if block.blockType == "" {
					return nil, false
				}
				selected[index] = true
				found++
			}
		} else {
			quote := normalizeRewriteQuote(selection["selected_text"].(string))
			for index, block := range blocks {
				if strings.Contains(block.visible, quote) || strings.Contains(normalizeRewriteQuote(block.raw), quote) {
					if block.blockType == "" {
						return nil, false
					}
					selected[index] = true
					found++
				}
			}
			if found != 1 {
				return nil, false
			}
		}
		if found == 0 {
			return nil, false
		}
	}
	result := []rewriteMarkdownBlock{}
	for index, block := range blocks {
		if selected[index] {
			result = append(result, block)
		}
	}
	return result, true
}
