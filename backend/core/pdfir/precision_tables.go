package pdfir

import (
	"encoding/json"
	"io"
	"math"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// SourceTableFragment retains the original producer HTML, complete safe rows,
// physical geometry, and (for a deleted continuation) its immutable block ID.
// OmittedPrefixRows describes producer merging, never deletion of source rows.
type SourceTableFragment struct {
	HTML                string     `json:"html"`
	Rows                []TableRow `json:"rows"`
	BBox                []float64  `json:"bbox"`
	PageSize            []float64  `json:"page_size"`
	ContinuationBlockID string     `json:"continuation_block_id,omitempty"`
	OmittedPrefixRows   int        `json:"omitted_prefix_rows,omitempty"`
}

// ParseSourceTable rejects any representation that parseTable would repair,
// omit or truncate. The raw HTML is retained and never rendered as trusted HTML.
func ParseSourceTable(body string) ([]TableRow, string, error) {
	bad := func() ([]TableRow, string, error) {
		return nil, "", precisionError("unsupported or incomplete table HTML")
	}
	if len(body) == 0 || len(body) > maxTableHTML {
		return bad()
	}
	z := html.NewTokenizer(strings.NewReader(body))
	stack := []string{}
	tables, rows, cells := 0, 0, 0
	allowed := map[string]bool{"table": true, "tbody": true, "thead": true, "tfoot": true, "tr": true, "td": true, "th": true, "b": true, "strong": true, "i": true, "em": true, "sup": true, "sub": true, "span": true, "br": true, "eq": true}
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF || len(stack) != 0 {
				return bad()
			}
			goto done
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			tag := tok.Data
			if !allowed[tag] {
				return bad()
			}
			if tag == "table" {
				tables++
				if tables != 1 || len(stack) != 0 {
					return bad()
				}
			}
			if len(stack) == 0 && tag != "table" {
				return bad()
			}
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			if tag == "tr" {
				if parent != "table" && parent != "tbody" && parent != "thead" && parent != "tfoot" {
					return bad()
				}
				rows++
			}
			if tag == "td" || tag == "th" {
				if parent != "tr" {
					return bad()
				}
				cells++
			}
			for _, a := range tok.Attr {
				if a.Key != "colspan" && a.Key != "rowspan" && a.Key != "style" && a.Key != "align" && a.Key != "valign" && a.Key != "width" && a.Key != "height" && a.Key != "border" && a.Key != "class" {
					return bad()
				}
			}
			if tag != "br" {
				stack = append(stack, tag)
				if len(stack) > 64 {
					return bad()
				}
			}
		case html.EndTagToken:
			tag := z.Token().Data
			if len(stack) == 0 || stack[len(stack)-1] != tag {
				return bad()
			}
			stack = stack[:len(stack)-1]
		case html.TextToken:
			if strings.TrimSpace(string(z.Text())) != "" {
				inside := false
				for _, x := range stack {
					inside = inside || x == "td" || x == "th"
				}
				if !inside {
					return bad()
				}
			}
		default:
			return bad()
		}
	}
done:
	t := parseTable(body)
	if tables != 1 || len(t.Warnings) > 0 || len(t.Rows) != rows {
		return bad()
	}
	n := 0
	lines := []string{}
	for _, r := range t.Rows {
		parts := []string{}
		for _, c := range r.Cells {
			n++
			parts = append(parts, c.Text)
		}
		lines = append(lines, strings.Join(parts, " | "))
	}
	if n != cells {
		return bad()
	}
	text := strings.Join(lines, "\n")
	if len([]rune(text)) > 262144 {
		return bad()
	}
	return t.Rows, text, nil
}

func precisionDeletedTable(b precisionBlock) bool {
	if b.Type != "table" || len(b.Blocks) == 0 {
		return false
	}
	body := false
	for _, c := range b.Blocks {
		if !c.LinesDeleted || len(c.Lines) != 0 || len(c.Blocks) != 0 {
			return false
		}
		body = body || c.Type == "table_body"
	}
	return body
}
func precisionTableParent(base Document, p precisionPage, b precisionBlock) (Block, error) {
	box, e := precisionBox(b.BBox, p.Size)
	if e != nil {
		return Block{}, e
	}
	found := []Block{}
	for _, x := range base.Blocks {
		if x.Type != "table" || x.Page != *p.Index || len(x.BBox) != 4 {
			continue
		}
		same := true
		for i, v := range box {
			same = same && math.Abs(v-x.BBox[i]) <= .001001
		}
		if same {
			found = append(found, x)
		}
	}
	if len(found) != 1 {
		return Block{}, precisionError("ambiguous table/parent association")
	}
	return found[0], nil
}

