// Package pdfir owns original-PDF interpretation, independently of retrieval and translation.
package pdfir

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strings"
)

const MaxSourceBytes int64 = 128 << 20
const MaxArchiveBytes int64 = 256 << 20
const maxJSONBytes int64 = 32 << 20
const Revision = "mineru-vlm-ir-v1"

type Parser struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}
type Page struct {
	Index  int     `json:"index"`
	Width  float64 `json:"width,omitempty"`
	Height float64 `json:"height,omitempty"`
}
type Block struct {
	Image *Image    `json:"image,omitempty"`
	Table *Table    `json:"table,omitempty"`
	ID    string    `json:"id"`
	Type  string    `json:"type"`
	Page  int       `json:"page"`
	BBox  []float64 `json:"bbox,omitempty"`
	Latex string    `json:"latex,omitempty"`
	Text  string    `json:"text,omitempty"`
}
type Document struct {
	ProjectionVersion int      `json:"projection_version,omitempty"`
	Version           int      `json:"version"`
	SourceSHA256      string   `json:"source_sha256"`
	Parser            Parser   `json:"parser"`
	CoordinateSpace   string   `json:"coordinate_space"`
	Pages             []Page   `json:"pages"`
	Blocks            []Block  `json:"blocks"`
	Warnings          []string `json:"warnings,omitempty"`
}

func Digest(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }
func SourceHash(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, MaxSourceBytes+1))
	if e != nil {
		return "", e
	}
	if n > MaxSourceBytes {
		return "", errors.New("PDF exceeds 128 MiB limit")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func bounded(r io.Reader, n int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, n+1))
	if e == nil && int64(len(b)) > n {
		e = errors.New("MinerU response exceeds size limit")
	}
	return b, e
}
func AdaptZIP(raw []byte, hash, model string) (Document, error) {
	d := Document{Version: 1, SourceSHA256: hash, Parser: Parser{"mineru", model}, CoordinateSpace: "normalized_top_left", Pages: []Page{}, Blocks: []Block{}}
	if int64(len(raw)) > MaxArchiveBytes {
		return d, errors.New("archive too large")
	}
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return d, e
	}
	var content, layout []byte
	layoutCount := 0
	for _, f := range z.File {
		if path.IsAbs(f.Name) || strings.Contains(f.Name, "\\") || strings.Contains("/"+f.Name, "/../") {
			return d, errors.New("unsafe archive member")
		}
		if path.Base(f.Name) == "layout.json" || strings.HasSuffix(f.Name, "_middle.json") {
			layoutCount++
			if layoutCount == 1 {
				r, err := f.Open()
				if err == nil {
					layout, err = bounded(r, maxJSONBytes)
					if err != nil {
						layout = nil
					}
					r.Close()
				}
			} else {
				layout = nil
			}
		}
		if strings.HasSuffix(f.Name, "_content_list.json") {
			if content != nil {
				return d, errors.New("ambiguous content lists")
			}
			r, e := f.Open()
			if e != nil {
				return d, e
			}
			content, e = bounded(r, maxJSONBytes)
			r.Close()
			if e != nil {
				return d, e
			}
		}
	}
	if content == nil {
		return d, errors.New("missing content list")
	}
	var items []contentItem

	if e = json.Unmarshal(content, &items); e != nil {
		return d, e
	}
	pages := map[int]bool{}
	for i, v := range items {
		if v.Page == nil || *v.Page < 0 || *v.Page > 100000 {
			return d, errors.New("invalid page index")
		}
		b := Block{ID: Digest(fmt.Sprintf("%s:%s:%s:%d", hash, Revision, model, i))[:24], Type: v.Type, Page: *v.Page, Text: v.Text}
		pages[b.Page] = true
		if b.Type == "equation" || b.Type == "formula" {
			b.Type = "formula"
			b.Latex = v.Latex
			if b.Latex == "" {
				b.Latex = v.Text
			}
			b.Latex = strings.TrimSpace(b.Latex)
			for _, pair := range [][2]string{{"$$", "$$"}, {"\\[", "\\]"}, {"$", "$"}} {
				if strings.HasPrefix(b.Latex, pair[0]) && strings.HasSuffix(b.Latex, pair[1]) && len(b.Latex) >= len(pair[0])+len(pair[1]) {
					b.Latex = strings.TrimSpace(b.Latex[len(pair[0]) : len(b.Latex)-len(pair[1])])
					break
				}
			}
		}
		valid := len(v.BBox) == 4
		for _, x := range v.BBox {
			valid = valid && !math.IsNaN(x) && !math.IsInf(x, 0) && x >= 0 && x <= 1000
		}
		if valid && v.BBox[0] < v.BBox[2] && v.BBox[1] < v.BBox[3] {
			for _, x := range v.BBox {
				b.BBox = append(b.BBox, x/1000)
			}
		} else {
			d.Warnings = append(d.Warnings, fmt.Sprintf("Block %d has no reliable geometry", i))
		}
		d.Blocks = append(d.Blocks, b)
	}
	for p := range pages {
		d.Pages = append(d.Pages, Page{Index: p})
	}
	sort.Slice(d.Pages, func(i, j int) bool { return d.Pages[i].Index < d.Pages[j].Index })
	projectTables(&d, items, layout)
	projectImages(&d, items, layout)
	return d, nil
}
