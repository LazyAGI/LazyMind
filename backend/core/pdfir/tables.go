package pdfir

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const ProjectionVersion = 3
const maxTableHTML = 2 << 20

type TableCell struct {
	Text    string `json:"text"`
	RowSpan int    `json:"row_span"`
	ColSpan int    `json:"col_span"`
	Header  bool   `json:"header,omitempty"`
}
type TableRow struct {
	Cells []TableCell `json:"cells"`
}
type TableRelation struct {
	BlockID  string `json:"block_id"`
	Relation string `json:"relation"`
}
type Table struct {
	Rows          []TableRow      `json:"rows"`
	Captions      []string        `json:"captions"`
	Footnotes     []string        `json:"footnotes"`
	Number        string          `json:"number,omitempty"`
	RelatedBlocks []TableRelation `json:"related_blocks"`
	Warnings      []string        `json:"warnings,omitempty"`
}
type contentItem struct {
	ImageCaptions  []string  `json:"image_caption"`
	ImageFootnotes []string  `json:"image_footnote"`
	Type           string    `json:"type"`
	Page           *int      `json:"page_idx"`
	BBox           []float64 `json:"bbox"`
	Text           string    `json:"text"`
	Latex          string    `json:"latex"`
	Body           string    `json:"table_body"`
	Captions       []string  `json:"table_caption"`
	Footnotes      []string  `json:"table_footnote"`
}

func tableWarning(t *Table, s string) {
	for _, w := range t.Warnings {
		if w == s {
			return
		}
	}
	t.Warnings = append(t.Warnings, s)
}
func nodeText(n *html.Node, depth int) string {
	if depth > 128 {
		return ""
	}
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "iframe", "object", "svg", "template":
			return ""
		case "br":
			return " "
		}
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c, depth+1))
	}
	return b.String()
}
func plainTableText(s string) string {
	if len(s) > maxTableHTML {
		s = s[:maxTableHTML]
	}
	n, e := html.Parse(strings.NewReader(s))
	if e != nil {
		return ""
	}
	return strings.Join(strings.Fields(nodeText(n, 0)), " ")
}
func parseTable(body string) *Table {
	t := &Table{Rows: []TableRow{}, Captions: []string{}, Footnotes: []string{}, RelatedBlocks: []TableRelation{}}
	if len(body) > maxTableHTML {
		tableWarning(t, "Table exceeds structured projection limit")
		return t
	}
	root, e := html.Parse(strings.NewReader(body))
	if e != nil {
		tableWarning(t, "Table HTML could not be parsed")
		return t
	}
	var table *html.Node
	var find func(*html.Node, int)
	find = func(n *html.Node, depth int) {
		if depth > 128 || table != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "table" {
			table = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c, depth+1)
		}
	}
	find(root, 0)
	if table == nil {
		tableWarning(t, "Table has no structured rows")
		return t
	}
	total := 0
	var walk func(*html.Node, int)
	walk = func(n *html.Node, depth int) {
		if depth > 128 {
			return
		}
		if n != table && n.Type == html.ElementNode && n.Data == "table" {
			tableWarning(t, "Nested table omitted")
			return
		}
		if n.Type == html.ElementNode && n.Data == "tr" {
			if len(t.Rows) >= 1000 {
				tableWarning(t, "Table exceeds structured projection limit")
				return
			}
			row := TableRow{Cells: []TableCell{}}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type != html.ElementNode || (c.Data != "td" && c.Data != "th") {
					continue
				}
				if len(row.Cells) >= 100 || total >= 200000 {
					tableWarning(t, "Table exceeds structured projection limit")
					break
				}
				text := strings.Join(strings.Fields(nodeText(c, 0)), " ")
				if len(text) > 200000-total {
					text = string([]rune(text)[:min(len([]rune(text)), (200000-total)/4)])
					tableWarning(t, "Table exceeds structured projection limit")
				}
				total += len(text)
				cell := TableCell{Text: text, RowSpan: 1, ColSpan: 1, Header: c.Data == "th"}
				for _, a := range c.Attr {
					if a.Key != "rowspan" && a.Key != "colspan" {
						continue
					}
					v, err := strconv.Atoi(a.Val)
					if err != nil || v < 1 || v > 100 {
						tableWarning(t, "Invalid or oversized table span bounded")
						v = max(1, min(v, 100))
					}
					if a.Key == "rowspan" {
						cell.RowSpan = v
					} else {
						cell.ColSpan = v
					}
				}
				row.Cells = append(row.Cells, cell)
			}
			t.Rows = append(t.Rows, row)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, depth+1)
		}
	}
	walk(table, 0)
	// Bound the effective grid, not just each individual span, so adversarial
	// spans cannot expand a small HTML input into an enormous browser table.
	occupied := [100]int{}
	for i := range t.Rows {
		cells := make([]TableCell, 0, len(t.Rows[i].Cells))
		col := 0
		for _, c := range t.Rows[i].Cells {
			for col < len(occupied) && occupied[col] > 0 {
				col++
			}
			if col == len(occupied) {
				tableWarning(t, "Table exceeds structured projection limit")
				break
			}
			if c.RowSpan > len(t.Rows)-i {
				c.RowSpan = len(t.Rows) - i
				tableWarning(t, "Invalid or oversized table span bounded")
			}
			available := 0
			for col+available < len(occupied) && occupied[col+available] == 0 {
				available++
			}
			if c.ColSpan > available {
				c.ColSpan = available
				tableWarning(t, "Invalid or oversized table span bounded")
			}
			for k := col; k < col+c.ColSpan; k++ {
				occupied[k] = c.RowSpan
			}
			col += c.ColSpan
			cells = append(cells, c)
		}
		t.Rows[i].Cells = cells
		for k := range occupied {
			if occupied[k] > 0 {
				occupied[k]--
			}
		}
	}

	return t
}

