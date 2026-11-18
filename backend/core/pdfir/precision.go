package pdfir

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"path"
	"reflect"
	"strings"
)

const SourcePrecisionVersion = 3

// SourcePrecision is a separately cached, immutable view. It never changes the
// canonical blocks used by notes and ordinary reading. Regions bind to frozen
// producer spans, NOT to guessed offsets in a rendered parent paragraph.
type SourcePrecision struct {
	// Nil means no containment proof. A present (possibly empty) set binds the
	// page-only restrictions to this immutable projection and its IR digest.
	PageOnlyBlockIDs *[]string      `json:"page_only_block_ids,omitempty"`
	Version          int            `json:"version"`
	SourceSHA256     string         `json:"source_sha256"`
	BaseIRDigest     string         `json:"base_ir_digest"`
	ArchiveDigest    string         `json:"archive_digest"`
	Regions          []SourceRegion `json:"regions"`
}
type SourceRegion struct {
	Kind          string               `json:"kind,omitempty"`
	Table         *SourceTableFragment `json:"table,omitempty"`
	ID            string               `json:"id"`
	ParentBlockID string               `json:"parent_block_id"`
	Page          int                  `json:"page"`
	Text          string               `json:"text"`
	Spans         []SourceSpan         `json:"spans"`
}
type SourceSpan struct {
	Type    string    `json:"type"`
	Content string    `json:"content"`
	BBox    []float64 `json:"bbox"`
}
type precisionSpan struct {
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	HTML      string    `json:"html"`
	BBox      []float64 `json:"bbox"`
	CrossPage bool      `json:"cross_page"`
}
type precisionBlock struct {
	LinesDeleted bool             `json:"lines_deleted"`
	CellMerge    []int            `json:"cell_merge"`
	Type         string           `json:"type"`
	BBox         []float64        `json:"bbox"`
	Angle        float64          `json:"angle"`
	Blocks       []precisionBlock `json:"blocks"`
	Lines        []struct {
		Spans []precisionSpan `json:"spans"`
	} `json:"lines"`
}
type precisionPage struct {
	Index     *int             `json:"page_idx"`
	Size      []float64        `json:"page_size"`
	Preproc   []precisionBlock `json:"preproc_blocks"`
	Discarded []precisionBlock `json:"discarded_blocks"`
	Paras     []precisionBlock `json:"para_blocks"`
}

func precisionError(reason string) error {
	return fmt.Errorf("source precision unavailable: %s", reason)
}
func precisionTextType(kind string) bool {
	return kind == "text" || kind == "title" || kind == "list" || kind == "ref_text"
}
func precisionSpanKey(s precisionSpan) string {
	b, _ := json.Marshal([]any{s.Type, s.Content, s.HTML, s.BBox})
	return string(b)
}
func precisionBox(box, size []float64) ([]float64, error) {
	if len(box) != 4 || len(size) != 2 || box[0] >= box[2] || box[1] >= box[3] {
		return nil, precisionError("invalid geometry")
	}
	out := make([]float64, 4)
	for i, v := range box {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > size[i%2] {
			return nil, precisionError("invalid geometry")
		}
		out[i] = v / size[i%2]
	}
	return out, nil
}

// The walker uses SkipDir to skip the children of a verified table.
var errPrecisionSkip = fs.SkipDir

