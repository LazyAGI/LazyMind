package paperguide

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"lazymind/core/pdfir"
)

var ErrInvalidSource = errors.New("invalid paper guide source")
var ErrInvalidGuide = errors.New("invalid paper guide output")

// BuildSourceSnapshot binds the evidence catalog to the exact PDF, IR and
// server-owned scope. Ownership checks happen before constructing the snapshot.
func BuildSourceSnapshot(d pdfir.Document, identity SourceIdentity) (GuideSourceSnapshot, error) {
	return buildSourceSnapshot(d, identity, SourceCatalogVersion)
}

// BuildSourceSnapshotWithFigureProofs consumes the independent proof returned
// alongside the verified v3 document by pdfir.ProjectGuideSourcePrecision.
func BuildSourceSnapshotWithFigureProofs(d pdfir.Document, identity SourceIdentity, proofs []pdfir.OriginalFigureProof) (GuideSourceSnapshot, error) {
	return buildSourceSnapshot(d, identity, SourceCatalogVersion, proofs...)
}

// Empty catalogVersion is the original frozen catalog. Keep its construction
// rules intact so existing guides and exact selections remain verifiable.
func buildSourceSnapshot(d pdfir.Document, identity SourceIdentity, catalogVersion string, proofs ...pdfir.OriginalFigureProof) (GuideSourceSnapshot, error) {
	if catalogVersion != "" && catalogVersion != "paper-guide-source-v1" && catalogVersion != "paper-guide-source-v2" && catalogVersion != "paper-guide-source-v3" && catalogVersion != "paper-guide-source-v4" && catalogVersion != sourceCatalogV5 && catalogVersion != SourceCatalogVersion {
		return GuideSourceSnapshot{}, fmt.Errorf("%w: unknown catalog version", ErrInvalidSource)
	}
	v3Precision := catalogVersion == sourceCatalogV5 || catalogVersion == SourceCatalogVersion
	current := v3Precision || catalogVersion == "paper-guide-source-v4" || catalogVersion == "paper-guide-source-v2" || catalogVersion == "paper-guide-source-v3"
	var s GuideSourceSnapshot
	if !validSHA(identity.SourceSHA256) || identity.SourceSHA256 != d.SourceSHA256 ||
		identity.OwnerID == "" || identity.DocumentID == "" || identity.DatasetID == "" {
		return s, fmt.Errorf("%w: source identity", ErrInvalidSource)
	}
	data, err := json.Marshal(d)
	if err != nil {
		return s, fmt.Errorf("%w: invalid IR", ErrInvalidSource)
	}
	var frozen pdfir.Document
	if err := json.Unmarshal(data, &frozen); err != nil {
		return s, fmt.Errorf("%w: invalid IR", ErrInvalidSource)
	}
	s = GuideSourceSnapshot{
		Precision: frozen.Precision, CatalogVersion: catalogVersion, Identity: identity, IRDigest: pdfir.Digest(string(data)), Parser: d.Parser, IRVersion: d.Version,
		ProjectionVersion: d.ProjectionVersion, CoordinateSpace: d.CoordinateSpace,
		Pages: frozen.Pages, Blocks: frozen.Blocks, Assets: []GuideAsset{}, Sections: []SourceSection{}, Evidence: []GuideEvidence{},
		Coverage: GuideCoverage{Warnings: append([]string{}, d.Warnings...), PageWarnings: []int{}},
	}
	if current {
		s.SourceWarnings = append([]string(nil), d.Warnings...)
	}
	pages := make(map[int]bool, len(s.Pages))
	for _, p := range s.Pages {
		if p.Index < 0 || p.Index > 100000 || pages[p.Index] {
			return GuideSourceSnapshot{}, fmt.Errorf("%w: page index", ErrInvalidSource)
		}
		pages[p.Index] = true
	}
	pageOnly := map[string]bool{}
	regions := map[string][]pdfir.SourceRegion{}
	continuations := map[string]string{}
	if frozen.Precision != nil {
		p := frozen.Precision
		if !((v3Precision && p.Version == pdfir.SourcePrecisionVersion) || (catalogVersion == "paper-guide-source-v3" && p.Version == 1) || (catalogVersion == "paper-guide-source-v4" && p.Version == 2)) || p.SourceSHA256 != d.SourceSHA256 || !validSHA(p.BaseIRDigest) || !validSHA(p.ArchiveDigest) {
			return GuideSourceSnapshot{}, fmt.Errorf("%w: precision contract", ErrInvalidSource)
		}
		if p.PageOnlyBlockIDs != nil {
			if !v3Precision {
				return GuideSourceSnapshot{}, fmt.Errorf("%w: historical containment metadata", ErrInvalidSource)
			}
			for _, id := range *p.PageOnlyBlockIDs {
				if id == "" || pageOnly[id] {
					return GuideSourceSnapshot{}, fmt.Errorf("%w: page-only block ID", ErrInvalidSource)
				}
				pageOnly[id] = true
			}
		}
		regionIDs := map[string]bool{}
		for _, r := range p.Regions {
			if r.ID == "" || regionIDs[r.ID] || !pages[r.Page] {
				return GuideSourceSnapshot{}, fmt.Errorf("%w: precision region", ErrInvalidSource)
			}
			regionIDs[r.ID] = true
			if r.Kind == "table_body_fragment" {
				if (!v3Precision && catalogVersion != "paper-guide-source-v4") || r.Table == nil || len(r.Spans) != 0 || !validBBox(r.Table.BBox) || len(r.Table.PageSize) != 2 {
					return GuideSourceSnapshot{}, fmt.Errorf("%w: table fragment payload", ErrInvalidSource)
				}
				rows, text, err := pdfir.ParseSourceTable(r.Table.HTML)
				if err != nil || !reflect.DeepEqual(rows, r.Table.Rows) || text != r.Text || r.Table.OmittedPrefixRows < 0 || (r.Table.OmittedPrefixRows > 0 && r.Table.OmittedPrefixRows >= len(rows)) {
					return GuideSourceSnapshot{}, fmt.Errorf("%w: table fragment rendering", ErrInvalidSource)
				}
				if r.Table.ContinuationBlockID != "" {
					if continuations[r.Table.ContinuationBlockID] != "" {
						return GuideSourceSnapshot{}, fmt.Errorf("%w: duplicate continuation", ErrInvalidSource)
					}
					continuations[r.Table.ContinuationBlockID] = r.ParentBlockID
				}
				regions[r.ParentBlockID] = append(regions[r.ParentBlockID], r)
				continue
			}
			if r.Table != nil || len(r.Spans) == 0 || (r.Kind != "" && r.Kind != "body_text" && r.Kind != "table_footnote" && r.Kind != "table_caption" && r.Kind != "table_identity") || (catalogVersion == "paper-guide-source-v3" && r.Kind != "") {
				return GuideSourceSnapshot{}, fmt.Errorf("%w: region kind", ErrInvalidSource)
			}
			parts := []string{}
			for _, span := range r.Spans {
				if !validBBox(span.BBox) || (span.Type != "text" && span.Type != "inline_equation") {
					return GuideSourceSnapshot{}, fmt.Errorf("%w: precision span", ErrInvalidSource)
				}
				content := span.Content
				if span.Type == "inline_equation" {
					content = "$" + content + "$"
				}
				parts = append(parts, content)
			}
			if strings.Join(parts, "\n") != r.Text {
				return GuideSourceSnapshot{}, fmt.Errorf("%w: precision text", ErrInvalidSource)
			}
			regions[r.ParentBlockID] = append(regions[r.ParentBlockID], r)
		}
	}
	seen := make(map[string]bool, len(s.Blocks))
	var original func(pdfir.Block, string, bool) *GuideOriginalDisplay
	if catalogVersion == SourceCatalogVersion {
		var err error
		original, err = originalDisplayBuilder(frozen, proofs)
		if err != nil {
			return GuideSourceSnapshot{}, err
		}
		if len(proofs) > 0 {
			s.FigureProofs = proofs
			s.FigureProofDigest = digest([]any{identity, s.IRDigest, proofs})
		}
	}
	warnings := map[int]bool{}
	section := -1
	hasAbstract := false
	for _, b := range s.Blocks {
		if b.ID == "" || seen[b.ID] || !pages[b.Page] {
			return GuideSourceSnapshot{}, fmt.Errorf("%w: block or page ID", ErrInvalidSource)
		}
		seen[b.ID] = true
		if parent, ok := continuations[b.ID]; ok {
			if parent == b.ID || b.Type != "table" || b.Text != "" || b.Table == nil || len(b.Table.Rows) != 0 || strings.Join(b.Table.Captions, "") != "" || strings.Join(b.Table.Footnotes, "") != "" {
				return GuideSourceSnapshot{}, fmt.Errorf("%w: nonempty continuation", ErrInvalidSource)
			}
			delete(continuations, b.ID)
			continue
		}
		if frozen.Precision != nil && frozen.Precision.Version >= 2 && b.Type == "table" {
			if err := pdfir.ValidateTableFragments(b, regions[b.ID], s.Blocks); err != nil {
				return GuideSourceSnapshot{}, fmt.Errorf("%w: %v", ErrInvalidSource, err)
			}
		}
		bbox := []float64(nil)
		if d.CoordinateSpace == "normalized_top_left" && validBBox(b.BBox) {
			bbox = append([]float64(nil), b.BBox...)
		} else {
			warnings[b.Page] = true
		}
		l := GuideLocator{Kind: "page", Page: b.Page}
		e := GuideEvidence{SourceBlockID: b.ID, SourceType: b.Type, Text: b.Text}
		var asset *GuideAsset
		switch {
		case b.Type == "formula" && (current || b.Latex != ""):
			asset = &GuideAsset{Kind: "formula"}
			if b.Latex != "" {
				l.Kind, l.Latex, l.BBox = "formula", b.Latex, bbox
				e.Text = b.Latex
			} else {
				asset.Warnings = []string{"Formula transcription is unavailable; only original text and page are readable."}
			}
		case current && (b.Type == "image" || b.Type == "chart") || b.Image != nil && b.Type == "image":
			if bbox != nil {
				l.Kind, l.BBox = "figure_region", bbox
			}
			asset = &GuideAsset{Kind: "figure"}
			if b.Image != nil {
				e.Text = joinSourceText(b.Text, b.Image.Captions, b.Image.Footnotes)
				asset.RelatedBlocks = b.Image.RelatedBlocks
				asset.Warnings = b.Image.Warnings
			}
			if current {
				asset.Warnings = append(append([]string{}, asset.Warnings...), "Figure pixels are not supplied to the guide model; captions and regions do not establish visual observations.")
			}
		case b.Type == "table" && (current || b.Table != nil):
			asset = &GuideAsset{Kind: "table"}
			if b.Table != nil {
				e.Text = joinSourceText(b.Text, b.Table.Captions, tableRows(b.Table), b.Table.Footnotes)
				// Whole-table locators need no row/column claim. If bounds are
				// unavailable, the page remains a truthful citation.
				if bbox != nil && len(b.Table.Warnings) == 0 && len(b.Table.Rows) != 0 {
					l.Kind, l.BBox = "table_cells", bbox
				}
				asset = &GuideAsset{Kind: "table", RelatedBlocks: b.Table.RelatedBlocks, Warnings: b.Table.Warnings}
			}
			if current && (b.Table == nil || len(b.Table.Rows) == 0) {
				asset.Warnings = append(append([]string{}, asset.Warnings...), "Parsed table cells are unavailable; do not infer values or cell positions.")
			}
		case b.Text != "" && bbox != nil:
			l.Kind, l.End, l.BBox = "text_span", len([]rune(b.Text)), bbox
		}
		if b.Type == "table" && len(regions[b.ID]) > 0 {
			e.Text = ""
			l = GuideLocator{Kind: "page", Page: b.Page}
			if bbox != nil {
				l.Kind = "table_cells"
				l.BBox = bbox
			}
			for _, r := range regions[b.ID] {
				if r.Kind == "table_identity" {
					if r.Page != b.Page {
						return GuideSourceSnapshot{}, fmt.Errorf("%w: identity page", ErrInvalidSource)
					}
					e.Text = joinSourceText(e.Text, []string{r.Text})
				}
			}
		}
		if v3Precision && (frozen.Precision == nil || frozen.Precision.PageOnlyBlockIDs == nil || pageOnly[b.ID]) {
			l = GuideLocator{Kind: "page", Page: b.Page}
		}
		e.Locator = l
		e.ID = s.sourceID("evidence", b.ID)
		if asset != nil {
			asset.ID, asset.BlockID, asset.Locator = e.ID, b.ID, l
			if original != nil && asset.Kind != "table" {
				asset.OriginalDisplay = original(b, asset.Kind, pageOnly[b.ID])
			}
			s.Assets = append(s.Assets, *asset)
			if len(asset.Warnings) != 0 {
				warnings[b.Page] = true
				if current {
					for _, warning := range asset.Warnings {
						if !contains(s.Coverage.Warnings, warning) {
							s.Coverage.Warnings = append(s.Coverage.Warnings, warning)
						}
					}
				}
			}
		}
		entries := []GuideEvidence{e}
		if rs := regions[b.ID]; len(rs) > 0 {
			if asset != nil && b.Type != "table" || asset == nil && b.Text == "" {
				return GuideSourceSnapshot{}, fmt.Errorf("%w: region parent", ErrInvalidSource)
			}
			if asset == nil {
				entries = nil
			}
			for _, r := range rs {
				if asset != nil {
					if r.Kind == "table_identity" {
						continue
					}
					if r.Kind != "table_body_fragment" && r.Kind != "table_footnote" && r.Kind != "table_caption" {
						return GuideSourceSnapshot{}, fmt.Errorf("%w: table region kind", ErrInvalidSource)
					}
				} else if r.Kind != "" && r.Kind != "body_text" {
					return GuideSourceSnapshot{}, fmt.Errorf("%w: body region kind", ErrInvalidSource)
				}
				entry := GuideEvidence{ID: s.sourceID("evidence_region", r.ID), SourceBlockID: b.ID, SourceRegionID: r.ID, SourceType: b.Type, Text: r.Text, Locator: GuideLocator{Kind: "page", Page: r.Page}}
				if asset != nil {
					entry.SourceType = "text"
					entry.ParentAssetID = e.ID
					entries[0].FragmentEvidenceIDs = append(entries[0].FragmentEvidenceIDs, entry.ID)
					s.Assets[len(s.Assets)-1].FragmentEvidenceIDs = append(s.Assets[len(s.Assets)-1].FragmentEvidenceIDs, entry.ID)
				}
				entries = append(entries, entry)
				warnings[r.Page] = true
			}
			delete(regions, b.ID)
		}
		s.Evidence = append(s.Evidence, entries...)
		title, kind := sourceHeading(b)
		if kind != "" || section == -1 || (s.Sections[section].Kind == "page_group" && s.Sections[section].Pages[0] != b.Page) {
			if kind == "" {
				title, kind = fmt.Sprintf("Page %d", b.Page+1), "page_group"
			}
			s.Sections = append(s.Sections, SourceSection{ID: s.sourceID("section", b.ID), Title: title, Kind: kind, Pages: []int{}, EvidenceIDs: []string{}})
			section++
		}
		current := &s.Sections[section]
		for _, entry := range entries {
			if !containsPage(current.Pages, entry.Locator.Page) {
				current.Pages = append(current.Pages, entry.Locator.Page)
			}
			current.EvidenceIDs = append(current.EvidenceIDs, entry.ID)
		}
		if kind == "abstract" && inlineAbstract.MatchString(strings.TrimSpace(b.Text)) {
			hasAbstract = true
		}
		if kind == "" && strings.TrimSpace(b.Text) != "" && (b.Type == "text" || b.Type == "reference" || b.Type == "list") {
			switch current.Kind {
			case "abstract":
				hasAbstract = true
			case "conclusion":
				s.Coverage.Conclusion = true
			case "limitations":
				s.Coverage.Limitations = true
			case "references":
				s.Coverage.References = true
			case "appendix":
				s.Coverage.Appendix = true
			case "section", "page_group":
				s.Coverage.Body = true
			}
		}
	}
	for id := range pageOnly {
		if !seen[id] {
			return GuideSourceSnapshot{}, fmt.Errorf("%w: unknown page-only block", ErrInvalidSource)
		}
	}
	if len(regions) > 0 || len(continuations) > 0 {
		return GuideSourceSnapshot{}, fmt.Errorf("%w: unknown region parent", ErrInvalidSource)
	}
	for _, p := range s.Pages {
		if warnings[p.Index] {
			s.Coverage.PageWarnings = append(s.Coverage.PageWarnings, p.Index)
		}
	}
	s.Coverage.AbstractOnly = hasAbstract && !s.Coverage.Body && !s.Coverage.Conclusion && !s.Coverage.Limitations && !s.Coverage.Appendix
	return s, nil
}

