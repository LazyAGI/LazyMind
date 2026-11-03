package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"io"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	xhtml "golang.org/x/net/html"
)

const maxDocumentImages = 100

type imageReference struct {
	Source   string `json:"source"`
	Alt      string `json:"alt,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

// Extract from adopted document content, never from the complete media library.
func documentImageReferences(raw json.RawMessage) ([]imageReference, string, error) {
	refs := []imageReference{}
	seen := map[string]bool{}
	add := func(ref imageReference) {
		ref.Source = strings.TrimSpace(ref.Source)
		if ref.Source != "" && seen[ref.Source] {
			return
		}
		seen[ref.Source] = true
		refs = append(refs, ref)
	}
	title := ""
	var markdown string
	if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '"' && json.Unmarshal(raw, &markdown) == nil {
		source := []byte(markdown)
		root := goldmark.DefaultParser().Parse(text.NewReader(source))
		var htmlSource bytes.Buffer
		_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch n := node.(type) {
			case *ast.Image:
				add(imageReference{Source: html.UnescapeString(string(util.UnescapePunctuations(n.Destination))), Alt: string(n.Text(source))})
			case *ast.Heading:
				if title == "" {
					title = string(n.Text(source))
				}
			case *ast.RawHTML:
				for i := 0; i < n.Segments.Len(); i++ {
					segment := n.Segments.At(i)
					htmlSource.Write(segment.Value(source))
				}
				appendHTMLImageReferences(htmlSource.Bytes(), add)
				htmlSource.Reset()
			case *ast.HTMLBlock:
				for i := 0; i < n.Lines().Len(); i++ {
					segment := n.Lines().At(i)
					htmlSource.Write(segment.Value(source))
				}
				if n.HasClosure() {
					htmlSource.Write(n.ClosureLine.Value(source))
				}
				appendHTMLImageReferences(htmlSource.Bytes(), add)
				htmlSource.Reset()
			}
			return ast.WalkContinue, nil
		})
	} else {
		var document struct {
			Title  string            `json:"title"`
			Blocks []json.RawMessage `json:"blocks"`
		}
		if json.Unmarshal(raw, &document) != nil || document.Blocks == nil {
			return nil, "", errors.New("invalid document")
		}
		title = document.Title
		var visit func([]json.RawMessage, int) error
		visit = func(blocks []json.RawMessage, depth int) error {
			if depth > 64 {
				return errors.New("document nesting limit")
			}
			for _, rawBlock := range blocks {
				var block struct {
					Type       string            `json:"type"`
					Content    string            `json:"content"`
					Children   []json.RawMessage `json:"children"`
					References []struct {
						Type string `json:"type"`
						ID   string `json:"id"`
						Path string `json:"path"`
						URL  string `json:"url"`
					} `json:"references"`
				}
				if json.Unmarshal(rawBlock, &block) != nil {
					return errors.New("invalid image block")
				}
				if block.Type == "image" {
					ref := imageReference{Alt: block.Content}
					for _, item := range block.References {
						if item.Type != "media_asset" {
							continue
						}
						ref.Source = item.Path
						if ref.Source == "" {
							ref.Source = item.URL
						}
						if item.ID != "" {
							ref.Fallback = ref.Source
							ref.Source = "asset://" + item.ID
						}
						break
					}
					if ref.Source == "" {
						for _, item := range block.References {
							if item.Type == "preview_asset" {
								ref.Source = item.URL
								if ref.Source == "" {
									ref.Source = item.Path
								}
								break
							}
						}
					}
					add(ref)
				}
				if err := visit(block.Children, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		if err := visit(document.Blocks, 0); err != nil {
			return nil, "", err
		}
	}
	if len(refs) > maxDocumentImages {
		return nil, "", errors.New("too many images")
	}
	return refs, imageExportName(title), nil
}

func appendHTMLImageReferences(source []byte, add func(imageReference)) {
	tokens := xhtml.NewTokenizer(bytes.NewReader(source))
	ignored := 0
	for {
		kind := tokens.Next()
		if kind == xhtml.ErrorToken {
			if tokens.Err() == io.EOF {
				return
			}
			return
		}
		if kind != xhtml.StartTagToken && kind != xhtml.SelfClosingTagToken && kind != xhtml.EndTagToken {
			continue
		}
		token := tokens.Token()
		if token.Data == "pre" || token.Data == "code" || token.Data == "script" || token.Data == "style" {
			if kind == xhtml.EndTagToken {
				if ignored > 0 {
					ignored--
				}
			} else {
				ignored++
			}
		}
		if token.Data != "img" || kind == xhtml.EndTagToken || ignored != 0 {
			continue
		}
		ref := imageReference{}
		for _, attribute := range token.Attr {
			if attribute.Key == "src" {
				ref.Source = attribute.Val
			}
			if attribute.Key == "alt" {
				ref.Alt = attribute.Val
			}
		}
		add(ref)
	}
}

func imageExportName(value string) string {
	var name []rune
	for _, ch := range strings.TrimSpace(value) {
		if unicode.IsControl(ch) || strings.ContainsRune(`/\\:*?"<>|`, ch) {
			continue
		}
		name = append(name, ch)
		if len(name) >= 60 {
			break
		}
	}
	return strings.Trim(string(name), ". ")
}
