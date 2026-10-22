package sourceurl

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// ImportResolution is shared by all interactive import entry points. A website
// page and its upstream repository are separate so provenance is never lost.
type ImportResolution struct {
	DownloadURL       string
	PathPrefix        string
	ResolvedSourceURL string
	CanonicalName     string
	Adapter           string
}

type importAdapter func(context.Context, *url.URL, *http.Client, string) (ImportResolution, bool, error)

// ResolveImportURL runs the built-in website adapters before the generic HTTP
// package path. New websites have one explicit resolver rather than adding URL
// rewriting to handlers or UI components.
func ResolveImportURL(ctx context.Context, rawURL string, client *http.Client, apiBaseURL string) (ImportResolution, error) {
	if err := ctx.Err(); err != nil {
		return ImportResolution{}, err
	}
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || parsed.Opaque != "" {
		return ImportResolution{}, fmt.Errorf("invalid skill import URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return ImportResolution{}, fmt.Errorf("skill import URL must use HTTP or HTTPS")
	}
	if parsed.User != nil {
		return ImportResolution{}, fmt.Errorf("skill import URL must not contain credentials")
	}
	for _, adapter := range []importAdapter{resolveSkillHubImport, resolveClawHubImport, resolveSkillsSHImport, resolveSkillsMPImport, resolveGitHubImport} {
		resolution, matched, err := adapter(ctx, parsed, client, apiBaseURL)
		if err != nil || matched {
			return resolution, err
		}
	}
	return ImportResolution{DownloadURL: rawURL, Adapter: "http_zip"}, nil
}

func resolveSkillHubImport(_ context.Context, parsed *url.URL, _ *http.Client, _ string) (ImportResolution, bool, error) {
	resolution, matched, err := ResolveSkillHubPageURL(parsed)
	return ImportResolution{DownloadURL: resolution.DownloadURL, CanonicalName: path.Base(resolution.Coordinate), Adapter: "skillhub"}, matched, err
}

func resolveClawHubImport(_ context.Context, parsed *url.URL, _ *http.Client, _ string) (ImportResolution, bool, error) {
	resolution, matched, err := ResolveClawHubPageURL(parsed)
	return ImportResolution{DownloadURL: resolution.DownloadURL, CanonicalName: path.Base(resolution.Coordinate), Adapter: "clawhub"}, matched, err
}

func resolveSkillsSHImport(ctx context.Context, parsed *url.URL, client *http.Client, apiBaseURL string) (ImportResolution, bool, error) {
	resolution, matched, err := ResolveSkillsSHPageURL(ctx, parsed, client, apiBaseURL)
	if !matched || err != nil {
		return ImportResolution{}, matched, err
	}
	return resolveWebsiteGitHub(ctx, resolution.GitHubSourceURL, resolution.CanonicalName, "skills_sh", client, apiBaseURL)
}

func resolveSkillsMPImport(ctx context.Context, parsed *url.URL, client *http.Client, apiBaseURL string) (ImportResolution, bool, error) {
	resolution, matched, err := ResolveSkillsMPPageURL(ctx, parsed, client)
	if !matched || err != nil {
		return ImportResolution{}, matched, err
	}
	return resolveWebsiteGitHub(ctx, resolution.GitHubSourceURL, resolution.CanonicalName, "skillsmp", client, apiBaseURL)
}

func resolveWebsiteGitHub(ctx context.Context, source, canonicalName, adapter string, client *http.Client, apiBaseURL string) (ImportResolution, bool, error) {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme != "https" {
		return ImportResolution{}, true, fmt.Errorf("%s skill has an invalid GitHub source URL", adapter)
	}
	resolution, matched, err := resolveGitHubImport(ctx, parsed, client, apiBaseURL)
	if err != nil {
		return ImportResolution{}, true, err
	}
	if !matched {
		return ImportResolution{}, true, fmt.Errorf("%s skill must resolve to a GitHub repository directory", adapter)
	}
	resolution.Adapter, resolution.CanonicalName = adapter, canonicalName
	return resolution, true, nil
}

func resolveGitHubImport(ctx context.Context, parsed *url.URL, client *http.Client, apiBaseURL string) (ImportResolution, bool, error) {
	resolution, matched, err := ResolveGitHubImportURL(ctx, parsed, client, apiBaseURL)
	return ImportResolution{DownloadURL: resolution.DownloadURL, PathPrefix: resolution.PathPrefix, ResolvedSourceURL: parsed.String(), Adapter: "github"}, matched, err
}