func validSHA(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 64 && err == nil && strings.ToLower(s) == s
}
func (s GuideSourceSnapshot) sourceID(kind string, block string) string {
	namespace := s.CatalogVersion
	if namespace == "" {
		namespace = legacySourceNamespace
	}
	tuple, _ := json.Marshal([]string{namespace, s.Identity.SourceSHA256, s.Identity.OwnerID, s.Identity.DatasetID, s.Identity.DocumentID, s.IRDigest, kind, block})
	return "ev_" + pdfir.Digest(string(tuple))
}
func validBBox(b []float64) bool {
	if len(b) != 4 || !(b[0] < b[2]) || !(b[1] < b[3]) {
		return false
	}
	for _, x := range b {
		if !(x >= 0 && x <= 1) {
			return false
		}
	}
	return true
}
func containsPage(ps []int, p int) bool {
	for _, q := range ps {
		if q == p {
			return true
		}
	}
	return false
}
func joinSourceText(text string, parts ...[]string) string {
	all := []string{}
	if text != "" {
		all = append(all, text)
	}
	for _, p := range parts {
		all = append(all, p...)
	}
	return strings.Join(all, "\n")
}
func tableRows(t *pdfir.Table) []string {
	rows := make([]string, 0, len(t.Rows))
	for _, r := range t.Rows {
		cells := make([]string, 0, len(r.Cells))
		for _, c := range r.Cells {
			cells = append(cells, c.Text)
		}
		rows = append(rows, strings.Join(cells, " | "))
	}
	return rows
}

