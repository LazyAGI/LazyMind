package sourceurl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const GitHubAPIBaseURL = "https://api.github.com"

type GitHubResolution struct {
	DownloadURL string
	PathPrefix  string
}

// ResolveGitHubPageURL recognizes supported GitHub repository page URLs and
// resolves them to repository archives. Direct release assets and non-GitHub
// URLs are left to the caller by returning matched=false.
func ResolveGitHubPageURL(ctx context.Context, parsed *url.URL, client *http.Client, apiBaseURL string) (resolution GitHubResolution, matched bool, err error) {
	if parsed == nil {
		return GitHubResolution{}, false, nil
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host != "github.com" {
		return GitHubResolution{}, false, nil
	}
	parts, err := githubURLPathParts(parsed.EscapedPath())
	if err != nil {
		return GitHubResolution{}, true, err
	}
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return GitHubResolution{}, true, fmt.Errorf("GitHub URL must identify a repository")
	}

	// Release assets are already direct package URLs and should continue down
	// the generic HTTP(S) download path without GitHub page resolution.
	if isGitHubReleaseZip(parts) {
		return GitHubResolution{}, false, nil
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return GitHubResolution{}, true, fmt.Errorf("GitHub URL must not contain credentials, port, query, or fragment")
	}
	owner := parts[0]
	repository := strings.TrimSuffix(parts[1], ".git")
	if !isValidGitHubName(owner) || !isValidGitHubName(repository) {
		return GitHubResolution{}, true, fmt.Errorf("GitHub URL must identify a repository")
	}
	if ref, ok := githubArchiveRef(parts); ok {
		return GitHubResolution{DownloadURL: githubArchiveURL(owner, repository, ref)}, true, nil
	}
	if len(parts) == 2 {
		ref, err := resolveGitHubDefaultBranch(ctx, client, apiBaseURL, owner, repository)
		if err != nil {
			return GitHubResolution{}, true, err
		}
		return GitHubResolution{DownloadURL: githubArchiveURL(owner, repository, ref)}, true, nil
	}
	if len(parts) < 5 || parts[2] != "tree" {
		return GitHubResolution{}, true, fmt.Errorf("GitHub URL must point to a repository root, direct ZIP, or /tree/<ref>/<skill-path>")
	}
	ref, pathPrefix, err := resolveGitHubTreeRef(ctx, client, apiBaseURL, owner, repository, parts[3:])
	if err != nil {
		return GitHubResolution{}, true, err
	}
	return GitHubResolution{DownloadURL: githubArchiveURL(owner, repository, ref), PathPrefix: pathPrefix}, true, nil
}

// ResolveGitHubPageURLFromResolvedArchive reconstructs a GitHub page
// resolution from an archive URL that was already recorded in a lock file.
// This keeps frozen builds deterministic and avoids querying the GitHub API
// again merely to rediscover the repository's default branch or tree ref.
func ResolveGitHubPageURLFromResolvedArchive(parsed *url.URL, resolvedArchiveURL string) (resolution GitHubResolution, matched bool, err error) {
	if parsed == nil {
		return GitHubResolution{}, false, nil
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host != "github.com" {
		return GitHubResolution{}, false, nil
	}
	parts, err := githubURLPathParts(parsed.EscapedPath())
	if err != nil {
		return GitHubResolution{}, true, err
	}
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return GitHubResolution{}, true, fmt.Errorf("GitHub URL must identify a repository")
	}
	if isGitHubReleaseZip(parts) {
		return GitHubResolution{}, false, nil
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return GitHubResolution{}, true, fmt.Errorf("GitHub URL must not contain credentials, port, query, or fragment")
	}
	owner := parts[0]
	repository := strings.TrimSuffix(parts[1], ".git")
	if !isValidGitHubName(owner) || !isValidGitHubName(repository) {
		return GitHubResolution{}, true, fmt.Errorf("GitHub URL must identify a repository")
	}
	if ref, ok := githubArchiveRef(parts); ok {
		return GitHubResolution{DownloadURL: githubArchiveURL(owner, repository, ref)}, true, nil
	}

	ref, err := githubResolvedArchiveRef(resolvedArchiveURL, owner, repository)
	if err != nil {
		return GitHubResolution{}, true, err
	}
	if len(parts) == 2 {
		return GitHubResolution{DownloadURL: resolvedArchiveURL}, true, nil
	}
	if len(parts) < 5 || parts[2] != "tree" {
		return GitHubResolution{}, true, fmt.Errorf("GitHub URL must point to a repository root, direct ZIP, or /tree/<ref>/<skill-path>")
	}
	treeParts := parts[3:]
	refParts := strings.Split(ref, "/")
	if len(treeParts) <= len(refParts) {
		return GitHubResolution{}, true, fmt.Errorf("locked GitHub archive ref does not match source URL")
	}
	for index := range refParts {
		if treeParts[index] != refParts[index] {
			return GitHubResolution{}, true, fmt.Errorf("locked GitHub archive ref does not match source URL")
		}
	}
	return GitHubResolution{
		DownloadURL: resolvedArchiveURL,
		PathPrefix:  strings.Join(treeParts[len(refParts):], "/"),
	}, true, nil
}