// Pinned MinerU 3.4.4 table_merge.detect_table_headers normalizes fullwidth
// ASCII and removes whitespace, scanning at most five prefix rows. We support
// its structural branch only. Every omitted raw row is still in the fragment.
func producerPrefix(a, b []TableRow) int {
	normalize := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, precisionNormalizeText(s))
	}
	n := 0
	for n < 5 && n < len(a) && n < len(b) {
		if len(a[n].Cells) != len(b[n].Cells) {
			break
		}
		same := true
		for k, x := range a[n].Cells {
			y := b[n].Cells[k]
			same = same && x.ColSpan == y.ColSpan && x.RowSpan == y.RowSpan && normalize(x.Text) == normalize(y.Text)
		}
		if !same {
			break
		}
		n++
	}
	// Match bounded producer rowspan expansion, rejecting prefixes beyond five
	// rows rather than dropping arbitrary leading source rows.
	for i := 0; i < n; i++ {
		for _, c := range b[i].Cells {
			if i+c.RowSpan > n {
				n = i + c.RowSpan
			}
		}
		if n > len(b) || n > 5 {
			return -1
		}
	}
	return n
}

// The supported producer table grammar is flat. Reject every recognized
// payload channel we cannot consume; indexing nested spans alone is not proof
// that the frozen projection preserves them.
func validateFlatPrecisionTable(b precisionBlock) error {
	if b.Type != "table" || len(b.Lines) != 0 {
		return precisionError("unsupported table ancestor payload")
	}
	for _, c := range b.Blocks {
		if e := validateFlatPrecisionTableChild(c); e != nil {
			return e
		}
	}
	return nil
}
func validateFlatPrecisionTableChild(c precisionBlock) error {
	if len(c.Blocks) != 0 {
		return precisionError("unsupported nested table child payload")
	}
	if c.Type != "table_body" && c.Type != "table_caption" && c.Type != "table_footnote" {
		return precisionError("unsupported table child")
	}
	for _, l := range c.Lines {
		for _, s := range l.Spans {
			if c.Type == "table_body" {
				if s.Type != "table" || s.Content != "" {
					return precisionError("unsupported typed table body payload")
				}
			} else if (s.Type != "text" && s.Type != "inline_equation") || s.HTML != "" {
				return precisionError("unsupported typed table text payload")
			}
		}
	}
	return nil
}

type physicalTable struct {
	parent   Block
	para     precisionBlock
	page     precisionPage
	span     precisionSpan
	rows     []TableRow
	text     string
	deleted  bool
	physical *precisionBlock
}

