package pdfir

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"regexp"
	"strings"
	"unicode"
)

// This deliberately limited replay is pinned to MinerU 3.4.4 (commit
// 0dfc9460cd9ab693b9af60ae3fbffd7bc111b062), backend/vlm/
// vlm_middle_json_mkcontent.py, utils/char_utils.py and
// backend/utils/markdown_utils.py. Hybrid uses that VLM content producer.
// It proves FULL payload equality; it does not infer any parent rune offsets.
const precisionProducerVersion = "3.4.4"

func precisionContentItems(raw []byte) ([]contentItem, error) {
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return nil, e
	}
	for _, f := range z.File {
		if strings.HasSuffix(f.Name, "_content_list.json") {
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			data, e := bounded(r, maxJSONBytes)
			r.Close()
			if e != nil {
				return nil, e
			}
			var items []contentItem
			e = json.Unmarshal(data, &items)
			return items, e
		}
	}
	return nil, precisionError("missing content list")
}
func precisionNormalizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'Ａ' && r <= 'Ｚ' || r >= 'ａ' && r <= 'ｚ' || r >= '０' && r <= '９' {
			return r - 0xfee0
		}
		return r
	}, s)
}
func precisionEscapeText(s string) string {
	var out strings.Builder
	slashes := 0
	for _, r := range precisionNormalizeText(s) {
		if r == '\\' {
			out.WriteRune(r)
			slashes++
			continue
		}
		if strings.ContainsRune("*_`~$", r) && slashes%2 == 0 {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
		slashes = 0
	}
	return out.String()
}

var precisionBlockPrefix = regexp.MustCompile(`^([ \t]{0,3})(#{1,6}|[+-])([ \t])`)
var precisionEQ = regexp.MustCompile(`(?s)<eq>(.*?)</eq>`)
var precisionHTMLImage = regexp.MustCompile(`src="([^"]*)"`)

func precisionRender(para precisionBlock, cjk bool) (string, error) {
	type rendered struct {
		kind, raw, text string
		line, last      int
	}
	parts := []rendered{}
	for i, line := range para.Lines {
		for j, s := range line.Spans {
			text := ""
			switch s.Type {
			case "text":
				text = precisionEscapeText(s.Content)
			case "inline_equation":
				text = "$" + s.Content + "$"
			case "interline_equation":
				text = "\n$$\n" + s.Content + "\n$$\n"
			default:
				return "", precisionError("unsupported producer text span")
			}
			parts = append(parts, rendered{s.Type, s.Content, strings.TrimSpace(text), i, len(line.Spans) - 1 - j})
		}
	}
	var out strings.Builder
	for i, p := range parts {
		if p.text == "" {
			continue
		}
		if p.kind == "interline_equation" {
			out.WriteString(p.text)
			continue
		}
		following := false
		for _, q := range parts[i+1:] {
			if (q.kind == "text" && strings.TrimSpace(precisionNormalizeText(q.raw)) != "") || (q.kind == "inline_equation" && strings.TrimSpace(q.raw) != "") {
				following = true
				break
			}
		}
		if cjk {
			out.WriteString(p.text)
			if following && (p.last != 0 || p.kind == "inline_equation") {
				out.WriteByte(' ')
			}
			continue
		}
		runes := []rune(p.text)
		hyphen := p.kind == "text" && p.last == 0 && len(runes) >= 2 && strings.ContainsRune("-\u00ad\u2010\u2011\u2043", runes[len(runes)-1]) && ((runes[len(runes)-2] >= 'a' && runes[len(runes)-2] <= 'z') || (runes[len(runes)-2] >= 'A' && runes[len(runes)-2] <= 'Z'))
		if hyphen {
			if p.line+1 < len(para.Lines) && len(para.Lines[p.line+1].Spans) > 0 {
				next := para.Lines[p.line+1].Spans[0]
				first := []rune(precisionNormalizeText(next.Content))
				if next.Type == "text" && len(first) > 0 && unicode.IsLower(first[0]) {
					runes = runes[:len(runes)-1]
				}
			}
			out.WriteString(string(runes))
			continue
		}
		out.WriteString(p.text)
		if following {
			out.WriteByte(' ')
		}
	}
	text := out.String()
	if para.Type == "text" {
		if at := precisionBlockPrefix.FindStringSubmatchIndex(text); at != nil {
			text = text[:at[4]] + "\\" + text[at[4]:]
		}
	}
	return text, nil
}
func precisionRenderMatches(want string, para precisionBlock) bool {
	// The archive does not retain detect_lang's result. These are its only two
	// formatting branches, not arbitrary whitespace edits. Both consume every
	// ordered typed span, and matching requires exact full-string equality.
	for _, cjk := range []bool{false, true} {
		got, e := precisionRender(para, cjk)
		if e == nil && got == want {
			return true
		}
	}
	return false
}
func precisionAssetStrings(want []string, para precisionBlock, kind string) bool {
	children := []precisionBlock{}
	for _, child := range para.Blocks {
		if child.Type == kind {
			children = append(children, child)
		}
	}
	if len(want) != len(children) {
		return false
	}
	for i, child := range children {
		if !precisionRenderMatches(want[i], child) {
			return false
		}
	}
	return true
}
func precisionFormattedTable(body string) (string, error) {
	// Embedded image prefix rewriting depends on a producer bucket path not
	// retained in this contract. Reject it instead of inventing that association.
	for _, m := range precisionHTMLImage.FindAllStringSubmatch(body, -1) {
		if !strings.HasPrefix(m[1], "data:") {
			return "", precisionError("unsupported table media prefix")
		}
	}
	return precisionEQ.ReplaceAllStringFunc(body, func(match string) string {
		m := precisionEQ.FindStringSubmatch(match)
		return " $" + html.UnescapeString(m[1]) + "$ "
	}), nil
}
func precisionItemType(item, para string) bool {
	if item == "text" {
		return para == "text" || para == "title"
	}
	if item == "equation" || item == "formula" {
		return para == "interline_equation"
	}
	return item == para
}
func verifyPrecisionPayloads(items []contentItem, pages []precisionPage) error {
	type placed struct {
		page  int
		size  []float64
		block precisionBlock
	}
	nodes := []placed{}
	for _, p := range pages {
		if e := walkPrecision(append(append([]precisionBlock(nil), p.Paras...), p.Discarded...), 0, func(b precisionBlock) error { nodes = append(nodes, placed{*p.Index, p.Size, b}); return nil }); e != nil {
			return e
		}
	}
	for i, item := range items {
		matches := []precisionBlock{}
		for _, node := range nodes {
			if node.page != *item.Page || !precisionItemType(item.Type, node.block.Type) || len(item.BBox) != 4 {
				continue
			}
			box, e := precisionBox(node.block.BBox, node.size)
			if e != nil {
				continue
			}
			same := true
			for k, v := range box {
				if math.Abs(v*1000-item.BBox[k]) > 1.001 {
					same = false
				}
			}
			if same {
				matches = append(matches, node.block)
			}
		}
		bad := func(reason string) error {
			return precisionError(fmt.Sprintf("canonical payload %d (%s): %s", i, item.Type, reason))
		}
		if len(matches) != 1 {
			return bad("no unique layout association")
		}
		para := matches[0]
		if item.Latex != "" {
			return bad("unsupported producer latex override")
		}
		switch item.Type {
		case "table", "image", "chart":
			if item.Text != "" {
				return bad("unexpected asset text")
			}
		default:
			if (item.Text != "" || len(para.Lines) > 0) && !precisionRenderMatches(item.Text, para) {
				return bad("text does not equal complete producer render")
			}
		}
		if item.Type == "table" {
			bodies := []string{}
			for _, child := range para.Blocks {
				if child.Type == "table_body" {
					for _, line := range child.Lines {
						for _, span := range line.Spans {
							if span.Type == "table" && span.HTML != "" {
								bodies = append(bodies, span.HTML)
							}
						}
					}
				}
			}
			if len(bodies) == 0 && precisionDeletedTable(para) && item.Body == "" && precisionAssetStrings(item.Captions, para, "table_caption") && precisionAssetStrings(item.Footnotes, para, "table_footnote") {
				continue // association to exactly one surviving parent is proved separately
			}
			if len(bodies) != 1 {
				return bad("table requires one complete body")
			}
			body, e := precisionFormattedTable(bodies[0])
			if e != nil {
				return e
			}
			if body != item.Body {
				return bad("table HTML differs from verified layout")
			}
			if !precisionAssetStrings(item.Captions, para, "table_caption") || !precisionAssetStrings(item.Footnotes, para, "table_footnote") {
				return bad("table caption or footnote differs from complete producer render")
			}
		}
		if item.Type == "image" && (!precisionAssetStrings(item.ImageCaptions, para, "image_caption") || !precisionAssetStrings(item.ImageFootnotes, para, "image_footnote")) {
			return bad("image caption or footnote differs from complete producer render")
		}
	}
	return nil
}
