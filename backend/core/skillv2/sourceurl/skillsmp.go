package sourceurl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const skillsMPMaxPageBytes = 2 << 20

type SkillsMPResolution struct {
	GitHubSourceURL string
	CanonicalName   string
}

// ResolveSkillsMPPageURL reads the exact GitHub source declared by a SkillsMP
// detail page. SkillsMP slugs are opaque: they are never split to infer a source.
func ResolveSkillsMPPageURL(ctx context.Context, parsed *url.URL, client *http.Client) (SkillsMPResolution, bool, error) {
	if parsed == nil || !isSkillsMPHost(parsed.Hostname()) {
		return SkillsMPResolution{}, false, nil
	}
	if err := validateSkillsMPPageURL(parsed); err != nil {
		return SkillsMPResolution{}, true, err
	}
	pageURL := *parsed
	pageURL.RawQuery, pageURL.Fragment = "", ""
	if client == nil {
		client = http.DefaultClient
	}
	pageClient := *client
	pageClient.Jar = nil
	if pageClient.Timeout == 0 || pageClient.Timeout > 15*time.Second {
		pageClient.Timeout = 15 * time.Second
	}
	pageClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("SkillsMP page redirected too many times")
		}
		if err := validateSkillsMPPageURL(req.URL); err != nil {
			return err
		}
		req.URL.RawQuery, req.URL.Fragment = "", ""
		req.Header.Del("Authorization")
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return SkillsMPResolution{}, true, fmt.Errorf("SkillsMP page request could not be created")
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "LazyMind/1.0")
	response, err := pageClient.Do(req)
	if err != nil {
		return SkillsMPResolution{}, true, fmt.Errorf("SkillsMP page could not be fetched: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return SkillsMPResolution{}, true, fmt.Errorf("SkillsMP page returned HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, skillsMPMaxPageBytes+1))
	if err != nil {
		return SkillsMPResolution{}, true, fmt.Errorf("SkillsMP page could not be read: %w", err)
	}
	if len(body) > skillsMPMaxPageBytes {
		return SkillsMPResolution{}, true, fmt.Errorf("SkillsMP page exceeds the size limit")
	}
	resolution, err := skillsMPSourceFromHTML(body)
	return resolution, true, err
}

func isSkillsMPHost(host string) bool {
	return strings.EqualFold(strings.TrimPrefix(strings.ToLower(host), "www."), "skillsmp.com")
}

func validateSkillsMPPageURL(parsed *url.URL) error {
	if parsed.Scheme != "https" || !isSkillsMPHost(parsed.Hostname()) || parsed.User != nil || parsed.Port() != "" {
		return fmt.Errorf("SkillsMP URL must use HTTPS without credentials or a port")
	}
	parts, err := githubURLPathParts(parsed.EscapedPath())
	if err != nil {
		return fmt.Errorf("SkillsMP URL contains an invalid path segment")
	}
	if len(parts) > 0 {
		switch parts[0] {
		case "en", "zh", "ja", "ko", "es", "fr", "de", "pt", "ar":
			parts = parts[1:]
		}
	}
	if (len(parts) == 2 && parts[0] == "skills") || (len(parts) == 4 && parts[0] == "creators") {
		return nil
	}
	return fmt.Errorf("SkillsMP URL must identify a skill at /skills/<id> or /creators/<owner>/<repo>/<skill>")
}

func skillsMPSourceFromHTML(body []byte) (SkillsMPResolution, error) {
	var sources []string
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			if tokenizer.Err() != io.EOF {
				return SkillsMPResolution{}, fmt.Errorf("SkillsMP page HTML could not be read")
			}
			return uniqueSkillsMPSource(sources)
		case html.StartTagToken:
			token := tokenizer.Token()
			if token.Data != "script" {
				continue
			}
			isJSONLD := false
			for _, attribute := range token.Attr {
				if attribute.Key == "type" && strings.EqualFold(strings.TrimSpace(attribute.Val), "application/ld+json") {
					isJSONLD = true
				}
			}
			if !isJSONLD || tokenizer.Next() != html.TextToken {
				continue
			}
			var document any
			if json.Unmarshal(tokenizer.Text(), &document) != nil {
				continue
			}
			sources = append(sources, skillsMPJSONLDSources(document, 0)...)
		}
	}
}

func skillsMPJSONLDSources(document any, depth int) []string {
	if depth > 32 {
		return nil
	}
	var sources []string
	switch value := document.(type) {
	case []any:
		for _, item := range value {
			sources = append(sources, skillsMPJSONLDSources(item, depth+1)...)
		}
	case map[string]any:
		isSource := value["@type"] == "SoftwareSourceCode"
		if types, ok := value["@type"].([]any); ok {
			for _, sourceType := range types {
				if sourceType == "SoftwareSourceCode" {
					isSource = true
				}
			}
		}
		if source, ok := value["codeRepository"].(string); isSource && ok {
			sources = append(sources, source)
		}
		if graph, ok := value["@graph"]; ok {
			sources = append(sources, skillsMPJSONLDSources(graph, depth+1)...)
		}
	}
	return sources
}

func uniqueSkillsMPSource(sources []string) (SkillsMPResolution, error) {
	var result SkillsMPResolution
	for _, raw := range sources {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || strings.ToLower(parsed.Hostname()) != "github.com" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return SkillsMPResolution{}, fmt.Errorf("SkillsMP page declares an invalid GitHub source")
		}
		parts, err := githubURLPathParts(parsed.EscapedPath())
		if err != nil || len(parts) < 4 || (parts[2] != "tree" && parts[2] != "blob") {
			return SkillsMPResolution{}, fmt.Errorf("SkillsMP page must declare an exact GitHub skill directory or SKILL.md")
		}
		source, matched, err := parseGitHubSourceURL(parsed)
		if err != nil || !matched || !source.explicitDirectory {
			return SkillsMPResolution{}, fmt.Errorf("SkillsMP page declares an invalid GitHub skill source")
		}
		name := source.repository
		if len(source.treeParts) > 1 {
			name = source.treeParts[len(source.treeParts)-1]
		}
		if result.GitHubSourceURL != "" && result.GitHubSourceURL != raw {
			return SkillsMPResolution{}, fmt.Errorf("SkillsMP page declares multiple different skill sources")
		}
		result = SkillsMPResolution{GitHubSourceURL: raw, CanonicalName: name}
	}
	if result.GitHubSourceURL == "" {
		return SkillsMPResolution{}, fmt.Errorf("SkillsMP page did not declare an exact GitHub skill source")
	}
	return result, nil
}