func projectPrecisionTables(base Document, pages []precisionPage, index map[string]map[int]int) ([]SourceRegion, map[string]bool, error) {
	all := []physicalTable{}
	seen := map[string]bool{}
	for _, p := range pages {
		err := walkPrecision(p.Paras, 0, func(b precisionBlock) error {
			if b.Type != "table" {
				return nil
			}
			if e := validateFlatPrecisionTable(b); e != nil {
				return e
			}
			parent, e := precisionTableParent(base, p, b)
			if e != nil {
				return e
			}
			if seen[parent.ID] {
				return precisionError("duplicate table parent")
			}
			seen[parent.ID] = true
			deleted := precisionDeletedTable(b)
			bodies := []precisionBlock{}
			for _, c := range b.Blocks {
				if c.Type == "table_body" {
					bodies = append(bodies, c)
				}
				for _, v := range c.CellMerge {
					if v != 0 {
						return precisionError("unsupported cross-page cell merge")
					}
				}
			}
			for _, v := range b.CellMerge {
				if v != 0 {
					return precisionError("unsupported cross-page cell merge")
				}
			}
			if len(bodies) != 1 {
				return precisionError("table requires one physical body")
			}
			candidates := []precisionSpan{}
			ancestors := []precisionBlock{}
			e = walkPrecision(p.Preproc, 0, func(c precisionBlock) error {
				if c.Type == "table" && reflect.DeepEqual(c.BBox, b.BBox) {
					ancestors = append(ancestors, c)
				}
				for _, l := range c.Lines {
					for _, s := range l.Spans {
						if s.Type == "table" && reflect.DeepEqual(s.BBox, bodies[0].BBox) {
							if e := validateFlatPrecisionTableChild(c); e != nil {
								return e
							}
							candidates = append(candidates, s)
						}
					}
				}
				return nil
			})
			if e != nil {
				return e
			}
			if len(candidates) != 1 {
				return precisionError("ambiguous physical table body")
			}
			s := candidates[0]
			rows, text, e := ParseSourceTable(s.HTML)
			if e != nil {
				return e
			}
			var physical *precisionBlock
			if len(ancestors) > 1 {
				return precisionError("ambiguous physical table ancestor")
			}
			if len(ancestors) == 1 {
				physical = &ancestors[0]
				if e := validateFlatPrecisionTable(*physical); e != nil {
					return e
				}
				count := 0
				for _, child := range physical.Blocks {
					if child.Type != "table_body" {
						continue
					}
					if len(child.Blocks) != 0 {
						return precisionError("nested physical table body")
					}
					for _, line := range child.Lines {
						for _, span := range line.Spans {
							if span.Type != "table" || precisionSpanKey(span) != precisionSpanKey(s) {
								return precisionError("physical table body ancestry mismatch")
							}
							count++
						}
					}
				}
				if count != 1 {
					return precisionError("missing physical table body ancestor")
				}
			}
			all = append(all, physicalTable{parent: parent, para: b, page: p, span: s, rows: rows, text: text, deleted: deleted, physical: physical})
			return errPrecisionSkip
		})
		if err != nil {
			return nil, nil, err
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].parent.Page == all[j].parent.Page {
			return all[i].parent.ID < all[j].parent.ID
		}
		return all[i].parent.Page < all[j].parent.Page
	})
	regions := []SourceRegion{}
	used := map[string]bool{}
	add := func(r SourceRegion) {
		data, _ := json.Marshal([]any{SourcePrecisionVersion, r.ParentBlockID, len(regions), r})
		r.ID = "region_" + Digest(string(data))
		regions = append(regions, r)
	}
	for _, home := range all {
		if home.deleted {
			continue
		}
		mergedHTML := ""
		for _, c := range home.para.Blocks {
			if c.Type == "table_body" {
				for _, l := range c.Lines {
					for _, s := range l.Spans {
						if s.Type == "table" {
							mergedHTML = s.HTML
						}
					}
				}
			}
		}
		merged, _, e := ParseSourceTable(mergedHTML)
		if e != nil {
			return nil, nil, e
		}
		if len(home.rows) > len(merged) || !reflect.DeepEqual(home.rows, merged[:len(home.rows)]) {
			return nil, nil, precisionError("home table rows differ from merged prefix")
		}
		type choice struct{ idx, skip int }
		solutions := [][]choice{}
		steps := 0
		var search func(int, int, []choice) error
		search = func(offset, last int, chain []choice) error {
			steps++
			if steps > 20000 {
				return precisionError("table association search limit")
			}
			if offset == len(merged) {
				solutions = append(solutions, append([]choice(nil), chain...))
				return nil
			}
			if len(solutions) > 1 {
				return nil
			}
			for i, c := range all {
				if !c.deleted || c.parent.Page <= last {
					continue
				}
				skip := producerPrefix(home.rows, c.rows)
				if skip < 0 || skip >= len(c.rows) {
					continue
				}
				tail := c.rows[skip:]
				if offset+len(tail) <= len(merged) && reflect.DeepEqual(tail, merged[offset:offset+len(tail)]) {
					if e := search(offset+len(tail), c.parent.Page, append(chain, choice{i, skip})); e != nil {
						return e
					}
				}
			}
			return nil
		}
		if e = search(len(home.rows), home.parent.Page, nil); e != nil {
			return nil, nil, e
		}
		if len(solutions) != 1 {
			return nil, nil, precisionError("no unique complete physical table chain")
		}
		fragments := []physicalTable{home}
		skips := []int{0}
		for _, c := range solutions[0] {
			f := all[c.idx]
			if used[f.parent.ID] {
				return nil, nil, precisionError("continuation claimed by multiple parents")
			}
			used[f.parent.ID] = true
			fragments = append(fragments, f)
			skips = append(skips, c.skip)
		}
		for i, f := range fragments {
			box, e := precisionBox(f.span.BBox, f.page.Size)
			if e != nil {
				return nil, nil, e
			}
			payload := &SourceTableFragment{HTML: f.span.HTML, Rows: f.rows, BBox: box, PageSize: f.page.Size, OmittedPrefixRows: skips[i]}
			if f.deleted {
				payload.ContinuationBlockID = f.parent.ID
			}
			add(SourceRegion{Kind: "table_body_fragment", ParentBlockID: home.parent.ID, Page: f.parent.Page, Text: f.text, Table: payload})
		}
		grouped := 0
		for _, f := range fragments {
			if f.physical != nil {
				grouped++
			}
		}
		// Falling back for just one unwrapped fragment would discard proven
		// original notes/captions from the other physical ancestors.
		if grouped != 0 && grouped != len(fragments) {
			return nil, nil, precisionError("mixed physical table ancestry")
		}
		if grouped == len(fragments) {
			ancillary, err := physicalTableText(home, fragments)
			if err != nil {
				return nil, nil, err
			}
			for _, r := range ancillary {
				add(r)
			}
			continue
		}
		// Captions and footnotes remain typed text and must have unique exact physical
		// provenance regardless of whether the producer supplied cross_page flags.
		for _, c := range home.para.Blocks {
			if c.Type == "table_body" {
				continue
			}
			if c.Type != "table_caption" && c.Type != "table_footnote" {
				return nil, nil, precisionError("unsupported table child")
			}
			kind := "table_footnote"
			if c.Type == "table_caption" {
				kind = "table_identity"
			}
			rs := []SourceRegion{}
			for _, l := range c.Lines {
				for _, s := range l.Spans {
					if s.Type != "text" && s.Type != "inline_equation" {
						return nil, nil, precisionError("unsupported table text span")
					}
					page := -1
					candidates := index[precisionSpanKey(s)]
					if kind == "table_identity" && candidates[home.parent.Page] == 1 {
						candidates = map[int]int{home.parent.Page: 1}
					}
					for p, n := range candidates {
						if n != 1 || page != -1 {
							return nil, nil, precisionError("ambiguous table text physical page")
						}
						page = p
					}
					if page < 0 {
						return nil, nil, precisionError("missing physical table text")
					}
					var size []float64
					for _, p := range pages {
						if *p.Index == page {
							size = p.Size
						}
					}
					box, e := precisionBox(s.BBox, size)
					if e != nil {
						return nil, nil, e
					}
					if kind == "table_identity" && page != home.parent.Page {
						return nil, nil, precisionError("caption outside table start page")
					}
					if len(rs) == 0 || rs[len(rs)-1].Page != page {
						rs = append(rs, SourceRegion{Kind: kind, ParentBlockID: home.parent.ID, Page: page, Spans: []SourceSpan{}})
					}
					r := &rs[len(rs)-1]
					r.Spans = append(r.Spans, SourceSpan{Type: s.Type, Content: s.Content, BBox: box})
					content := s.Content
					if s.Type == "inline_equation" {
						content = "$" + content + "$"
					}
					if r.Text != "" {
						r.Text += "\n"
					}
					r.Text += content
				}
			}
			for _, r := range rs {
				add(r)
			}
		}
	}
	for _, f := range all {
		if f.deleted && !used[f.parent.ID] {
			return nil, nil, precisionError("unassociated deleted continuation table")
		}
	}
	return regions, seen, nil
}