var inlineAbstract = regexp.MustCompile(`(?i)^(?:abstract|摘要)\s*[:：.。—]\s*\S`)
var numberedHeading = regexp.MustCompile(`^(?:[0-9]+\.)*[0-9]+\.?\s+`)

func sourceHeading(b pdfir.Block) (string, string) {
	text := strings.TrimSpace(b.Text)
	if b.Type == "text" && inlineAbstract.MatchString(text) {
		if strings.HasPrefix(text, "摘要") {
			return "摘要", "abstract"
		}
		return "Abstract", "abstract"
	}
	if text == "" || len([]rune(text)) > 120 || strings.ContainsAny(text, "\r\n") {
		return "", ""
	}
	key := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(numberedHeading.ReplaceAllString(text, ""), ":")))
	// MinerU drops heading levels. Use exact, recognizable short headings,
	// or explicit headings from an IR that supplies them.
	if b.Type != "text" && b.Type != "title" && b.Type != "heading" {
		return "", ""
	}
	switch key {
	case "abstract", "摘要":
		return text, "abstract"
	case "conclusion", "conclusions", "结论", "結論":
		return text, "conclusion"
	case "limitations", "limitations and future work", "局限", "局限性":
		return text, "limitations"
	case "references", "bibliography", "参考文献", "參考文獻":
		return text, "references"
	case "appendix", "appendices", "附录", "附錄", "supplementary materials":
		return text, "appendix"
	}
	if strings.HasPrefix(key, "appendix ") && (b.Type != "text" || len([]rune(key)) <= 12) {
		return text, "appendix"
	}
	if b.Type == "title" || b.Type == "heading" || strings.Contains("|introduction|background|methods|results|discussion|引言|引論|背景|方法|结果|結果|讨论|討論|", "|"+key+"|") {
		return text, "section"
	}
	return "", ""
}

