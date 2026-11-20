package pdfir

import (
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

const maxCellMergeColumns = 100 // same bound as the safe table projection

func allCellMergeColumns(marker []int) bool {
	if len(marker) == 0 || len(marker) > maxCellMergeColumns {
		return false
	}
	for _, flag := range marker {
		if flag != 1 {
			return false
		}
	}
	return true
}

// The supported MinerU 3.4.4 seam has the same all-one marker on both
// continuation parents and their sole bodies. Zero-only metadata keeps the
// ordinary table path. Any other nonzero marker must be explained here.
func tableCellMergeMarker(para precisionBlock, physical *precisionBlock, physicalBody precisionBlock, deleted bool) ([]int, error) {
	marked := false
	for _, flag := range physicalBody.CellMerge {
		marked = marked || flag != 0
	}
	for _, table := range []*precisionBlock{&para, physical} {
		if table == nil {
			continue
		}
		for _, flag := range table.CellMerge {
			marked = marked || flag != 0
		}
		for _, child := range table.Blocks {
			for _, flag := range child.CellMerge {
				marked = marked || flag != 0
			}
		}
	}
	if !marked {
		return nil, nil
	}
	want := para.CellMerge
	bad := precisionError("unsupported cross-page cell merge markers")
	if !deleted || !allCellMergeColumns(want) || physical == nil || !reflect.DeepEqual(want, physical.CellMerge) {
		return nil, bad
	}
	for _, table := range []precisionBlock{para, *physical} {
		for _, child := range table.Blocks {
			if child.Type == "table_body" && !reflect.DeepEqual(want, child.CellMerge) ||
				child.Type != "table_body" && len(child.CellMerge) != 0 {
				return nil, bad
			}
		}
	}
	return want, nil
}

// MinerU considers only the previous page's last and the current page's first
// top-level para blocks. An otherwise identical table on either page cannot
// claim the continuation by matching text.
func physicalCellMergeChains(tables []physicalTable) (map[string]int, error) {
	homeByPage := map[int]physicalTable{}
	for _, table := range tables {
		paras := table.page.Paras
		if !table.deleted && len(paras) > 0 && isTablePara(table, paras[len(paras)-1]) {
			homeByPage[table.parent.Page] = table
		}
	}
	chains := map[string]int{}
	for i, next := range tables {
		if len(next.cellMerge) == 0 {
			continue
		}
		home, found := homeByPage[next.parent.Page-1]
		if !found || next.pageOrder != home.pageOrder+1 || len(next.page.Paras) == 0 || !isTablePara(next, next.page.Paras[0]) {
			return nil, precisionError("cell merge lacks a unique adjacent table at the page boundary")
		}
		if _, exists := chains[home.parent.ID]; exists {
			return nil, precisionError("cell merge has multiple continuations at the same page boundary")
		}
		for _, table := range []physicalTable{home, next} {
			if err := validatePhysicalCellMergeTable(table); err != nil {
				return nil, err
			}
		}
		chains[home.parent.ID] = i
	}
	return chains, nil
}

func isTablePara(table physicalTable, para precisionBlock) bool {
	return para.Type == "table" && reflect.DeepEqual(table.para.BBox, para.BBox)
}

func validatePhysicalCellMergeTable(table physicalTable) error {
	if table.physical == nil || table.physical.LinesDeleted || table.span.CrossPage {
		return precisionError("cell merge lacks physical table ancestry")
	}
	// The caller already proved one canonical body and one physical span.
	// Also bind that span to the sole undeleted body in the raw table wrapper.
	var paraBody, rawBody precisionBlock
	for _, child := range table.para.Blocks {
		if child.Type == "table_body" {
			paraBody = child
		}
	}
	count := 0
	for _, child := range table.physical.Blocks {
		if child.Type == "table_body" {
			rawBody = child
			count++
		}
	}
	box, err := precisionBox(table.span.BBox, table.page.Size)
	if err != nil {
		return err
	}
	if count != 1 || rawBody.LinesDeleted || !reflect.DeepEqual(rawBody.BBox, paraBody.BBox) ||
		!reflect.DeepEqual(rawBody.BBox, table.span.BBox) || !precisionContains(table.parent.BBox, box) {
		return precisionError("unproven cell merge fragment geometry or body ancestry")
	}
	return nil
}

// Scan visual slots including inherited rowspans. The seam must be owned by
// its own unit cells and cover the same width as the complete physical grid.
func cellMergeGrid(rows []TableRow, seam int) (int, error) {
	occupied := [maxCellMergeColumns]int{}
	width := 0
	for i, row := range rows {
		if i == seam {
			for _, n := range occupied {
				if n != 0 {
					return 0, precisionError("cell merge requires unit cells with no inherited seam slots")
				}
			}
		}
		col := 0
		for _, c := range row.Cells {
			if i == seam && (c.Header || c.RowSpan != 1 || c.ColSpan != 1) {
				return 0, precisionError("cell merge requires unit cells on the seam")
			}
			for col < len(occupied) && occupied[col] != 0 {
				col++
			}
			if c.RowSpan <= 0 || i+c.RowSpan > len(rows) || c.ColSpan <= 0 || col+c.ColSpan > len(occupied) {
				return 0, precisionError("invalid cell merge grid")
			}
			for k := col; k < col+c.ColSpan; k++ {
				if occupied[k] != 0 {
					return 0, precisionError("overlapping cell merge grid")
				}
				occupied[k] = c.RowSpan
			}
			col += c.ColSpan
			width = max(width, col)
		}
		for k, n := range occupied {
			if n > 0 {
				occupied[k]--
			}
		}
	}
	return width, nil
}

// The input has already passed ParseSourceTable. Read the seam again as
// untrimmed decoded text so a separator found in the source survives the
// producer's concatenation. Markup in this particular row is unsupported.
func plainCellMergeRow(body string, target int) ([]string, error) {
	z := html.NewTokenizer(strings.NewReader(body))
	row := -1
	inHeader, inCell := false, false
	var cell strings.Builder
	text := []string{}
	bad := precisionError("cell merge requires a plain-text seam")
	for {
		kind := z.Next()
		switch kind {
		case html.ErrorToken:
			return nil, bad
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			switch {
			case token.Data == "thead":
				inHeader = true
			case token.Data == "tr":
				row++
				if row == target && (inHeader || kind != html.StartTagToken) {
					return nil, bad
				}
			case row == target:
				if token.Data != "td" || inCell || kind != html.StartTagToken {
					return nil, bad
				}
				spans := map[string]bool{}
				for _, a := range token.Attr {
					if a.Key == "colspan" || a.Key == "rowspan" {
						n, err := strconv.Atoi(a.Val)
						if err != nil || n != 1 || spans[a.Key] {
							return nil, bad
						}
						spans[a.Key] = true
					}
				}
				inCell = true
			}
		case html.EndTagToken:
			tag := z.Token().Data
			if tag == "thead" {
				inHeader = false
			}
			if row == target {
				switch {
				case tag == "td" && inCell:
					text = append(text, cell.String())
					cell.Reset()
					inCell = false
				case tag == "tr" && !inCell:
					return text, nil
				default:
					return nil, bad
				}
			}
		case html.TextToken:
			if row == target && inCell {
				cell.Write(z.Text())
			}
		}
	}
}

func cellMergeRepeatedHeader(a, b TableRow) bool {
	// A matching first-row text signature is required by both the producer's
	// structural and visual header checks. Decline both forms here.
	normalize := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) {
				return -1
			}
			if r >= 0xff01 && r <= 0xff5e {
				return r - 0xfee0
			}
			return r
		}, s)
	}
	if len(a.Cells) != len(b.Cells) {
		return false
	}
	for i := range a.Cells {
		if normalize(a.Cells[i].Text) != normalize(b.Cells[i].Text) {
			return false
		}
	}
	return true
}