// Full preproc table ancestry preserves captions/notes dropped by producer
// merging. Canonical para text must be a complete exact member of this proven
// parent chain; membership does not authorize attaching unrelated page text.
func physicalTableText(home physicalTable, fragments []physicalTable) ([]SourceRegion, error) {
	type placed struct {
		span precisionSpan
		page int
		kind string
	}
	physical := map[string][]placed{}
	regions := []SourceRegion{}
	for _, f := range fragments {
		for _, c := range f.physical.Blocks {
			if c.Type == "table_body" {
				continue
			}
			if c.Type != "table_caption" && c.Type != "table_footnote" {
				return nil, precisionError("unsupported physical table child")
			}
			kind := c.Type
			if kind == "table_caption" && f.parent.Page == home.parent.Page {
				kind = "table_identity"
			}
			r := SourceRegion{Kind: kind, ParentBlockID: home.parent.ID, Page: f.parent.Page, Spans: []SourceSpan{}}
			for _, l := range c.Lines {
				for _, s := range l.Spans {
					if s.Type != "text" && s.Type != "inline_equation" {
						return nil, precisionError("unsupported physical table text")
					}
					box, e := precisionBox(s.BBox, f.page.Size)
					if e != nil {
						return nil, e
					}
					physical[precisionSpanKey(s)] = append(physical[precisionSpanKey(s)], placed{s, f.parent.Page, c.Type})
					r.Spans = append(r.Spans, SourceSpan{Type: s.Type, Content: s.Content, BBox: box})
					text := s.Content
					if s.Type == "inline_equation" {
						text = "$" + text + "$"
					}
					if r.Text != "" {
						r.Text += "\n"
					}
					r.Text += text
				}
			}
			if len(r.Spans) > 0 {
				regions = append(regions, r)
			}
		}
	}
	claimed := map[string]int{}
	for _, c := range home.para.Blocks {
		if c.Type == "table_body" {
			continue
		}
		if c.Type != "table_caption" && c.Type != "table_footnote" {
			return nil, precisionError("unsupported table child")
		}
		for _, l := range c.Lines {
			for _, s := range l.Spans {
				matches := []placed{}
				for _, p := range physical[precisionSpanKey(s)] {
					if p.kind == c.Type && (c.Type != "table_caption" || p.page == home.parent.Page) {
						matches = append(matches, p)
					}
				}
				if len(matches) != 1 {
					return nil, precisionError("table text lacks unique physical ancestor")
				}
				key := precisionSpanKey(s)
				claimed[key]++
				if claimed[key] > 1 {
					return nil, precisionError("duplicated table ancillary span")
				}
			}
		}
	}
	return regions, nil
}