// MatchTextSpan requires an unambiguous match. Offsets count original Unicode
// code points; no case-folding, numerical canonicalization or symbol removal.
func MatchTextSpan(text, quote string) (int, int, error) {
	start, end, ambiguous, err := findExcerpt(text, quote)
	if ambiguous {
		return 0, 0, fmt.Errorf("%w: ambiguous excerpt", ErrInvalidGuide)
	}
	return start, end, err
}

func findExcerpt(text, quote string) (int, int, bool, error) {
	source, offsets := normalizeText(text)
	wanted, _ := normalizeText(quote)
	if wanted == "" {
		return 0, 0, false, fmt.Errorf("%w: empty excerpt", ErrInvalidGuide)
	}
	at := strings.Index(source, wanted)
	if at < 0 {
		return 0, 0, false, fmt.Errorf("%w: absent excerpt", ErrInvalidGuide)
	}
	start := len([]rune(source[:at]))
	end := start + len([]rune(wanted)) - 1
	return offsets[start][0], offsets[end][1], strings.Contains(source[at+1:], wanted), nil
}

func normalizeText(s string) (string, [][2]int) {
	r := []rune(s)
	var b strings.Builder
	spans := make([][2]int, 0, len(r))
	for i := 0; i < len(r); {
		// A soft hyphen explicitly marks a discretionary break. An ASCII
		// hyphen can instead be a scientific operator or part of a name.
		if r[i] == '\u00ad' {
			i++
			if i < len(r) && (r[i] == '\r' || r[i] == '\n') {
				for i < len(r) && unicode.IsSpace(r[i]) {
					i++
				}
			}
			continue
		}
		if unicode.IsSpace(r[i]) {
			start := i
			for i < len(r) && unicode.IsSpace(r[i]) {
				i++
			}
			if len(spans) > 0 && i < len(r) {
				b.WriteByte(' ')
				spans = append(spans, [2]int{start, i})
			}
			continue
		}
		b.WriteRune(r[i])
		spans = append(spans, [2]int{i, i + 1})
		i++
	}
	return b.String(), spans
}