func walkPrecision(bs []precisionBlock, depth int, visit func(precisionBlock) error) error {
	if depth > 16 {
		return precisionError("layout depth limit")
	}
	for _, b := range bs {
		if err := visit(b); err != nil {
			if err == errPrecisionSkip {
				continue
			}
			return err
		}
		if err := walkPrecision(b.Blocks, depth+1, visit); err != nil {
			return err
		}
	}
	return nil
}
func readPrecisionLayout(raw []byte) ([]byte, error) {
	if int64(len(raw)) > MaxArchiveBytes {
		return nil, precisionError("archive size limit")
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	if len(z.File) > 4096 {
		return nil, precisionError("archive member limit")
	}
	var layout []byte
	count := 0
	for _, f := range z.File {
		if path.IsAbs(f.Name) || strings.Contains(f.Name, "\\") || strings.Contains("/"+f.Name, "/../") {
			return nil, precisionError("unsafe member")
		}
		if path.Base(f.Name) != "layout.json" && !strings.HasSuffix(f.Name, "_middle.json") {
			continue
		}
		count++
		if count > 1 {
			return nil, precisionError("ambiguous layout")
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		layout, e = bounded(r, maxJSONBytes)
		r.Close()
		if e != nil {
			return nil, e
		}
	}
	if count != 1 {
		return nil, precisionError("missing retained layout")
	}
	return layout, nil
}

// ProjectSourcePrecision uses only a retained source-matched producer archive.
// It has no parser/network dependency and either covers all merged text or fails.
func ProjectSourcePrecision(base Document, raw []byte) (Document, error) {
	return projectSourcePrecision(base, raw, nil)
}

// ProjectGuideSourcePrecision obtains figure proofs from the same archive
// verification pass. Tables with ambiguous caption locations are omitted from the guide.
func ProjectGuideSourcePrecision(base Document, raw []byte) (Document, []OriginalFigureProof, error) {
	proofs := []OriginalFigureProof{}
	d, err := projectSourcePrecision(base, raw, &proofs)
	if err != nil {
		return Document{}, nil, err
	}
	return d, uniqueOriginalFigureProofs(proofs, base.Blocks), nil
}

func projectSourcePrecision(base Document, raw []byte, figures *[]OriginalFigureProof) (Document, error) {
	fail := func(e error) (Document, error) { return Document{}, e }
	if base.Precision != nil {
		return fail(precisionError("expected base IR"))
	}
	layout, err := readPrecisionLayout(raw)
	if err != nil {
		return fail(err)
	}
	original, err := AdaptZIP(raw, base.SourceSHA256, base.Parser.Model)
	if err != nil {
		return fail(err)
	}
	if len(base.Blocks) != len(original.Blocks) {
		return fail(precisionError("base block count mismatch"))
	}
	// Bind every field consumed by the catalog, including canonical table rows,
	// image/table captions and footnotes. Metadata is not a substitute for payload proof.
	for i, b := range original.Blocks {
		other := base.Blocks[i]
		if !reflect.DeepEqual(b, other) {
			return fail(precisionError("base/archive association mismatch"))
		}
	}
	var l struct {
		Pages   []precisionPage `json:"pdf_info"`
		Backend string          `json:"_backend"`
		Version string          `json:"_version_name"`
	}
	if json.Unmarshal(layout, &l) != nil || len(l.Pages) == 0 || len(l.Pages) > 10000 {
		return fail(precisionError("invalid layout pages"))
	}
	if l.Version != precisionProducerVersion || (l.Backend != "hybrid" && l.Backend != "vlm") {
		return fail(precisionError("unsupported producer contract"))
	}
	pages := map[int]precisionPage{}
	index := map[string]map[int]int{}
	spanCount := 0
	for _, p := range l.Pages {
		if p.Index == nil || *p.Index < 0 || *p.Index > 100000 || len(p.Size) != 2 || p.Preproc == nil || p.Paras == nil {
			return fail(precisionError("invalid physical page"))
		}
		if _, ok := pages[*p.Index]; ok {
			return fail(precisionError("duplicate physical page"))
		}
		for _, v := range p.Size {
			if !(v > 0 && v <= 1e6) {
				return fail(precisionError("invalid page size"))
			}
		}
		pages[*p.Index] = p
		err = walkPrecision(append(append([]precisionBlock(nil), p.Preproc...), p.Discarded...), 0, func(b precisionBlock) error {
			for _, line := range b.Lines {
				for _, s := range line.Spans {
					spanCount++
					if spanCount > 200000 {
						return precisionError("span count limit")
					}
					if s.Type != "text" && s.Type != "inline_equation" && s.Type != "interline_equation" && s.Type != "table" {
						continue
					}
					if _, e := precisionBox(s.BBox, p.Size); e != nil {
						return e
					}
					k := precisionSpanKey(s)
					if index[k] == nil {
						index[k] = map[int]int{}
					}
					index[k][*p.Index]++
				}
			}
			return nil
		})
		if err != nil {
			return fail(err)
		}
	}
	items, e := precisionContentItems(raw)
	if e != nil {
		return fail(e)
	}
	covered := make([]bool, len(items))
	var collectFigure func(int, contentItem, precisionBlock, []float64)
	if figures != nil {
		collectFigure = func(i int, item contentItem, parent precisionBlock, size []float64) {
			if item.Type == "image" || item.Type == "chart" {
				*figures = append(*figures, proveOriginalFigure(base.Blocks[i], item, parent, size))
			}
		}
	}
	if e = verifyPrecisionPayloads(items, l.Pages, covered, collectFigure); e != nil {
		return fail(e)
	}
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return fail(err)
	}
	projection := &SourcePrecision{Version: SourcePrecisionVersion, SourceSHA256: base.SourceSHA256, BaseIRDigest: Digest(string(baseJSON)), ArchiveDigest: Digest(string(raw)), Regions: []SourceRegion{}}
	var omitted map[string]bool
	if figures != nil {
		omitted = make(map[string]bool)
	}
	tableRegions, tableParents, err := projectPrecisionTables(base, l.Pages, index, omitted)
	if err != nil {
		return fail(err)
	}
	projection.Regions = append(projection.Regions, tableRegions...)
	claimed := map[string]map[int]int{}
	parents := map[string]bool{}
	for _, p := range l.Pages {
		err = walkPrecision(append(append([]precisionBlock(nil), p.Paras...), p.Discarded...), 0, func(para precisionBlock) error {
			if para.Type == "table" {
				return errPrecisionSkip // tables are proved as complete physical chains below
			}
			spans := []precisionSpan{}
			for _, line := range para.Lines {
				spans = append(spans, line.Spans...)
			}
			if !precisionTextType(para.Type) {
				for _, s := range spans {
					if s.CrossPage {
						return precisionError("unsupported cross-page block")
					}
					if s.Type == "text" || s.Type == "inline_equation" || s.Type == "interline_equation" || s.Type == "table" {
						if index[precisionSpanKey(s)][*p.Index] == 0 {
							return precisionError("asset or ancillary text has no proven home page")
						}
					} else if s.Content != "" || s.HTML != "" {
						return precisionError("unsupported typed ancillary payload")
					}
				}
				return nil
			}
			if len(spans) == 0 {
				// MinerU retains empty merge_prev placeholders. A nonempty canonical
				// parent still must be covered by the association check below.
				return nil
			}
			if para.Angle != 0 {
				return precisionError("rotated text paragraph")
			}
			norm, e := precisionBox(para.BBox, p.Size)
			if e != nil {
				return e
			}
			candidates := []Block{}
			for _, b := range base.Blocks {
				if b.Page != *p.Index || !precisionTextType(b.Type) || len(b.BBox) != 4 {
					continue
				}
				match := true
				for i, v := range norm {
					if math.Abs(v-b.BBox[i]) > .001001 {
						match = false
					}
				}
				if match {
					candidates = append(candidates, b)
				}
			}
			if len(candidates) != 1 {
				return precisionError("ambiguous paragraph/parent association")
			}
			parent := candidates[0]
			if parents[parent.ID] {
				return precisionError("duplicate paragraph association")
			}
			parents[parent.ID] = true
			regions := []SourceRegion{}
			cross := false
			for _, s := range spans {
				if s.Type != "text" && s.Type != "inline_equation" {
					return precisionError("unsupported text span type")
				}
				k := precisionSpanKey(s)
				page := -1
				for candidate, n := range index[k] {
					if n > 0 && (candidate != *p.Index) == s.CrossPage {
						if page != -1 {
							return precisionError("ambiguous physical span")
						}
						page = candidate
					}
				}
				if page < 0 {
					return precisionError("span has no proven physical page")
				}
				if claimed[k] == nil {
					claimed[k] = map[int]int{}
				}
				claimed[k][page]++
				if claimed[k][page] > index[k][page] {
					return precisionError("duplicated physical text span")
				}
				box, e := precisionBox(s.BBox, pages[page].Size)
				if e != nil {
					return e
				}
				cross = cross || page != *p.Index
				if len(regions) == 0 || regions[len(regions)-1].Page != page {
					regions = append(regions, SourceRegion{Kind: "body_text", ParentBlockID: parent.ID, Page: page, Spans: []SourceSpan{}})
				}
				r := &regions[len(regions)-1]
				r.Spans = append(r.Spans, SourceSpan{Type: s.Type, Content: s.Content, BBox: box})
				// This is a frozen rendering of typed raw spans. Newlines preserve original
				// breaks, including hyphens; no claim of parent-rune equivalence is made.
				content := s.Content
				if s.Type == "inline_equation" {
					content = "$" + content + "$"
				}
				if r.Text != "" {
					r.Text += "\n"
				}
				r.Text += content
			}
			if cross {
				for i := range regions {
					b, _ := json.Marshal([]any{SourcePrecisionVersion, parent.ID, i, regions[i]})
					regions[i].ID = "region_" + Digest(string(b))
					projection.Regions = append(projection.Regions, regions[i])
				}
			}
			return nil
		})
		if err != nil {
			return fail(err)
		}
	}
	// Every canonical body text must be associated, including unflagged
	// paragraphs: a missing flag cannot hide a continuation behind the old page.
	for _, b := range base.Blocks {
		if b.Type == "table" && !tableParents[b.ID] {
			return fail(precisionError("unassociated table"))
		}
		if precisionTextType(b.Type) && b.Text != "" && !parents[b.ID] {
			return fail(precisionError("unassociated body text"))
		}
	}
	// Only fully verified text can authorize the original box. Appended caption
	// blocks have no canonical content-item proof and stay conservatively page-only.
	out := base
	if len(omitted) > 0 {
		// Drop dedicated captions with their table. Keep ordinary paragraphs
		// that mention it and keep the shared reader IR intact.
		captions := map[string]bool{}
		for _, b := range base.Blocks {
			if omitted[b.ID] && b.Table != nil {
				for _, link := range b.Table.RelatedBlocks {
					if link.Relation == "caption" {
						captions[link.BlockID] = true
					}
				}
			}
		}
		out.Blocks = make([]Block, 0, len(base.Blocks))
		for _, b := range base.Blocks {
			if b.Type == "table_caption" && captions[b.ID] {
				omitted[b.ID] = true
			}
			if !omitted[b.ID] {
				out.Blocks = append(out.Blocks, b)
			}
		}
	}
	pageOnly := []string{}
	for i, b := range base.Blocks {
		if omitted[b.ID] {
			continue
		}
		unsafe := (b.Text != "" || b.Latex != "") && (i >= len(covered) || !covered[i])
		if b.Image != nil && strings.Join(append(append([]string{}, b.Image.Captions...), b.Image.Footnotes...), "") != "" {
			unsafe = true // a body crop is not a proof for associated image text
		}
		if b.Type == "table" {
			unsafe = false // empty identity retains the existing precise-body contract
			for _, r := range tableRegions {
				if r.ParentBlockID != b.ID || r.Kind != "table_identity" {
					continue
				}
				for _, span := range r.Spans {
					if r.Page != b.Page || !precisionContains(b.BBox, span.BBox) {
						unsafe = true
					}
				}
			}
		}
		if unsafe {
			pageOnly = append(pageOnly, b.ID)
		}
	}
	projection.PageOnlyBlockIDs = &pageOnly
	out.Precision = projection
	out.Pages = append([]Page(nil), base.Pages...)
	for _, p := range l.Pages {
		found := false
		for _, q := range out.Pages {
			if q.Index == *p.Index {
				found = true
			}
		}
		if !found {
			out.Pages = append(out.Pages, Page{Index: *p.Index, Width: p.Size[0], Height: p.Size[1]})
		}
	}
	if len(projection.Regions) > 20000 {
		return fail(precisionError("region limit"))
	}
	return out, nil
}

// MinerU's content-list coordinates are integer thousandths. The existing
// association contract allows one unit of that quantization, never column gaps.
func precisionContains(parent, span []float64) bool {
	if len(parent) != 4 || len(span) != 4 {
		return false
	}
	const rounding = .001001
	return span[0] >= parent[0]-rounding && span[1] >= parent[1]-rounding && span[2] <= parent[2]+rounding && span[3] <= parent[3]+rounding
}
