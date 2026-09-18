package externalcapability

import (
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"strings"

	"lazymind/core/doc"
)

var generatedImagePath = regexp.MustCompile(`^/static-files/ai_generated/[A-Za-z0-9_-]+\.(png|jpg|jpeg|webp|gif)$`)

// Only trusted builtin image results are passed here. Never sign arbitrary MCP
// output, remote URLs, or filesystem paths. History ownership is checked first.
func rewriteImageResult(raw []byte, fresh bool) json.RawMessage {
	return mapImageResultURLs(raw, func(path string, _ *url.URL) string {
		if fresh {
			return doc.StaticFileURLFromAnyStoragePath(path)
		}
		return path
	})
}

// External clients cannot resolve server-relative paths against LazyMind.
// Keep the execution signature, and use only the configured public endpoint.
func externalImageResult(raw []byte) json.RawMessage {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LAZYMIND_PUBLIC_BASE_URL")), "/")
	if base == "" {
		base = "http://localhost:8000/api/core"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return raw
	}
	return mapImageResultURLs(raw, func(path string, source *url.URL) string {
		result := base + path
		if source.RawQuery != "" {
			result += "?" + source.RawQuery
		}
		return result
	})
}

func mapImageResultURLs(raw []byte, rewrite func(string, *url.URL) string) json.RawMessage {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	var walk func(any)
	walk = func(value any) {
		switch item := value.(type) {
		case []any:
			for _, child := range item {
				walk(child)
			}
		case map[string]any:
			for _, child := range item {
				walk(child)
			}
			original, ok := item["image_url"].(string)
			if !ok {
				return
			}
			u, err := url.Parse(original)
			if err != nil || u.IsAbs() || u.Host != "" || u.RawPath != "" || u.Fragment != "" {
				return
			}
			path := strings.TrimPrefix(u.Path, "/api/core")
			if !generatedImagePath.MatchString(path) {
				return
			}
			replacement := rewrite(path, u)
			item["image_url"] = replacement
			if markdown, ok := item["image_markdown"].(string); ok {
				item["image_markdown"] = strings.ReplaceAll(markdown, original, replacement)
			}
		}
	}
	walk(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}
