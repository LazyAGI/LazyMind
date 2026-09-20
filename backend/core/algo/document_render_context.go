package algo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"unicode/utf8"
)

type DocumentCodeFenceDisplay struct {
	Start    int    `json:"start" required:"true"`
	End      int    `json:"end" required:"true"`
	Language string `json:"language" required:"true"`
}
type DocumentImageDisplay struct {
	Start  int  `json:"start" required:"true"`
	End    int  `json:"end" required:"true"`
	Width  int  `json:"width" required:"true"`
	Height *int `json:"height,omitempty"`
}
type DocumentRenderContext struct {
	SourceHash string                     `json:"source_hash" required:"true"`
	CodeFences []DocumentCodeFenceDisplay `json:"code_fences" required:"true"`
	Images     []DocumentImageDisplay     `json:"images" required:"true"`
}

var displayLanguage = regexp.MustCompile(`^[A-Za-z0-9_+.-]{1,40}$`)

// ValidFor binds optional display hints to the exact canonical Markdown source.
func (c *DocumentRenderContext) ValidFor(raw json.RawMessage) bool {
	var source string
	if json.Unmarshal(raw, &source) != nil || len(c.CodeFences)+len(c.Images) > 1000 {
		return false
	}
	sum := sha256.Sum256([]byte(source))
	if c.SourceHash != hex.EncodeToString(sum[:]) {
		return false
	}
	length := utf8.RuneCountInString(source)
	previous := 0
	for _, item := range c.CodeFences {
		if item.Start < previous || item.End <= item.Start || item.End > length || !displayLanguage.MatchString(item.Language) {
			return false
		}
		previous = item.End
	}
	previous = 0
	for _, item := range c.Images {
		if item.Start < previous || item.End <= item.Start || item.End > length || item.Width < 1 || item.Width > 10000 || (item.Height != nil && (*item.Height < 1 || *item.Height > 10000)) {
			return false
		}
		previous = item.End
	}
	return true
}