func githubResolvedArchiveRef(raw, owner, repository string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.") != "github.com" {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	escapedParts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(escapedParts) < 4 || escapedParts[2] != "archive" {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	resolvedOwner, ownerErr := url.PathUnescape(escapedParts[0])
	resolvedRepository, repositoryErr := url.PathUnescape(escapedParts[1])
	if ownerErr != nil || repositoryErr != nil || !strings.EqualFold(resolvedOwner, owner) || !strings.EqualFold(strings.TrimSuffix(resolvedRepository, ".git"), repository) {
		return "", fmt.Errorf("locked GitHub archive URL does not match source repository")
	}
	escapedRef := strings.Join(escapedParts[3:], "/")
	if !strings.HasSuffix(strings.ToLower(escapedRef), ".zip") {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	escapedRef = escapedRef[:len(escapedRef)-len(".zip")]
	ref, err := url.PathUnescape(escapedRef)
	if err != nil {
		return "", fmt.Errorf("locked GitHub archive URL is invalid")
	}
	refParts := strings.Split(ref, "/")
	if len(refParts) >= 3 && refParts[0] == "refs" && (refParts[1] == "heads" || refParts[1] == "tags") {
		refParts = refParts[2:]
	}
	for _, part := range refParts {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, '\\') || strings.ContainsRune(part, 0) {
			return "", fmt.Errorf("locked GitHub archive URL is invalid")
		}
	}
	return strings.Join(refParts, "/"), nil
}

func isGitHubReleaseZip(parts []string) bool {
	return len(parts) >= 6 && parts[2] == "releases" && parts[3] == "download" && strings.HasSuffix(strings.ToLower(parts[len(parts)-1]), ".zip")
}

func githubArchiveRef(parts []string) (string, bool) {
	if len(parts) < 4 || parts[2] != "archive" {
		return "", false
	}
	refParts := append([]string(nil), parts[3:]...)
	if len(refParts) >= 3 && refParts[0] == "refs" && (refParts[1] == "heads" || refParts[1] == "tags") {
		refParts = refParts[2:]
	} else if len(refParts) != 1 {
		return "", false
	}
	last := refParts[len(refParts)-1]
	if !strings.HasSuffix(last, ".zip") {
		return "", false
	}
	refParts[len(refParts)-1] = strings.TrimSuffix(last, ".zip")
	if refParts[len(refParts)-1] == "" {
		return "", false
	}
	return strings.Join(refParts, "/"), true
}

func githubURLPathParts(escapedPath string) ([]string, error) {
	rawParts := strings.Split(strings.Trim(escapedPath, "/"), "/")
	if len(rawParts) == 1 && rawParts[0] == "" {
		return nil, fmt.Errorf("GitHub URL must identify a repository")
	}
	parts := make([]string, 0, len(rawParts))
	for _, rawPart := range rawParts {
		part, err := url.PathUnescape(rawPart)
		if err != nil || part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\`) || strings.ContainsRune(part, 0) {
			return nil, fmt.Errorf("GitHub URL contains an invalid path segment")
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func resolveGitHubDefaultBranch(ctx context.Context, client *http.Client, apiBaseURL, owner, repository string) (string, error) {
	var response struct {
		DefaultBranch string `json:"default_branch"`
	}
	endpoint := strings.TrimRight(apiBaseURL, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository)
	status, err := githubAPIGet(ctx, client, endpoint, &response)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 || strings.TrimSpace(response.DefaultBranch) == "" {
		return "", fmt.Errorf("GitHub repository default branch could not be resolved")
	}
	return response.DefaultBranch, nil
}

func resolveGitHubTreeRef(ctx context.Context, client *http.Client, apiBaseURL, owner, repository string, treeParts []string) (string, string, error) {
	for split := len(treeParts) - 1; split > 0; split-- {
		ref := strings.Join(treeParts[:split], "/")
		pathPrefix := strings.Join(treeParts[split:], "/")
		endpoint := strings.TrimRight(apiBaseURL, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/commits/" + url.PathEscape(ref)
		status, err := githubAPIGet(ctx, client, endpoint, nil)
		if err != nil {
			return "", "", err
		}
		if status == http.StatusNotFound || status == http.StatusUnprocessableEntity {
			continue
		}
		if status >= 200 && status < 300 {
			return ref, pathPrefix, nil
		}
		return "", "", fmt.Errorf("GitHub ref lookup failed with HTTP status %d", status)
	}
	return "", "", fmt.Errorf("GitHub URL ref could not be resolved")
}

func githubAPIGet(ctx context.Context, client *http.Client, endpoint string, out any) (int, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "LazyMind-Skill-Importer")
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("GitHub ref lookup failed: %w", err)
	}
	defer response.Body.Close()
	if out != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out); err != nil {
			return 0, fmt.Errorf("GitHub ref lookup returned invalid JSON: %w", err)
		}
	}
	return response.StatusCode, nil
}

func githubArchiveURL(owner, repository, ref string) string {
	return "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/archive/" + url.PathEscape(ref) + ".zip"
}

func isValidGitHubName(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}