// ValidateTableFragments reconstructs the immutable canonical merged cell view
// from frozen physical fragments. It also checks prefix and placeholder claims
// when a snapshot is loaded without the original producer archive.
func ValidateTableFragments(parent Block, regions []SourceRegion, blocks []Block) error {
	fragments := []SourceRegion{}
	for _, r := range regions {
		if r.Kind == "table_body_fragment" {
			fragments = append(fragments, r)
		}
	}
	if len(fragments) == 0 || parent.Table == nil {
		return precisionError("missing complete table fragments")
	}
	merged := []TableRow{}
	var first []TableRow
	previous := -1
	for i, r := range fragments {
		f := r.Table
		if f == nil || len(f.PageSize) != 2 || !validPhysicalSize(f.PageSize) || r.Page <= previous {
			return precisionError("invalid fragment page or size")
		}
		previous = r.Page
		if i == 0 {
			if r.Page != parent.Page || f.ContinuationBlockID != "" || f.OmittedPrefixRows != 0 {
				return precisionError("missing table start fragment")
			}
			first = f.Rows
		} else {
			if f.ContinuationBlockID == "" || f.OmittedPrefixRows != producerPrefix(first, f.Rows) {
				return precisionError("unproven continuation prefix")
			}
			found := false
			for _, b := range blocks {
				if b.ID == f.ContinuationBlockID {
					found = b.Type == "table" && b.Page == r.Page && b.Text == "" && b.Table != nil && len(b.Table.Rows) == 0 && strings.Join(b.Table.Captions, "") == "" && strings.Join(b.Table.Footnotes, "") == ""
				}
			}
			if !found {
				return precisionError("unproven continuation placeholder")
			}
		}
		body, e := precisionFormattedTable(f.HTML)
		if e != nil {
			return e
		}
		rows, _, e := ParseSourceTable(body)
		if e != nil {
			return e
		}
		if f.OmittedPrefixRows < 0 || (f.OmittedPrefixRows > 0 && f.OmittedPrefixRows >= len(rows)) {
			return precisionError("invalid omitted prefix")
		}
		merged = append(merged, rows[f.OmittedPrefixRows:]...)
	}
	if !reflect.DeepEqual(merged, parent.Table.Rows) {
		return precisionError("canonical table cells not completely covered")
	}
	return nil
}
func validPhysicalSize(s []float64) bool {
	for _, v := range s {
		if !(v > 0 && v <= 1e6) {
			return false
		}
	}
	return true
}
