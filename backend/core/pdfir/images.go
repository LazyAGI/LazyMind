package pdfir

import (
	"fmt"
	"regexp"
	"strings"
)

type Image struct {
	Captions      []string        `json:"captions"`
	Footnotes     []string        `json:"footnotes"`
	Number        string          `json:"number,omitempty"`
	RelatedBlocks []TableRelation `json:"related_blocks"`
	Warnings      []string        `json:"warnings,omitempty"`
}

var figureLabel = regexp.MustCompile(`(?i)(?:\bfigure\s+|\bfig\.\s*|图\s*)([A-Z]?\d+(?:\.\d+)*[a-z]?)\b`)

func figureNumber(s string) string {
	m := figureLabel.FindStringSubmatchIndex(s)
	if m == nil || strings.TrimSpace(s[:m[0]]) != "" {
		return ""
	}
	return strings.ToUpper(s[m[2]:m[3]])
}
func projectImages(d *Document, items []contentItem, layout []byte) {
	perPage := map[int]int{}
	for i, v := range items {
		if v.Type != "image" {
			continue
		}
		perPage[*v.Page]++
		im := &Image{Captions: []string{}, Footnotes: []string{}, RelatedBlocks: []TableRelation{}}
		for _, s := range v.ImageFootnotes {
			im.Footnotes = append(im.Footnotes, plainTableText(s))
		}
		d.Blocks[i].Image = im
	}
	captions := layoutCaptionsOfType(layout, "image_caption")
	direction := 0
	conflict := false
	for _, c := range captions {
		below, above := captionCandidatesOfType(d, c, 1, "image"), captionCandidatesOfType(d, c, -1, "image")
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
			if figureNumber(c.text) == "" {
				continue
			}
			matches := captionCandidatesOfType(d, c, direction, "image")
			if len(matches) == 1 {
				assignments[matches[0]] = append(assignments[matches[0]], c)
			}
		}
	}
	for i, v := range items {
		im := d.Blocks[i].Image
		if im == nil {
			continue
		}
		if cs := assignments[i]; len(cs) == 1 {
			c := cs[0]
			im.Captions = []string{c.text}
			im.Number = figureNumber(c.text)
			id := Digest(fmt.Sprintf("%s:%s:image-caption:%d:%v:%s", d.SourceSHA256, Revision, c.page, c.box, c.text))[:24]
			im.RelatedBlocks = append(im.RelatedBlocks, TableRelation{id, "caption"})
			d.Blocks = append(d.Blocks, Block{ID: id, Type: "image_caption", Page: c.page, BBox: c.box, Text: c.text})
		} else if len(captions) == 0 && perPage[*v.Page] == 1 {
			for _, s := range v.ImageCaptions {
				s = plainTableText(s)
				if figureNumber(s) != "" {
					im.Captions = append(im.Captions, s)
				}
			}
			if len(im.Captions) == 1 {
				im.Number = figureNumber(im.Captions[0])
			} else {
				im.Captions = []string{}
				im.Warnings = append(im.Warnings, "Figure caption association is ambiguous or unavailable")
			}
		} else {
			im.Warnings = append(im.Warnings, "Figure caption association is ambiguous or unavailable")
		}
	}
	numbers := map[string]int{}
	for _, b := range d.Blocks {
		if b.Image != nil && b.Image.Number != "" {
			numbers[b.Image.Number]++
		}
	}
	for i := range items {
		im := d.Blocks[i].Image
		if im == nil || im.Number == "" {
			continue
		}
		if numbers[im.Number] != 1 {
			im.Warnings = append(im.Warnings, "Figure number is not unique; paragraph links omitted")
			continue
		}
		for _, b := range d.Blocks[:len(items)] {
			if b.Type == "image" || b.Type == "table" || b.Type == "formula" {
				continue
			}
			for _, m := range figureLabel.FindAllStringSubmatch(b.Text, -1) {
				if strings.ToUpper(m[1]) == im.Number {
					relation := "mention"
					if len(im.Captions) == 1 && plainTableText(b.Text) == im.Captions[0] {
						relation = "caption"
					}
					im.RelatedBlocks = append(im.RelatedBlocks, TableRelation{b.ID, relation})
					break
				}
			}
		}
	}
}