// ResolveTextLocator checks an original excerpt and narrows the canonical rune
// range. The bounding box remains the original block box. When the excerpt
// occurs multiple times or the block has no reliable geometry, return the page
// only. A missing excerpt is an error even when geometry is unavailable.
func ResolveTextLocator(s GuideSourceSnapshot, evidenceID, quote string) (GuideLocator, error) {
	e, b, ok := sourceBlock(s, evidenceID)
	if !ok || (e.ParentAssetID == "" && (b.Text == "" || b.Image != nil || b.Table != nil || b.Type == "formula")) {
		return GuideLocator{}, invalid("text evidence ID")
	}
	text := b.Text
	if e.SourceRegionID != "" {
		text = e.Text
	}
	start, end, ambiguous, err := findExcerpt(text, quote)
	if err != nil {
		return GuideLocator{}, err
	}
	if ambiguous || e.Locator.Kind != "text_span" {
		return GuideLocator{Kind: "page", Page: e.Locator.Page}, nil
	}
	out := e.Locator
	out.Start, out.End = start, end
	return out, nil
}

func sourceBlock(s GuideSourceSnapshot, evidenceID string) (GuideEvidence, pdfir.Block, bool) {
	for _, e := range s.Evidence {
		if e.ID != evidenceID {
			continue
		}
		for _, b := range s.Blocks {
			if b.ID == e.SourceBlockID {
				return e, b, true
			}
		}
		break
	}
	return GuideEvidence{}, pdfir.Block{}, false
}