var tableLabel = regexp.MustCompile(`(?i)(?:\btable\s+|\btab\.\s*|表\s*)([A-Z]?\d+(?:\.\d+)*[a-z]?)\b`)

func captionNumber(s string) string {
	loc := tableLabel.FindStringSubmatchIndex(s)
	if loc == nil || strings.TrimSpace(s[:loc[0]]) != "" {
		return ""
	}
	return strings.ToUpper(s[loc[2]:loc[3]])
}

type layoutBlock struct {
	Type   string        `json:"type"`
	BBox   []float64     `json:"bbox"`
	Blocks []layoutBlock `json:"blocks"`
	Lines  []struct {
		Spans []struct {
			Content string `json:"content"`
		} `json:"spans"`
	} `json:"lines"`
}
type tableCaption struct {
	page int
	box  []float64
	text string
}

func layoutCaptions(raw []byte) []tableCaption { return layoutCaptionsOfType(raw, "table_caption") }
func layoutCaptionsOfType(raw []byte, kind string) []tableCaption {
	var layout struct {
		Pages []struct {
			Index  *int          `json:"page_idx"`
			Size   []float64     `json:"page_size"`
			Blocks []layoutBlock `json:"para_blocks"`
		} `json:"pdf_info"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &layout) != nil {
		return nil
	}
	var out []tableCaption
	for _, p := range layout.Pages {
		if p.Index == nil || *p.Index < 0 || *p.Index > 100000 || len(p.Size) != 2 || p.Size[0] <= 0 || p.Size[1] <= 0 {
			continue
		}
		var walk func(layoutBlock, int)
		walk = func(b layoutBlock, depth int) {
			if depth > 32 || len(out) > 10000 {
				return
			}
			if b.Type == kind && len(b.BBox) == 4 {
				box := make([]float64, 4)
				valid := true
				for i, v := range b.BBox {
					box[i] = v / p.Size[i%2]
					valid = valid && !math.IsNaN(box[i]) && !math.IsInf(box[i], 0) && box[i] >= 0 && box[i] <= 1
				}
				if valid && box[0] < box[2] && box[1] < box[3] {
					var lines []string
					for _, l := range b.Lines {
						var spans []string
						for _, s := range l.Spans {
							spans = append(spans, s.Content)
						}
						lines = append(lines, strings.Join(spans, " "))
					}
					text := plainTableText(strings.Join(lines, " "))
					if text != "" {
						out = append(out, tableCaption{*p.Index, box, text})
					}
				}
			}
			for _, c := range b.Blocks {
				walk(c, depth+1)
			}
		}
		for _, b := range p.Blocks {
			walk(b, 0)
		}
	}
	return out
}

// Candidate captions must share most of their horizontal extent with a nearby
// table body. Direction is learned only from unambiguous geometric anchors.
func captionCandidates(d *Document, c tableCaption, side int) []int {
	return captionCandidatesOfType(d, c, side, "table")
}
func captionCandidatesOfType(d *Document, c tableCaption, side int, kind string) []int {
	var out []int
	for i, b := range d.Blocks {
		if b.Type != kind || b.Page != c.page || len(b.BBox) != 4 {
			continue
		}
		box := b.BBox
		overlap := math.Min(box[2], c.box[2]) - math.Max(box[0], c.box[0])
		if overlap < .7*math.Max(box[2]-box[0], c.box[2]-c.box[0]) {
			continue
		}
		gap := c.box[1] - box[3]
		if side < 0 {
			gap = box[1] - c.box[3]
		}
		if gap >= 0 && gap <= .04 {
			out = append(out, i)
		}
	}
	return out
}
func projectTables(d *Document, items []contentItem, layout []byte) {
	d.ProjectionVersion = ProjectionVersion
	for i, v := range items {
		if v.Type != "table" {
			continue
		}
		t := parseTable(v.Body)
		d.Blocks[i].Table = t
		for _, s := range v.Footnotes {
			t.Footnotes = append(t.Footnotes, plainTableText(s))
		}
	}
	captions := layoutCaptions(layout)
	direction := 0
	conflict := false
	for _, c := range captions {
		below, above := captionCandidates(d, c, 1), captionCandidates(d, c, -1)
		side := 0
		if len(below) == 1 && len(above) == 0 {
			side = 1
		}
		if len(above) == 1 && len(below) == 0 {
			side = -1
		}
		if side != 0 {
			if direction != 0 && direction != side {
				conflict = true
			}
			direction = side
		}
	}
	assignments := map[int][]tableCaption{}
	if !conflict && direction != 0 {
		for _, c := range captions {
			matches := captionCandidates(d, c, direction)
			if len(matches) == 1 {
				assignments[matches[0]] = append(assignments[matches[0]], c)
			}
		}
	}
	tablesPerPage := map[int]int{}
	for _, b := range d.Blocks {
		if b.Table != nil {
			tablesPerPage[b.Page]++
		}
	}
	originalCount := len(d.Blocks)
	for i := 0; i < originalCount; i++ {
		b := &d.Blocks[i]
		if b.Table == nil {
			continue
		}
		t := b.Table
		if cs := assignments[i]; len(cs) == 1 && captionNumber(cs[0].text) != "" {
			c := cs[0]
			t.Captions = []string{c.text}
			t.Number = captionNumber(c.text)
			id := Digest(fmt.Sprintf("%s:%s:table-caption:%d:%v:%s", d.SourceSHA256, Revision, c.page, c.box, c.text))[:24]
			t.RelatedBlocks = append(t.RelatedBlocks, TableRelation{id, "caption"})
			d.Blocks = append(d.Blocks, Block{ID: id, Type: "table_caption", Page: c.page, BBox: c.box, Text: c.text})
		} else if len(captions) == 0 && tablesPerPage[b.Page] == 1 && len(items[i].Captions) == 1 {
			t.Captions = []string{plainTableText(items[i].Captions[0])}
			t.Number = captionNumber(t.Captions[0])
		} else {
			tableWarning(t, "Caption association is ambiguous or unavailable")
		}
	}
	numbers := map[string]int{}
	for _, b := range d.Blocks {
		if b.Table != nil && b.Table.Number != "" {
			numbers[b.Table.Number]++
		}
	}
	for i := 0; i < originalCount; i++ {
		t := d.Blocks[i].Table
		if t == nil || t.Number == "" {
			continue
		}
		if numbers[t.Number] != 1 {
			tableWarning(t, "Table number is not unique; paragraph links omitted")
			continue
		}
		for _, b := range d.Blocks[:originalCount] {
			if b.Table != nil || b.Type == "formula" || b.Type == "table_caption" {
				continue
			}
			if len(t.Captions) == 1 && plainTableText(b.Text) == t.Captions[0] {
				t.RelatedBlocks = append(t.RelatedBlocks, TableRelation{b.ID, "caption"})
				continue
			}
			for _, m := range tableLabel.FindAllStringSubmatch(b.Text, -1) {
				if strings.ToUpper(m[1]) == t.Number {
					t.RelatedBlocks = append(t.RelatedBlocks, TableRelation{b.ID, "mention"})
					break
				}
			}
		}
	}
}