// Replay only the all-one branch of MinerU's table_merge._apply_cell_merge.
// Rows outside the seam remain intact. The temporary merged rows are never
// retained as physical table evidence or used to manufacture coordinates.
func replayTableCellMerge(homeHTML string, home []TableRow, nextHTML string, next []TableRow, marker []int) ([]TableRow, error) {
	if !allCellMergeColumns(marker) {
		return nil, precisionError("unsupported cross-page cell merge markers")
	}
	if len(home) == 0 || len(next) == 0 {
		return nil, precisionError("missing cell merge seam")
	}
	i := len(home) - 1
	homeCols, err := cellMergeGrid(home, i)
	if err != nil {
		return nil, err
	}
	nextCols, err := cellMergeGrid(next, 0)
	if err != nil {
		return nil, err
	}
	if homeCols != len(marker) || nextCols != len(marker) || len(home[i].Cells) != homeCols || len(next[0].Cells) != nextCols {
		return nil, precisionError("cell merge visual columns do not match the marker")
	}
	if cellMergeRepeatedHeader(home[0], next[0]) {
		return nil, precisionError("cell merge cannot skip a repeated header")
	}
	a, err := plainCellMergeRow(homeHTML, i)
	if err != nil {
		return nil, err
	}
	b, err := plainCellMergeRow(nextHTML, 0)
	if err != nil {
		return nil, err
	}
	if len(a) != len(marker) || len(b) != len(marker) {
		return nil, precisionError("cell merge visual columns lack explicit text cells")
	}
	joined := append([]TableCell(nil), home[i].Cells...)
	hasContinuation := false
	for col := range marker {
		hasContinuation = hasContinuation || strings.TrimSpace(b[col]) != ""
		joined[col].Text = strings.Join(strings.Fields(a[col]+b[col]), " ")
	}
	if !hasContinuation {
		return nil, precisionError("cell merge row has no continuation text")
	}
	rows := make([]TableRow, 0, len(home)+len(next)-1)
	rows = append(rows, home[:i]...)
	rows = append(rows, TableRow{Cells: joined})
	return append(rows, next[1:]...), nil
}