// ResolveTableLocator uses parsed physical cell origins. The server derives
// header context from logical column spans. If layout coverage is uncertain,
// the original whole-table or page locator is retained.
func ResolveTableLocator(s GuideSourceSnapshot, evidenceID string, cells []GuideCell) (GuideLocator, error) {
	e, b, ok := sourceBlock(s, evidenceID)
	if !ok || e.ParentAssetID != "" || b.Table == nil || b.Type != "table" || len(cells) > 64 {
		return GuideLocator{}, invalid("table evidence ID or cell count")
	}
	out := e.Locator
	if len(e.FragmentEvidenceIDs) > 0 && len(cells) > 0 {
		return GuideLocator{}, invalid("physical table fragments require page evidence")
	}
	if len(cells) == 0 {
		return out, nil
	}
	type placed struct {
		origin      GuideCell
		first, last int
		cell        pdfir.TableCell
	}
	chosen := map[GuideCell]bool{}
	for _, c := range cells {
		if c.Row < 0 || c.Row >= len(b.Table.Rows) || c.Column < 0 || c.Column >= len(b.Table.Rows[c.Row].Cells) || chosen[c] {
			return GuideLocator{}, invalid("table cell origin")
		}
		chosen[c] = true
	}
	if out.Kind != "table_cells" {
		return out, nil
	}
	occupied := map[int]int{} // column to first row after the active rowspan
	var placedCells []placed
	safe := true
	for row, r := range b.Table.Rows {
		col := 0
		for i, c := range r.Cells {
			for occupied[col] > row {
				col++
			}
			if c.ColSpan < 1 || c.ColSpan > 256 || c.RowSpan < 1 || row+c.RowSpan > len(b.Table.Rows) || col+c.ColSpan > 4096 {
				safe = false
				break
			}
			for x := col; x < col+c.ColSpan; x++ {
				if occupied[x] > row {
					safe = false
					break
				}
				occupied[x] = row + c.RowSpan
			}
			placedCells = append(placedCells, placed{GuideCell{row, i}, col, col + c.ColSpan, c})
			col += c.ColSpan
		}
		if !safe {
			break
		}
	}
	if !safe {
		return out, nil
	}
	out.Cells = append([]GuideCell(nil), cells...)
	headers := map[string]bool{}
	for _, header := range placedCells {
		if !header.cell.Header || header.cell.Text == "" || headers[header.cell.Text] {
			continue
		}
		for _, value := range placedCells {
			if chosen[value.origin] && header.origin.Row < value.origin.Row && header.first < value.last && value.first < header.last {
				out.Headers = append(out.Headers, header.cell.Text)
				headers[header.cell.Text] = true
				break
			}
		}
		if len(out.Headers) == 64 {
			break
		}
	}
	return out, nil
}
